package api

import (
	"net/http"
	"regexp"
	"strings"
	"unicode"
)

// Translate at the HTTP boundary: internal errors remain stable for errors.Is,
// agent diagnostics and logs. Never expose an unknown internal error verbatim.
var errorMessages = map[string]string{
	"only administrators may manage Agent sharing":                                                            "仅管理员可管理 Agent 分配。",
	"only administrators may manage users":                                                                    "仅管理员可管理用户。",
	"allocation revision is required; reload before saving":                                                   "请重新读取分配后保存。",
	"Agent allocation changed; reload before saving":                                                          "分配已变更，请重新读取后保存。",
	"shared users cannot manage fleet-wide infrastructure":                                                    "隔离用户不可管理主机或读取主机日志。",
	"shared users cannot administer the host":                                                                 "隔离用户不可管理主机。",
	"shared users cannot read host-wide core logs":                                                            "隔离用户不可读取主机日志。",
	"administrators retain access to every Agent":                                                             "管理员始终可访问所有 Agent。",
	"at most 512 Agents may be shared with a user":                                                            "每个用户最多分配 512 个 Agent。",
	"invalid or duplicate Agent allocation":                                                                   "Agent 分配无效或重复。",
	"traffic allocations require Agent isolation":                                                             "设置共享额度须先启用 Agent 隔离。",
	"at most 256 ports per Agent":                                                                             "每个 Agent 最多分配 256 个端口。",
	"invalid, reserved or duplicate shared port":                                                              "端口无效或重复，且不能使用 10085、10086。",
	"upgrade the Agent before assigning a shared traffic allowance":                                           "请先升级 Agent，再分配共享额度。",
	"upgrade the Agent before shared deployments":                                                             "请先升级 Agent，再部署共享配置。",
	"wait for the user's running task before changing Agent isolation":                                        "请等待该用户的执行中任务结束，再启用隔离。",
	"stop the user's unallocated running cores before enabling isolation":                                     "该用户还有未分配的运行中内核，请先停止或一并分配，再启用隔离。",
	"stop the user's core before sharing an uncertain deployment":                                             "部署结果不明，请由管理员停止内核后再启用隔离。",
	"redeploy the user's configuration instead of starting a shared or uncertain core":                        "共享或状态不明的内核，请重新部署用户配置后运行。",
	"configuration owner is disabled":                                                                         "配置所属用户已停用。",
	"the user's cumulative Agent traffic allowance is exhausted":                                              "共享额度已用完，请联系管理员调整额度。",
	"this core belongs to another or untracked deployment; an administrator must stop it before reassignment": "该内核已有其他用户或未确认的配置，请由管理员停止后再分配。",
	"this core has another user's pending or running task":                                                    "该内核有其他用户的任务尚未完成。",
	"reserved shared-port accounting cannot be reset, disabled or edited; manage its user allocation instead": "共享端口不可单独清零或删除，请在用户分配中调整。",
	"an Agent supports at most 256 reserved shared ports":                                                     "每个 Agent 最多保留 256 个共享端口。",
	"an Agent supports at most 256 monitored ports":                                                           "每个 Agent 最多监控 256 个端口。",
	"stop the existing service and deploy the isolated user's configuration instead of importing it":          "请先停止现有服务，再部署隔离用户的配置，不支持直接导入。",
	"Mihomo shared groups/providers/sub-rules require explicit accounting mapping":                            "Mihomo 使用了共享代理组、代理提供者或子规则，需要明确流量统计归属。",
	"Mihomo proxy names must be unique and explicit":                                                          "Mihomo 代理必须填写名称且名称不能重复。",
	"Mihomo requires tagged listeners using global rules":                                                     "Mihomo 流量统计需要带标签且使用全局路由规则的监听器。",
	"unsupported Mihomo routing rule":                                                                         "当前 Mihomo 路由规则不支持自动统计归属，请检查规则。",
	"invalid Mihomo rule":                                                                                     "Mihomo 路由规则无效，请检查规则格式。",
	"reserved accounting tags conflict with custom Mihomo routing":                                            "统计专用标签与自定义 Mihomo 路由冲突，请核对配置后再迁移。",
	"name is required and must not exceed 100 characters":                                                     "名称不能为空且不能超过 100 个字符。",
	"engine must be mihomo, xray, sing-box, or ss-rust":                                                       "内核必须选择 Mihomo、Xray、sing-box 或 Shadowsocks Rust。",
	"port must be between 1 and 65535":                                                                        "端口必须在 1 到 65535 之间。",
	"protocol must be tcp, udp, or both":                                                                      "协议必须选择 TCP、UDP 或 TCP + UDP。",
	"cycle must be monthly or yearly":                                                                         "流量周期必须选择按月或按年。",
	"cycle_anchor is required":                                                                                "请选择流量周期的起始日期。",
	"cycle_anchor cannot be in the future":                                                                    "流量周期的起始日期不能晚于今天。",
	"limit_bytes must not exceed 9223372036854775807":                                                         "流量额度不能超过 9223372036854775807 字节，请调小额度。",
	"invalid traffic period":                                                                                  "流量统计周期无效，请检查起始日期和周期类型。",
	"invalid core configuration":                                                                              "内核配置格式无效，请检查 JSON 或 YAML 内容。",
	"SS Rust plugin transport requires explicit accounting review":                                            "SS Rust 使用了插件传输，启用流量统计前需要人工核对出口归属。",
	"SS Rust accounting requires explicit server_port":                                                        "SS Rust 流量统计需要明确配置 server_port。",
	"global outbound mark must be reviewed before per-port accounting":                                        "配置已有全局出口标记，启用按端口统计前需要人工核对，避免统计冲突。",
	"unsupported accounting engine":                                                                           "当前内核不支持此流量统计方式。",
	"no attributable listeners":                                                                               "未找到可明确归属的监听端口，请检查入站配置。",
	"no explicit default outbound":                                                                            "未配置明确的默认出口，请先设置默认出口再启用统计。",
	"balancer/endpoint routing requires explicit accounting mapping":                                          "负载均衡或端点路由需要明确的流量统计归属，请先核对路由配置。",
	"existing Xray API inbound must listen on loopback before enabling statistics":                            "启用统计前，现有 Xray API 入站必须仅监听回环地址。",
	"reserved statistics protection outbound conflicts with custom configuration":                             "统计保护专用出口与自定义配置冲突，请检查出口标签和路由。",
	"Xray API tag conflicts with proxy outbound tags":                                                         "Xray API 标签与代理出口标签冲突，请设置不同标签。",
	"unknown default outbound":                                                                                "默认出口不存在，请检查默认出口与路由标签。",
	"outbound protocol requires explicit accounting mapping":                                                  "此出口协议需要明确的流量统计归属，请先核对配置。",
	"custom outbound socket mark requires manual review":                                                      "配置已有自定义出口套接字标记，需要人工核对以避免统计冲突。",
	"invalid routing rule":                                                                                    "路由规则无效，请检查规则格式。",
	"logical/balancer rules require explicit accounting mapping":                                              "逻辑路由或负载均衡规则需要明确的流量统计归属，请先核对配置。",
	"accounting requires unique tagged single-port inbounds":                                                  "流量统计要求每个入站仅监听一个端口且使用唯一标签。",
	"Xray API route requires explicit internal inbounds":                                                      "Xray API 路由必须明确指定内部入站。",
	"Xray API route includes a non-internal inbound":                                                          "Xray API 路由包含非内部入站，请移除不属于内部 API 的入站。",
	"reserved accounting tags conflict with custom or edited routing; review configuration before migration":  "统计专用标签与现有路由冲突，请核对配置后再迁移。",
	"existing sing-box stats API requires manual integration":                                                 "已有 sing-box 统计 API，需要人工整合配置后再启用统计。",
	"listener conflicts with local accounting API":                                                            "监听端口与本地统计 API 冲突，请更换端口。",
	"invalid sing-box configuration":                                                                          "sing-box 配置格式无效，请检查 JSON 内容。",
	"custom routing_mark requires manual review":                                                              "已有自定义 routing_mark，需要人工核对以避免统计冲突。",
	"conflicting accounting mark":                                                                             "流量统计标记冲突，请检查出口标记配置。",
	"invalid accounting configuration":                                                                        "流量统计配置无效，请检查入站、出口和路由归属。",
	"generated accounting entries are read-only; edit the original outbound or routing rule":                  "自动生成的统计条目为只读，请修改原始出口或路由规则。",
	"username already exists":                                                                                 "用户名已存在，请更换用户名。",
	"cannot disable or demote the last active administrator":                                                  "不能停用或降级最后一个可用的管理员账号，请先添加其他管理员。",
	"duplicate value": "该值已存在，请检查名称或标识是否重复。",
	"configuration changed; reload before saving":                                                                 "配置已被其他操作修改，请重新加载后再保存。",
	"configuration changed; reload before restoring":                                                              "配置已发生变化，请重新加载后再恢复历史版本。",
	"configuration changed after saving; no task was submitted, reload before deployment":                         "保存后配置已发生变化，本次未提交任务，请重新加载后再部署。",
	"configuration ownership changed during restore":                                                              "配置归属已变化，请刷新页面后重新选择历史版本。",
	"revision does not belong to the current configuration":                                                       "所选历史版本不属于当前配置，请重新选择。",
	"configuration version is required":                                                                           "缺少配置版本号，请刷新配置后重试。",
	"expected configuration version must be positive":                                                             "预期配置版本号必须是正整数，请刷新配置后重试。",
	"invalid configuration version":                                                                               "配置版本号无效，请刷新配置后重试。",
	"configuration ID and version are required":                                                                   "缺少配置标识或版本号，请重新加载配置。",
	"configuration name is required and must not exceed 100 characters":                                           "配置名称不能为空且不能超过 100 个字符。",
	"configuration description exceeds 300 characters":                                                            "配置说明不能超过 300 个字符。",
	"template name is required and limited to 100 characters":                                                     "模板名称不能为空且不能超过 100 个字符。",
	"template content must be between 1 byte and the configuration limit":                                         "模板内容不能为空且不能超过配置大小限制。",
	"profile requires engine, tag, and port":                                                                      "客户端配置必须提供内核、入站标签和端口。",
	"ambiguous deployed profile":                                                                                  "匹配到多个已部署的客户端配置，请检查入站标签和端口是否重复。",
	"deployed profile changed; refresh before saving":                                                             "已部署的客户端配置发生变化，请刷新后再保存。",
	"settings revision is required":                                                                               "缺少设置版本号，请刷新设置页面后重试。",
	"settings were changed in another session":                                                                    "设置已在其他会话中修改，请刷新页面后再保存。",
	"explicit engine selection required":                                                                          "请明确选择需要启用的内核。",
	"unsupported country/region code":                                                                             "国家或地区代码不受支持，请重新选择。",
	"agent name must contain 1 to 100 characters without control characters":                                      "节点名称须为 1 到 100 个字符且不能包含控制字符。",
	"agent name is empty":                                                                                         "节点名称不能为空。",
	"agent ID is required":                                                                                        "请先选择节点。",
	"client name must not exceed 100 characters or contain control characters":                                    "客户端名称不能超过 100 个字符或包含控制字符。",
	"client metadata tag is too long":                                                                             "客户端入站标签过长，请缩短后重试。",
	"client metadata is too large":                                                                                "客户端显示信息过大，请减少内容后重试。",
	"invalid client profile name scope":                                                                           "客户端名称的作用范围无效，请重新选择入站。",
	"agent does not advertise the requested engine":                                                               "节点未声明支持所选内核，请检查节点能力或升级 Agent。",
	"this Agent does not support remote upgrades; run the current one-click installation once":                    "当前 Agent 不支持远程升级，请在节点上执行一次最新的一键安装命令。",
	"upgrade this Agent before managing system BBR":                                                               "请先升级此节点的 Agent，再管理系统 BBR。",
	"this Agent cannot read the managed configuration independently; upgrade the Agent through the panel first":   "当前 Agent 不支持独立读取托管配置，请先通过面板升级 Agent。",
	"this Agent does not support the Mihomo Alpha mirror source; upgrade the Agent through the panel first":       "当前 Agent 不支持 Mihomo Alpha 镜像源，请先通过面板升级 Agent。",
	"another system TCP task is pending or running":                                                               "已有系统 TCP 调优任务等待执行或正在执行，请等待完成后重试。",
	"only pending tasks can be canceled":                                                                          "只能取消等待执行的任务，请刷新任务状态。",
	"only failed or canceled tasks can be retried":                                                                "只能重试失败或已取消的任务，请刷新任务状态。",
	"task is not running":                                                                                         "任务已不在执行中，请刷新任务状态。",
	"invalid task lease":                                                                                          "任务执行凭据已失效，请等待节点重新获取任务。",
	"task engine does not match configuration engine":                                                             "任务内核与配置内核不一致，请重新选择配置。",
	"node-owned configuration cannot be deployed to another agent":                                                "节点专属配置不能部署到其他节点，请选择原节点。",
	"existing service migration requires this agent's saved snapshot":                                             "迁移现有服务需要此节点已保存的配置快照，请重新读取并保存。",
	"node-owned configurations must use the agent configuration workflow":                                         "节点专属配置请在对应节点的配置页面中编辑。",
	"install tasks cannot reference a configuration":                                                              "安装任务不能同时指定配置，请安装后再部署配置。",
	"core source is only applicable to Mihomo development installs":                                               "内核来源仅适用于 Mihomo 开发版安装。",
	"TCP settings are only accepted by configure-tcp":                                                             "TCP 参数只能用于系统 TCP 调优任务。",
	"expected configuration version requires a configuration task":                                                "只有配置任务可以指定预期配置版本。",
	"agent-level tasks cannot reference an engine, configuration, or core version":                                "节点级任务不能指定内核、配置或内核版本。",
	"add-node name exceeds 100 characters":                                                                        "添加节点凭据的名称不能超过 100 个字符。",
	"enrollment token lifetime must be between 1 and 1440 minutes":                                                "节点注册凭据有效期必须在 1 到 1440 分钟之间。",
	"enrollment token max uses must be between 1 and 50":                                                          "节点注册凭据可用次数必须在 1 到 50 次之间。",
	"panel name is required":                                                                                      "面板名称不能为空。",
	"panel name must not exceed 40 characters":                                                                    "面板名称不能超过 40 个字符。",
	"panel description must not exceed 120 characters":                                                            "面板说明不能超过 120 个字符。",
	"Komari settings must not exceed 500 characters":                                                              "Komari 连接设置不能超过 500 个字符。",
	"Komari API Key contains invalid characters":                                                                  "Komari API 密钥包含无效字符，请检查后重试。",
	"Komari URL must be an absolute HTTP(S) URL without credentials, query, or fragment":                          "Komari 地址必须是完整的 HTTP(S) 地址，不能包含用户凭据、查询参数或片段。",
	"unsupported time zone":                                                                                       "不支持所选时区，请重新选择。",
	"unsupported time display":                                                                                    "不支持所选时间显示格式，请重新选择。",
	"unsupported UI font scale":                                                                                   "不支持所选界面字号，请重新选择。",
	"unsupported default config editor":                                                                           "不支持所选默认配置编辑器，请重新选择。",
	"unsupported task page size":                                                                                  "任务每页显示数量无效，请重新选择。",
	"unsupported task polling interval":                                                                           "任务刷新间隔无效，请重新选择。",
	"unsupported agent reporting interval":                                                                        "节点上报间隔无效，请重新选择。",
	"offline threshold must be at least three heartbeat intervals":                                                "离线判定时间不能小于心跳间隔的 3 倍。",
	"unsupported task recovery policy":                                                                            "任务恢复策略无效，请重新选择。",
	"unsupported public IP probe interval":                                                                        "公网 IP 探测间隔无效，请重新选择。",
	"unsupported core log minimum level":                                                                          "内核日志最低级别无效，请重新选择。",
	"unsupported core log retention policy":                                                                       "内核日志保留策略无效，请重新选择。",
	"unsupported database retention policy":                                                                       "数据库保留策略无效，请重新选择。",
	"unsupported heartbeat interval":                                                                              "心跳间隔无效，请重新选择。",
	"unsupported metrics interval":                                                                                "指标上报间隔无效，请重新选择。",
	"unsupported local core log capacity":                                                                         "本地内核日志容量无效，请重新选择。",
	"unsupported local core log rotation count":                                                                   "本地内核日志轮转数量无效，请重新选择。",
	"webhook URL must not exceed 500 characters":                                                                  "Webhook 地址不能超过 500 个字符。",
	"webhook URL must be an absolute http(s) URL":                                                                 "Webhook 地址必须是完整的 HTTP(S) 地址。",
	"public IP probe interval must be between 60 and 86400 seconds":                                               "公网 IP 探测间隔必须在 60 到 86400 秒之间。",
	"public IP probe endpoint exceeds the length limit":                                                           "公网 IP 探测地址过长，请缩短后重试。",
	"public IP probe endpoint must be an absolute HTTPS URL without credentials, query, or fragment":              "公网 IP 探测地址必须是完整的 HTTPS 地址，不能包含用户凭据、查询参数或片段。",
	"public IP probe fallback endpoint is not an approved same-family endpoint":                                   "公网 IP 备用探测地址必须使用受支持且地址协议栈一致的服务。",
	"public IP probe IPv4 fallback requires the approved ipify primary":                                           "启用 IPv4 备用探测前，请先选择受支持的 ipify 主探测地址。",
	"public IP probe IPv6 fallback requires the approved ipify primary":                                           "启用 IPv6 备用探测前，请先选择受支持的 ipify 主探测地址。",
	"Sub-Store endpoint is required and must not exceed 1000 characters":                                          "Sub-Store 地址不能为空且不能超过 1000 个字符。",
	"Sub-Store subscription name is required, must not exceed 100 characters, and cannot contain path characters": "Sub-Store 订阅名称不能为空、不能超过 100 个字符且不能包含路径字符。",
	"Sub-Store integration identity is invalid":                                                                   "Sub-Store 集成标识无效，请重新保存连接设置。",
	"Sub-Store sync mode is invalid":                                                                              "Sub-Store 同步模式无效，请重新选择。",
	"no more than 512 Sub-Store nodes can be selected":                                                            "Sub-Store 最多可选择 512 个节点。",
	"selected Sub-Store address mode is invalid":                                                                  "所选 Sub-Store 地址模式无效，请重新选择。",
	"selected Sub-Store node identity is invalid":                                                                 "所选 Sub-Store 节点标识无效，请重新选择。",
	"selected Sub-Store engine is invalid":                                                                        "所选 Sub-Store 内核无效，请重新选择。",
	"each Sub-Store node name is required and must not exceed 100 characters":                                     "每个 Sub-Store 节点名称不能为空且不能超过 100 个字符。",
	"duplicate Sub-Store node selection":                                                                          "重复选择了 Sub-Store 节点，请删除重复项。",
	"selected node is no longer available":                                                                        "所选节点已不可用，请刷新后重新选择。",
	"cross-site login request rejected":                                                                           "登录请求被安全校验拒绝：访问地址的协议、域名或端口与面板配置不一致。请使用配置的 HTTPS 地址；若通过反向代理访问，请管理员检查 QCH_BEHIND_TLS_PROXY 和 QCH_CORS_ORIGINS 配置。",
	"origin is not allowed":                                                                                       "当前访问地址未获允许，请使用面板配置的地址，或联系管理员检查 QCH_CORS_ORIGINS 中的协议、域名和端口。",
	"invalid admin token":                                                                                         "用户名、密码或管理员令牌不正确，请检查后重试。",
	"session expired":                                                                                             "登录已过期，请重新登录。",
	"authentication required":                                                                                     "请先登录后再操作。",
	"missing or invalid CSRF token":                                                                               "页面安全凭据已失效，请刷新页面或重新登录后重试。",
	"token role does not permit this operation":                                                                   "当前账号没有执行此操作的权限，请联系管理员。",
	"too many authentication failures":                                                                            "登录失败次数过多，请稍后再试。",
	"too many enrollment attempts":                                                                                "节点注册请求过于频繁，请稍后再试。",
	"too many failed agent authentication attempts":                                                               "节点认证失败次数过多，请检查节点凭据并稍后重试。",
	"valid add-node credential required":                                                                          "请提供有效的添加节点凭据。",
	"invalid, deleted, or mismatched add-node credential":                                                         "添加节点凭据无效、已删除或与节点不匹配，请重新获取安装命令。",
	"invalid signed agent request":                                                                                "节点请求签名无效，请检查节点凭据和系统时间。",
	"replayed signed request":                                                                                     "节点请求重复，已被安全校验拒绝，请重新发起请求。",
	"replayed request":                                                                                            "请求重复，已被安全校验拒绝，请重新发起请求。",
	"request body is too large":                                                                                   "提交的内容过大，请缩小内容后重试。",
	"request body must contain a single JSON value":                                                               "请求体只能包含一个 JSON 值，请检查提交格式。",
	"internal server error":                                                                                       "服务器处理失败，请稍后重试；若持续失败，请联系管理员查看服务日志。",
	"protected credential storage is unavailable; configure QCH_CONFIG_ENCRYPTION_KEY":                            "凭据加密存储不可用，请管理员配置 QCH_CONFIG_ENCRYPTION_KEY 后重试。",
	"audit unavailable; enrollment credential was not disclosed":                                                  "审计服务暂不可用，为保护凭据，本次未返回节点注册凭据，请稍后重试。",
	"not found":               "请求的资源不存在或已删除，请刷新页面后重试。",
	"conflict":                "数据状态冲突，请刷新页面后重试。",
	"invalid input":           "提交的参数无效，请检查输入后重试。",
	"invalid user permission": "用户权限无效，请重新选择权限。",
	"at least one user field must be provided":    "请至少填写一个需要修改的用户字段。",
	"invalid user role":                           "用户角色无效，请重新选择角色。",
	"display name must be at most 100 characters": "显示名称不能超过 100 个字符。",
	"cannot disable the current user session":     "不能停用当前登录账号，请使用其他管理员账号操作。",
	"password must be valid UTF-8":                "密码包含无效字符，请重新输入。",
	"password must be at least 12 bytes":          "密码至少需要 12 字节（中文字符通常占 3 字节）。",
	"password must be at most 72 bytes":           "密码不能超过 72 字节（中文字符通常占 3 字节）。",
	"username is required":                        "请输入用户名。",
	"username must be at most 64 bytes":           "用户名不能超过 64 字节。",
	"username may contain only lowercase letters, numbers, dot, underscore, and hyphen": "用户名只能包含小写字母、数字、点、下划线和短横线。",
	"enabled is required":                                               "请选择是否启用。",
	"month must use YYYY-MM":                                            "月份格式应为 YYYY-MM，例如 2026-09。",
	"agent_id is invalid":                                               "节点标识无效，请重新选择节点。",
	"policy_id is invalid":                                              "流量策略标识无效，请重新选择策略。",
	"agent_id is required":                                              "请先选择节点。",
	"configuration engine does not match the URL":                       "配置内核与请求地址不一致，请重新选择内核。",
	"revision limit must be between 1 and 100":                          "历史版本查询数量必须在 1 到 100 之间。",
	"revision version must be positive":                                 "历史版本号必须是正整数。",
	"expected_version must be positive":                                 "预期配置版本号必须是正整数，请刷新配置后重试。",
	"limit must be a positive integer":                                  "查询数量必须是正整数。",
	"limit must be between 1 and 200":                                   "查询数量必须在 1 到 200 之间。",
	"limit must be between 1 and 2000 per engine":                       "每个内核的日志查询数量必须在 1 到 2000 之间。",
	"limit must be between 1 and 5000":                                  "查询数量必须在 1 到 5000 之间。",
	"invalid task status filter":                                        "任务状态筛选条件无效，请重新选择。",
	"invalid task action filter":                                        "任务操作筛选条件无效，请重新选择。",
	"invalid agent_id filter":                                           "节点筛选条件无效，请重新选择。",
	"invalid engine filter":                                             "内核筛选条件无效，请重新选择。",
	"invalid level filter":                                              "日志级别筛选条件无效，请重新选择。",
	"before must be a positive integer":                                 "日志分页标识必须是正整数。",
	"since must be RFC3339":                                             "开始时间必须使用 RFC3339 格式，例如 2026-09-09T08:00:00+08:00。",
	"Komari integration is not configured":                              "尚未配置 Komari 集成，请先在设置中完成配置。",
	"country_code is required; use an empty string for automatic GeoIP": "请提供国家或地区代码；使用空字符串可恢复自动识别。",
	"uuid is required":                                                  "请提供服务器 UUID。",
	"Komari server UUID is invalid":                                     "Komari 服务器 UUID 无效，请检查后重试。",
	"system BBR requires agents.manage and tasks.execute":               "修改系统 BBR 需要节点管理和任务执行权限，请联系管理员。",
	"Agent is offline; reconnect before changing system BBR":            "节点已离线，请等待节点重新连接后再修改系统 BBR。",
	"agent does not support the requested engine":                       "节点不支持所选内核，请检查节点能力或升级 Agent。",
	"unknown server inbound protocol":                                   "不支持所选服务端入站协议，请重新选择。",
	"operation must be add, modify, or delete":                          "操作类型必须是新增、修改或删除。",
	"invalid preset engine or intent":                                   "预设内核或保存操作无效，请选择当前内核并使用保存校验或保存部署。",
	"mutation must be add, modify, or delete":                           "字段操作类型必须是新增、修改或删除。",
	"intent must be validate or deploy":                                 "操作意图必须是验证或部署。",
	"no saved configuration exists for this operation":                  "尚无已保存的配置，请先保存配置后再操作。",
	"unknown configuration field":                                       "配置字段不存在或不受支持，请刷新配置后重试。",
	"field already exists":                                              "配置字段已存在，请使用修改操作。",
	"field does not exist":                                              "配置字段不存在，请刷新配置或使用新增操作。",
	"a single SS Rust inbound must be selected":                         "请选择一个 SS Rust 入站。",
	"configuration cannot be empty":                                     "配置内容不能为空。",
	"mihomo configuration must be a YAML mapping":                       "Mihomo 配置必须是 YAML 映射对象。",
	"invalid mihomo YAML: multiple documents are not allowed":           "Mihomo 配置只能包含一个 YAML 文档。",
	"agent name is required and must not exceed 100 characters":         "节点名称不能为空且不能超过 100 个字符。",
	"agent version must not exceed 100 characters":                      "节点版本不能超过 100 个字符。",
	"agent OS and architecture are required":                            "节点必须提供操作系统和处理器架构。",
	"agent must declare between one and four capabilities":              "节点必须声明 1 到 4 种内核能力。",
	"an agent may have at most 16 labels":                               "每个节点最多可设置 16 个标签。",
	"agent label is too long":                                           "节点标签过长，请缩短后重试。",
	"agent must provide a valid Ed25519 public key":                     "节点必须提供有效的 Ed25519 公钥。",
	"Sub-Store redirects are not allowed":                               "Sub-Store 地址不允许重定向，请填写实际后端地址。",
}

// Keep field names, indices and numeric limits useful without leaking parser
// dumps or replacing identifiers inside already-localized messages.
var errorMessagePatterns = []struct {
	pattern *regexp.Regexp
	text    string
}{
	{regexp.MustCompile(`^ask an administrator to reserve port ([0-9]+) for this user before deploying$`), "端口 ${1} 未分配，请联系管理员。"},
	{regexp.MustCompile(`^port ([0-9]+) (?:is already reserved for another user|belongs to another user's deployed configuration|is reserved for another user)$`), "端口 ${1} 已由其他用户占用。"},
	{regexp.MustCompile(`^stop the (.+) core with successful traffic settlement before releasing reserved port ([0-9]+)$`), "请先停止 ${1} 并完成流量上报，再释放端口 ${2}。"},
	{regexp.MustCompile(`^shared port ([0-9]+) is bound to a different allocation or core; stop and release it first$`), "端口 ${1} 已绑定其他用户或内核，请先停机并释放。"},
	{regexp.MustCompile(`^remove the existing per-port quota on port ([0-9]+) before using a shared allowance$`), "请先取消端口 ${1} 的独立配额，再分配共享额度。"},
	{regexp.MustCompile(`^shared (?:configuration|configurations|listener|listeners|Xray|sing-box|SS Rust|statistics).*$`), "共享配置仅支持明确的固定端口入站，不允许动态监听或管理接口。"},
	{regexp.MustCompile(`^stop or replace the current (.+) configuration before sharing: .*$`), "现有 ${1} 配置不支持共享计费，请先停止或替换。"},
	{regexp.MustCompile(`^(.+) core tasks are disabled because an existing service could not be mapped safely: ([\s\S]+)$`), "现有 ${1} 服务无法安全接管，已禁用该内核的任务。请检查现有服务布局后重新发现。原始原因（供排查）：${2}"},
	{regexp.MustCompile(`^Mihomo (.+) requires migration to explicit listeners$`), "Mihomo 的 ${1} 需要先迁移为明确的监听器配置。"},
	{regexp.MustCompile(`^Mihomo proxy (.+) has shared transport or custom mark$`), "Mihomo 代理 ${1} 使用了共享传输或自定义标记，请人工核对统计归属。"},
	{regexp.MustCompile(`^unknown Mihomo outbound (.+)$`), "Mihomo 出口 ${1} 不存在，请检查代理名称和路由目标。"},
	{regexp.MustCompile(`^invalid or duplicate engine "([^"]+)"$`), "内核 ${1} 无效或重复，请重新选择。"},
	{regexp.MustCompile(`^unsupported action "([^"]+)"$`), "不支持任务操作 ${1}，请重新选择。"},
	{regexp.MustCompile(`^select between 1 and ([0-9]+) TCP parameters$`), "请至少选择 1 项 TCP 参数，最多可选择 ${1} 项。"},
	{regexp.MustCompile(`^unsupported TCP parameter "([^"]+)"$`), "不支持 TCP 参数 ${1}，请重新选择。"},
	{regexp.MustCompile(`^(?:invalid|unsupported) value for (\S+)$`), "参数 ${1} 的值无效或不受支持，请检查输入。"},
	{regexp.MustCompile(`^(\S+) requires ([0-9]+) integers$`), "参数 ${1} 需要填写 ${2} 个整数。"},
	{regexp.MustCompile(`^(\S+) requires ordered integers in \[([0-9]+), ([0-9]+)\]$`), "参数 ${1} 需要按从小到大的顺序填写整数，范围为 ${2} 到 ${3}。"},
	{regexp.MustCompile(`^port ([0-9]+) already has an outbound mark$`), "端口 ${1} 已有出口标记，请先检查现有统计配置。"},
	{regexp.MustCompile(`^outbounds\[([0-9]+)\] must be an object$`), "outbounds[${1}] 必须是对象。"},
	{regexp.MustCompile(`^outbounds\[([0-9]+)\]\.tag must be a string$`), "outbounds[${1}].tag 必须是字符串。"},
	{regexp.MustCompile(`^outbounds\[([0-9]+)\]\.tag duplicates an earlier outbound; assign distinct tags and update the intended route targets$`), "outbounds[${1}].tag 与其他出口重复，请设置唯一标签并更新对应路由目标。"},
	{regexp.MustCompile(`^chained, balanced or multiplexed outbound (.+) requires explicit accounting mapping$`), "出口 ${1} 使用了链式、负载均衡或多路复用，需要明确流量统计归属。"},
	{regexp.MustCompile(`^unknown route target (.+)$`), "路由目标 ${1} 不存在，请检查出口标签。"},
	{regexp.MustCompile(`^invalid JSON body: json: unknown field "([^"]+)"$`), "请求包含不支持的字段 ${1}，请检查字段名称或刷新页面后重试。"},
}

func chineseErrorMessage(status int, message string) string {
	message = strings.TrimSpace(message)
	if translated, ok := errorMessages[message]; ok {
		return translated
	}
	// Store errors wrap a stable sentinel; translate the useful cause separately.
	for _, prefix := range []string{"invalid input: ", "conflict: ", "not found: ", "forbidden: "} {
		if strings.HasPrefix(message, prefix) {
			return chineseErrorMessage(status, strings.TrimPrefix(message, prefix))
		}
	}
	for _, wrapper := range []struct{ prefix, text string }{
		{"预设配置无法建立独立出口归属，未保存或部署：", "预设配置无法建立独立出口归属，未保存或部署："},
		{"字段修改无法保持独立出口归属，未保存：", "字段修改无法保持独立出口归属，未保存："},
		{"无法安全更新独立出口配置：", "无法安全更新独立出口配置："},
		{"cannot verify previous accounting configuration: ", "无法验证原有统计配置："},
		{"stored revision is invalid: ", "已保存的历史版本无效："},
		{"rendered template is invalid: ", "模板生成的配置无效："},
	} {
		if strings.HasPrefix(message, wrapper.prefix) {
			return wrapper.text + chineseErrorMessage(status, strings.TrimPrefix(message, wrapper.prefix))
		}
	}
	for _, rule := range errorMessagePatterns {
		if rule.pattern.MatchString(message) {
			return rule.pattern.ReplaceAllString(message, rule.text)
		}
	}
	if strings.HasPrefix(message, "invalid JSON body:") {
		if strings.Contains(message, "http: request body too large") {
			return errorMessages["request body is too large"]
		}
		return "请求内容不是有效的 JSON，请检查字段类型、格式及内容大小。"
	}
	if strings.HasPrefix(message, "invalid mihomo YAML:") {
		return "Mihomo 配置的 YAML 格式无效，请检查缩进、字段类型和语法。"
	}
	if strings.HasPrefix(message, "invalid ") && strings.Contains(message, " JSON:") {
		return "内核配置的 JSON 格式无效，请检查逗号、引号、字段类型及多余内容。"
	}
	if strings.HasPrefix(message, "unsupported engine ") || strings.HasPrefix(message, "unsupported capability ") {
		return "不支持所选内核，请检查内核名称及节点能力。"
	}
	if strings.HasPrefix(message, "duplicate capability ") {
		return "节点声明了重复的内核能力，请检查节点配置。"
	}
	if strings.HasPrefix(message, "configuration exceeds ") {
		return "配置内容超过大小限制，请缩小配置后重试。"
	}
	if strings.HasSuffix(message, " configuration must be a JSON object") {
		return "内核配置必须是 JSON 对象。"
	}
	if strings.ContainsFunc(message, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		return message
	}
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "提交的参数或配置无效，请检查输入并刷新页面后重试。"
	case http.StatusUnauthorized:
		return "身份验证失败，请检查凭据或重新登录。"
	case http.StatusForbidden:
		return "请求被拒绝，请检查账号权限和面板访问地址。"
	case http.StatusNotFound:
		return errorMessages["not found"]
	case http.StatusConflict:
		return errorMessages["conflict"]
	case http.StatusTooManyRequests:
		return "请求过于频繁，请稍后重试。"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "服务暂不可用，请稍后重试；若持续失败，请联系管理员检查服务连接。"
	default:
		return errorMessages["internal server error"]
	}
}
