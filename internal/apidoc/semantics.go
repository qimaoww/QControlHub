package apidoc

import (
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

// These annotations describe validation and protocol semantics that cannot be
// inferred from a Go field type. Route existence and Key access remain derived
// exclusively from registrations and the capability registry.
func (g *generator) annotateField(name string, field object) {
	switch name {
	case "wireguard_additional_clients":
		field["description"] = "首个客户端使用既有 wireguard_client_* 字段。此列表保存额外设备，每个公钥及隧道地址须唯一。修改多客户端入站时传完整列表，空数组移除全部额外设备。"
	case "add_wireguard_client":
		field["description"] = "传入当前 WireGuard input，在草稿中追加一个独立客户端，保留监听参数和已有密钥。"
	case "tls_mode":
		field["enum"] = []string{"", "native", "nginx", "caddy", "external"}
		field["description"] = "空或 native 使用原生配置。Xray/Mihomo 独立 VLESS XHTTP TLS 支持 native、nginx、caddy。sing-box TLS WebSocket/HTTPUpgrade 支持托管或 external，HTTP/H2 托管仅 Caddy。托管后端绑定 127.0.0.1，不支持 REALITY；external 自行管理证书。面板证书按引用授权。"
	case "proxy_ingresses":
		field["description"] = "按 Nginx/Caddy 类型共享反代，每种类型一项，使用 scope=type 且不填写 port。省略/null 保留，空数组撤销；旧 mode+port 请求按类型归并。代次及升级兼容凭据由服务端生成，客户端值不授予执行权限。新增授权要求 shared-proxy-ingress-v2 Agent。"
	case "page_size":
		field["enum"] = []int{30, 50, 100, 200}
		field["default"] = 30
	case "expires_at":
		field["description"] = "RFC3339 或 null；null/省略表示不过期。更新按完整设置替换。"
	case "ip_allowlist":
		field["maxItems"] = 100
		field["description"] = "IP 或 CIDR 数组；空数组/省略为不限制，按可信代理后的来源校验。"
	case "expected_version", "expected_config_version", "revision", "version":
		field["minimum"] = 1
	case "expected_instance_id":
		field["description"] = "必须提供实际目标实例；配置任务主实例用空字符串，共享实例用实际 shr_… ID；不自动选择。"
	case "mode":
		field["default"] = "read_only"
		field["description"] = "省略为 read_only；读写模式不自动增加业务权限。"
	case "permissions":
		field["description"] = "显式业务权限数组。Key 至少一项；创建/更新不得超过调用者当前可授予权限。"
	case "content":
		field["description"] = "完整配置。Key 编辑时省略/null保留；字符串替换，空字符串仍需通过内核校验。原位脱敏占位符只能保留原值；须带当前并发标识。"
	case "owner_id", "creator_id", "role":
		field["description"] = "身份与资源归属由服务端决定；API Key 不得修改账户身份或指定其他所有者。"
	case "secrets":
		field["description"] = "读取省略。更新时省略未修改键以保留；空字符串明确清空；禁止回传脱敏占位符。"
	case "prefer_cached":
		field["default"] = false
		field["description"] = "仅 read-config/read-managed-config 可用；允许复用最近验证快照，部署前核对不要使用。"
	case "install_if_missing":
		field["default"] = false
		field["description"] = "仅配置专用任务入口允许；须指定当前配置和目标实例，并满足安装权限。"
	}
}

func (g *generator) annotateModel(name string, schema object) {
	fields, ok := schema["properties"].(object)
	if !ok {
		return
	}
	states := []string{"succeeded", "failed", "rolled_back", "recovery_incomplete"}
	switch name {
	case "api.apiKeyAuthorization":
		fields["mode"] = g.named("core.APIKeyMode")
		fields["expires_at"].(object)["description"] = "凭据的到期时间，null 表示无到期时间。"
		fields["permissions"] = object{"type": "array", "items": g.named("core.Permission"), "description": "当前有效权限。"}
		fields["ip_allowlist"] = object{"type": []string{"array", "null"}, "items": object{"type": "string"}}
		fields["endpoints"].(object)["description"] = "满足此 Key 模式与权限的 METHOD /path 模板。访问具体资源时仍验证归属。"
		schema["required"] = []string{"id", "name", "mode", "prefix", "permissions", "ip_allowlist", "expires_at", "workspace_id", "user_id", "source_ip", "endpoints"}
	case "store.TLSFrontendPreview":
		fields["status"].(object)["enum"] = []string{"blocked", "runtime_evidence_required"}
		fields["target_mode"].(object)["enum"] = []string{"nginx", "caddy"}
		fields["plan_id"].(object)["pattern"] = "^[0-9a-f]{64}$"
	case "store.TLSPortSummary":
		fields["state"].(object)["enum"] = []string{"unconfirmed", "partial", "visible_in_use", "observed_free"}
	case "store.TLSMigrationOperation":
		fields["state"].(object)["enum"] = append(states, "pending", "running", "verifying", "rolling_back", "canceled", "unchanged")
		schema["required"] = []string{"state", "plan"}
	case "store.TLSMigrationReceipt":
		fields["state"].(object)["enum"] = states
		fields["step"].(object)["enum"] = []string{"preflight", "handoff", "verify", "restore", "complete"}
		schema["required"] = []string{"state", "step", "paths"}
	case "store.TLSPathResult":
		for _, field := range []string{"candidate", "recovery"} {
			fields[field] = object{"type": "string", "enum": []string{"unknown", "failed", "succeeded"}}
		}
		schema["required"] = []string{"candidate", "recovery", "config_id", "instance_id", "engine", "tag", "path"}
	}
	if name != "core.ProxyIngress" {
		return
	}
	fields["mode"] = object{"type": "string", "enum": []string{"nginx", "caddy"}, "description": "共享的反代类型；同一共享关系每种类型至多一项。"}
	fields["scope"] = object{"type": "string", "enum": []string{"type"}, "description": "类型授权，不分配监听端口；mode-only 请求也按此语义保存。"}
	fields["port"] = object{"type": "integer", "minimum": 1, "maximum": 65535, "description": "仅兼容旧请求或旧 Agent 策略；新共享响应省略。scope=type 时不得填写，不接受 0 或保留端口。"}
	fields["generation"].(object)["readOnly"] = true
	fields["legacy"].(object)["readOnly"] = true
	fields["legacy"].(object)["description"] = "服务端生成的精确旧租约，仅发送给 Agent；共享详情不返回，客户端提交不生效。"
	schema["required"] = []string{"mode"}
}
func (g *generator) resolve(s object) object {
	if ref, ok := s["$ref"].(string); ok {
		if value, ok := g.schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(object); ok {
			return value
		}
	}
	return s
}
func (g *generator) annotate(op object, handler, method, path string) {
	if op["x-authentication"] == "public" {
		op["x-resource-scope"] = "公开服务状态或独立的身份接入流程；不赋予业务资源访问权。"
	}
	if op["x-authentication"] == "agent_signature" {
		op["x-resource-scope"] = "仅签名认证的实际 Agent 身份及其受理任务；API Key 不得冒充。"
	}
	switch path {
	case "/api/v1/api-key":
		op["x-authentication"] = "api_key"
		op["security"] = []any{object{"apiKey": []string{}}}
		op["x-resource-scope"] = "仅返回当前请求使用的 API Key 及创建者的有效授权。"
	case "/api/v1/settings/branding":
		if method == "put" {
			op["operationId"] = "updateSiteBranding_api_v1_settings_branding"
		}
	case "/api/v1/preferences/ui":
		op["summary"] = "查询界面偏好"
		if method == "patch" {
			op["summary"] = "更新界面偏好"
		}
		op["description"] = "当前认证账号的界面偏好；不接受账号参数。只保存当前值，无历史。PATCH 按页面及字段合并，须提供读取的 revision（首次为 0），冲突返回 409 后重新读取并合并当前修改。资源 ID 仅作选择提示，使用时仍须重新鉴权；实时配置仅允许 agent。请求上限 16384 字节。"
		if method == "patch" {
			op["requestBody"] = object{"required": true, "content": object{"application/json": object{"schema": object{"type": "object", "additionalProperties": false, "required": []string{"values", "revision"}, "properties": object{"revision": object{"type": "integer", "minimum": 0}, "values": store.UIPreferenceSchema()}}}}}
		}
	case "/api/v1/auth/session", "/api/v1/auth/logout":
		op["x-authentication"] = "session"
		op["security"] = []any{object{"session": []string{}}}
	case "/agent/v1/enroll":
		op["description"] = "首次注册使用单独的 enrollment_token 与 Ed25519 公钥；API Key 不能替代注册凭据。"
	case "/api/v1/agent-installer", "/api/v1/agent-binary":
		op["description"] = "安装或下载凭据使用独立的注册令牌/下载授权流程；普通 API Key 无权生成。"
	}
	if method == "get" {
		delete(op, "requestBody")
	}
	if handler == "updateConfig" || handler == "putAgentConfig" {
		op["requestBody"] = object{"required": true, "content": object{"application/json": object{"schema": g.named("core.Config")}}}
	}
	if body, ok := op["requestBody"].(object); ok {
		media := body["content"].(object)["application/json"].(object)
		schema := g.resolve(media["schema"].(object))
		copySchema := object{}
		for k, v := range schema {
			copySchema[k] = v
		}
		media["schema"] = copySchema
		if handler == "updateConfig" || handler == "putAgentConfig" {
			properties := object{}
			for key, value := range copySchema["properties"].(object) {
				properties[key] = value
			}
			properties["content"] = object{"type": []string{"string", "null"}, "description": "省略/null 保留已有配置；字符串替换，原位占位符保留敏感值。"}
			if handler == "putAgentConfig" {
				properties["version"] = object{"type": "integer", "minimum": 0, "description": "已有配置的当前并发标识；首次保存为 0，并须提供完整 content。"}
			}
			copySchema["properties"] = properties
		}
		var required []string
		switch handler {
		case "submitTLSMigration", "previewTLSFrontend":
			fields := copySchema["properties"].(object)
			fields["target_mode"] = object{"type": "string", "enum": []string{"nginx", "caddy"}}
			fields["expected_instance_id"] = object{"type": "string", "description": "必填，主实例为空字符串，共享实例为实际 shr_ ID。省略和 null 均拒绝。"}
			media["example"] = object{"expected_version": 1, "expected_instance_id": "", "target_mode": "caddy"}
			required = []string{"target_mode", "expected_version", "expected_instance_id"}
			if handler == "submitTLSMigration" {
				required = append(required, "plan_id", "task_id")
				fields["plan_id"] = object{"type": "string", "pattern": "^[0-9a-f]{64}$", "description": "预览返回的计划标识。"}
				fields["task_id"] = object{"type": "string", "pattern": "^tsk_[0-9a-f]{16}$", "description": "调用方预先保存的操作 ID，用于断线查询与重复提交核对。"}
				media["example"].(object)["plan_id"] = strings.Repeat("0", 64)
				media["example"].(object)["task_id"] = "tsk_0123456789abcdef"
			}
		case "saveServerInbound":
			fields := copySchema["properties"].(object)
			fields["operation"] = object{"type": "string", "enum": []string{"add", "modify", "delete"}}
			fields["intent"] = object{"type": "string", "enum": []string{"validate", "deploy"}, "description": "两种操作均保存配置、创建版本和任务。validate 校验候选，deploy 校验并尝试部署。"}
			fields["expected_version"] = object{"type": "integer", "minimum": 0, "description": "首次创建为 0。已有配置中新增、修改或删除均提交当前保存版本，过期返回 409。"}
			fields["expected_instance_id"] = object{"type": []string{"string", "null"}, "description": "空字符串校验主实例，shr_ ID 校验共享实例。旧接口省略或 null 时按授权隐式选择。显式 ID 不匹配返回 409。"}
			fields["original_tag"].(object)["description"] = "modify 和 delete 提供当前唯一的目标标识；add 省略或为空。"
			fields["fragment"].(object)["description"] = "仅 modify 允许。原生片段替换，要求 original_tag，不与预设 input 同时提交。"
			fields["input"].(object)["description"] = "add 或结构化 modify 使用完整预设输入；凭据必须通过协议校验。"
			media["example"] = object{"operation": "delete", "expected_instance_id": "", "expected_version": 1, "original_tag": "retired-listener", "intent": "validate"}
			required = []string{"operation", "intent", "expected_version"}
		case "putPageSize":
			copySchema["properties"].(object)["revision"] = object{"type": "integer", "minimum": 0}
			required = []string{"page_size", "revision"}
		case "createAPIKey":
			required = []string{"name", "permissions"}
		case "updateAPIKey":
			required = []string{"name", "permissions", "revision"}
		case "createConfig":
			required = []string{"name", "engine", "content"}
		case "updateConfig":
			required = []string{"name", "engine", "version"}
		case "putAgentConfig":
			required = []string{"version"}
		case "createTask":
			required = []string{"agent_id", "engine", "action"}
		case "createConfigExecutionTask":
			required = []string{"agent_id", "action", "expected_config_version", "expected_instance_id"}
		case "restoreConfigRevision":
			required = []string{"expected_version"}
		case "editRetryTask":
			required = []string{"content", "expected_version"}
		case "putSettings", "putNotifications":
			required = []string{"revision"}
		case "createUser":
			required = []string{"username", "password", "role"}
		case "createCertificate":
			required = []string{"name", "source"}
		case "testNotification":
			required = []string{"channel_id", "revision", "event"}
		case "previewNotification":
			required = []string{"event", "template"}
		case "importAgentConfig":
			required = []string{"snapshot_task_id", "name"}
		}
		if len(required) > 0 {
			copySchema["required"] = required
		}
	}
	switch handler {
	case "getManagedProxies":
		op["summary"] = "读取节点 Nginx/Caddy 状态"
		op["description"] = "读取节点已上报的运行版本、磁盘版本、官方候选、检查时间和影响范围。supported=false 表示需升级 QAgent。读取缓存不会联系上游。"
	case "checkManagedProxy":
		op["summary"] = "检查节点 Nginx/Caddy 官方版本"
		op["description"] = "component 可选 nginx、caddy。创建版本查询任务，仅需节点读取权限。API Key 创建任务须使用读写模式。检查不改变节点服务。"
	case "updateManagedProxy":
		op["summary"] = "更新节点托管 Nginx/Caddy"
		op["description"] = "component 可选 nginx、caddy。更新前读取托管监听和共享实例数量。切换可能短暂断连。通过 GET /tasks/{id} 跟踪任务，再读取节点代理状态核对运行版本。"
	case "getPageSize", "putPageSize":
		op["description"] = "当前认证账号的每页条数；page 为 nodes、traffic 或 tcp。缺失值为 30，允许 30、50、100、200；保存须带当前 revision，首次为 0，并发冲突返回 409。"
		g.resolve(g.named("store.PageSizePreference"))["properties"].(object)["revision"].(object)["minimum"] = 0
	case "putNotifications":
		op["description"] = "省略 secrets 字段保留已存凭据，显式空值清空。保留未读取凭据时，修改认证目标（SMTP 主机/端口/TLS/用户名或 Webhook 地址）须具备 secrets.read；也可显式替换相关凭据。"
	case "importAgentConfig":
		op["description"] = "仅节点所有者可导入本人已完成 read-config 任务的快照；expected_version 为当前配置并发标识（尚无配置为 0）。受理不会保存配置；查询任务实际完成后才新增配置与历史。后台执行保留原始 Key 并复核授权。"
	case "editRetryTask", "taskRetryEditor":
		op["description"] = "另须配置读写权限：节点配置 agent-config.read + agent-config.write；档案 configs.read + configs.write。实际配置由原任务确定，不能替换资源。编辑后重试按新的 Key 授权重新受理。"
	case "runSubStoreSync":
		op["description"] = "向已配置 Sub-Store 同步本人选中客户端接入数据，需要独立敏感读取授权。远程写入不具备数据库回滚能力；响应丢失先查询目标和同步结果。"
	case "updateUser":
		op["description"] = "Key 只能编辑非管理员且非创建者账户的显示名称、收缩权限或停用；保留权限不能超过 Key 当前范围。增加账户权限、重新启用及身份/凭据操作需要会话，以防通过替代账号扩大能力。账户创建、删除、清理也需要会话。"
	case "putAgentSharing", "putUserAgentAccess":
		op["description"] = "Key 转授权仅允许创建者所有的节点，不能借管理员角色转授他人节点；用户授权不能操作管理员或创建者。批量与旧授权逐项复核。"
	case "submitTLSMigration":
		op["summary"] = "迁移已预览的 TLS 公网端口组"
		op["description"] = "提供预览的 plan_id、自选 tsk_ 加 16 位十六进制关联标识，及相同的实例、版本和模式。202 表示已受理。端口交接可能中断长连接和新连接，按节点 tls-migrations/{task} 查询各路径的真实数据往返和恢复结果。仅整组通过才发布新版本。无变化返回 unchanged。"
	case "getTLSPortOccupancy":
		op["summary"] = "查询节点的可见 TLS 路径数与端口占用证据"
		op["description"] = "visible_routes 仅统计当前授权实例。partial 表示存在不可见占用或检查不完整。observed_free 要求 15 秒内完成且未被后续变更失效的节点监听观测。unconfirmed 表示缺少证据。"
	case "getTLSMigration":
		op["summary"] = "查询 TLS 迁移与逐路径恢复结果"
		op["description"] = "result.paths 每行含配置 ID、实例 ID、引擎、入站标识和路径，与 plan.members 同序。succeeded 表示该路径已核实数据往返及后端归属。unknown 表示证据不足。rolled_back 要求原路径全部通过；recovery_incomplete 需要核对 step 和各路径。"
	case "previewTLSFrontend":
		op["summary"] = "预览同一节点公网端口的 TLS 前端变更"
		op["description"] = "按唯一入站定位公网端口。只读返回可见成员的当前及目标模式、保存与部署版本、阻碍和待执行阶段核验项。无保存、签发、安装或服务变更。"
	case "saveServerInbound":
		op["description"] = "add 创建入站，modify 编辑现有入站，delete 删除入站。validate 和 deploy 均保存配置并受理异步任务。按 task.id 查询 /tasks/{id}，终态与已部署版本需分别核对。"
	case "saveManagedOutbound", "saveConfigField", "savePresetSource":
		op["description"] = "保存当前授权工作区配置，必要时同时受理异步任务；按返回配置和任务 ID 查询结果。snapshot 引用还要求 tasks.read，不能引用他人配置/证书。"
	}
	response := op["responses"].(object)
	// Both routes return arrays outside the generator's usual writeJSON inference.
	arrayItem := map[string]string{
		"/api/v1/core-logs":             "core.CoreLogEntry",
		"/api/v1/system-tcp/parameters": "core.TCPParameterRule",
	}[path]
	if method == "get" && arrayItem != "" {
		response["200"] = object{"description": op["summary"], "content": object{"application/json": object{
			"schema": object{"type": "array", "items": g.named(arrayItem)},
		}}}
	}
	switch handler {
	case "getAPIKeyAuthorization":
		response["200"] = object{
			"description": "当前 Key 的有效授权与可调用路由模板。具体资源仍需单独授权。",
			"content": object{"application/json": object{
				"schema": g.named("api.apiKeyAuthorization"),
				"example": object{
					"id": "key_0000000000000001", "name": "test-reader", "mode": "read_only", "prefix": "qch_key_demo",
					"permissions": []string{"agent-config.read"}, "ip_allowlist": []string{}, "expires_at": nil,
					"workspace_id": "usr_0000000000000001", "user_id": "usr_0000000000000001", "source_ip": "192.0.2.1",
					"endpoints": []string{"GET /api/v1/api-key", "GET /api/v1/openapi.json"},
				},
			}},
		}
	case "getAPIContract":
		response["200"] = object{"description": "此面板构建发布的完整 OpenAPI 文档。", "content": object{"application/json": object{"schema": object{"type": "object", "required": []string{"openapi", "info", "paths", "components"}}}}}
	case "previewTLSFrontend", "submitTLSMigration", "getTLSMigration", "getTLSPortOccupancy":
		annotateTLSResponses(response, handler)
	case "getManagedProxies":
		response["200"] = object{"description": "节点代理上报", "content": object{"application/json": object{"schema": g.named("api.nodeProxyReport")}}}
	case "checkManagedProxy", "updateManagedProxy":
		response["202"] = object{"description": "已受理或复用的任务", "content": object{"application/json": object{"schema": g.named("core.Task")}}}
	case "deleteConfig", "deleteTemplate", "cancelTask", "deleteAgent", "revokeAPIKey":
		response["204"] = object{"description": "操作成功，无响应体"}
		delete(response, "200")
	case "createTask", "createConfigExecutionTask":
		response["201"] = object{"description": "已受理新任务；按 id 查询直到终态", "content": object{"application/json": object{"schema": g.named("core.Task")}}}
		response["200"] = object{"description": "复用已有任务；核对任务状态与实际结果", "content": object{"application/json": object{"schema": g.named("core.Task")}}}
	case "getTask":
		response["200"] = object{"description": "真实任务状态及结果；view=status 省略完整内容", "content": object{"application/json": object{"schema": g.named("core.Task")}}}
	case "downloadManagementSkill", "getRegionFlag", "getIPQualityArchive", "certificatePublicChain":
		mediaType := map[string]string{
			"downloadManagementSkill": "application/zip",
			"getRegionFlag":           "image/svg+xml",
			"getIPQualityArchive":     "image/svg+xml",
			"certificatePublicChain":  "application/x-pem-file",
		}[handler]
		response["200"] = object{"description": op["summary"], "content": object{mediaType: object{"schema": object{"type": "string", "format": "binary"}}}}
	}
}

// Examples correspond to a single synthetic plan. Each stage shows the
// actual response shape, including the absence of a receipt before completion.
func annotateTLSResponses(responses object, handler string) {
	member := object{
		"config_id": "cfg_0000000000000001", "instance_id": "", "engine": "xray", "tag": "site", "version": 1,
		"deployed_version": 1, "domain": "edge.example.test", "path": "/stream", "transport": "xhttp", "backend_port": 23456,
		"from": "nginx", "to": "caddy", "certificate_id": "crt_0000000000000001", "certificate_version": 1,
	}
	plan := object{
		"agent_id": "agt_0000000000000001", "public_port": 443, "target_mode": "caddy", "plan_id": strings.Repeat("a", 64),
		"status": "runtime_evidence_required", "members": []any{member}, "blockers": []string{}, "connection_interruption_possible": true,
		"runtime_checks": []string{"agent_listener_ownership_ipv4_ipv6", "proxy_config_and_service", "original_client_round_trip", "restore_original_paths"},
	}
	taskID := "tsk_0123456789abcdef"
	set := func(status, description string, example object) {
		r := responses[status].(object)
		r["description"] = description
		r["content"].(object)["application/json"].(object)["example"] = example
	}
	row := object{"config_id": member["config_id"], "instance_id": member["instance_id"], "engine": member["engine"], "tag": member["tag"], "path": member["path"]}
	switch handler {
	case "previewTLSFrontend":
		set("200", "预览包含保存版本、部署版本、阻碍及待 Agent 核实项。", plan)
	case "submitTLSMigration":
		set("202", "已受理。根据 Location 查询原操作。", object{"task_id": taskID, "state": "pending", "plan": plan})
		responses["202"].(object)["headers"] = object{"Location": object{"required": true, "schema": object{"type": "string", "format": "uri-reference"}, "description": "同源迁移状态地址，查询需要配置读取与任务读取权限。"}}
		row["candidate"], row["recovery"] = "succeeded", "unknown"
		receipt := object{"state": "succeeded", "step": "complete", "paths": []any{row}}
		set("200", "目标已生效，或复用已有终态操作。", object{"state": "succeeded", "task_id": taskID, "plan": plan, "result": receipt})
	case "getTLSMigration":
		row["candidate"], row["recovery"] = "failed", "succeeded"
		set("200", "原操作在当前授权范围内的状态和逐路径验证结果。", object{"task_id": taskID, "state": "rolled_back", "plan": plan, "result": object{"state": "rolled_back", "step": "restore", "code": "business_validation_failed", "paths": []any{row}}})
	case "getTLSPortOccupancy":
		set("200", "可见路径数量及端口观测结论。", object{"agent_id": "agt_0000000000000001", "public_port": 443, "visible_routes": 0, "state": "partial"})
	}
}
