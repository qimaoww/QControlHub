package apidoc

import "strings"

// These annotations describe validation and protocol semantics that cannot be
// inferred from a Go field type. Route existence and Key access remain derived
// exclusively from registrations and the capability registry.
func (g *generator) annotateField(name string, field object) {
	switch name {
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
		}
		if len(required) > 0 {
			copySchema["required"] = required
		}
	}
	switch handler {
	case "editRetryTask", "taskRetryEditor":
		op["description"] = "另须配置读写权限：节点配置 agent-config.read + agent-config.write；档案 configs.read + configs.write。实际配置由原任务确定，不能替换资源。编辑后重试按新的 Key 授权重新受理。"
	case "runSubStoreSync":
		op["description"] = "向已配置 Sub-Store 同步本人选中客户端接入数据，需要独立敏感读取授权。远程写入不具备数据库回滚能力；响应丢失先查询目标和同步结果。"
	case "updateUser":
		op["description"] = "Key 只能编辑非管理员且非创建者账户的显示名称、收缩权限或停用；保留权限不能超过 Key 当前范围。增加账户权限、重新启用及身份/凭据操作需要会话，以防通过替代账号扩大能力。账户创建、删除、清理也需要会话。"
	case "putAgentSharing", "putUserAgentAccess":
		op["description"] = "Key 转授权仅允许创建者所有的节点，不能借管理员角色转授他人节点；用户授权不能操作管理员或创建者。批量与旧授权逐项复核。"
	case "saveServerInbound", "saveManagedOutbound", "saveConfigField", "savePresetSource":
		op["description"] = "保存当前授权工作区配置，必要时同时受理异步任务；按返回配置和任务 ID 查询结果。snapshot 引用还要求 tasks.read，不能引用他人配置/证书。"
	}
	response := op["responses"].(object)
	switch handler {
	case "deleteConfig", "deleteTemplate", "cancelTask", "deleteAgent", "revokeAPIKey":
		response["204"] = object{"description": "操作成功，无响应体"}
		delete(response, "200")
	case "createTask", "createConfigExecutionTask":
		response["201"] = object{"description": "已受理新任务；按 id 查询直到终态", "content": object{"application/json": object{"schema": g.named("core.Task")}}}
		response["200"] = object{"description": "复用已有任务；核对任务状态与实际结果", "content": object{"application/json": object{"schema": g.named("core.Task")}}}
	case "getTask":
		response["200"] = object{"description": "真实任务状态及结果；view=status 省略完整内容", "content": object{"application/json": object{"schema": g.named("core.Task")}}}
	case "downloadManagementSkill":
		response["200"] = object{"description": "可安装的 qcontrolhub-manager Skill ZIP", "content": object{"application/zip": object{"schema": object{"type": "string", "format": "binary"}}}}
	}
}
