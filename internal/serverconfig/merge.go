package serverconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// MergeGenerated replaces the primary generated inbound while retaining
// additional inbounds and advanced root options already present in the node
// configuration. If the current document has no recognizable generated
// inbound, the new inbound is appended.
func MergeGenerated(engine core.Engine, currentContent, generatedContent string) (string, error) {
	matchValue := ""
	if current, ok := Parse(engine, currentContent); ok {
		matchValue = current.Tag
	}
	if engine == core.EngineSingBox && strings.Contains(generatedContent, `"endpoints"`) {
		// Parse historically selected only inbounds. For endpoint upserts, use
		// the generated identity when that endpoint already exists so a save
		// replaces it instead of appending a duplicate.
		var generated map[string]any
		if json.Unmarshal([]byte(generatedContent), &generated) == nil {
			if items, ok := generated["endpoints"].([]any); ok && len(items) == 1 {
				tag := stringValue(mapValue(items[0])["tag"])
				if entries, err := DiscoverSingBoxEntries(currentContent); err == nil {
					for _, entry := range entries {
						if entry.Section == "endpoints" && entry.Tag == tag {
							matchValue = tag
						}
					}
				}
			}
		}
	}
	return mutateGenerated(engine, currentContent, generatedContent, matchValue, "upsert")
}

// MutateGenerated applies an explicit add, modify, or delete operation to the
// managed inbound. Modify and delete require originalTag to exist; add rejects
// a generated tag that already exists.
func MutateGenerated(engine core.Engine, currentContent, generatedContent, originalTag, operation string) (string, error) {
	if operation != "add" && operation != "modify" && operation != "delete" {
		return "", fmt.Errorf("不支持的入站操作 %q", operation)
	}
	if operation != "add" && originalTag == "" {
		return "", fmt.Errorf("%s 操作需要现有入站标识", operation)
	}
	return mutateGenerated(engine, currentContent, generatedContent, originalTag, operation)
}

// Deletion is keyed only by the saved identity. It must not require generating
// fresh credentials or validating obsolete connection parameters.
func DeletePresetInbound(engine core.Engine, content, tag string) (string, error) {
	if tag == "" {
		return "", fmt.Errorf("删除操作需要现有入站标识")
	}
	if engine == core.EngineShadowsocksRust {
		return MutateSSRustPort(content, "{}", tag, "delete")
	}
	listKey, matchKey := "inbounds", "tag"
	if engine == core.EngineMihomo {
		listKey, matchKey = "listeners", "name"
	}
	// JSON is also valid YAML; only identity is consumed for deletion.
	generated, err := json.Marshal(map[string]any{listKey: []any{map[string]any{matchKey: tag}}})
	if err != nil {
		return "", err
	}
	return MutateGenerated(engine, content, string(generated), tag, "delete")
}

func mutateGenerated(engine core.Engine, currentContent, generatedContent, matchValue, operation string) (string, error) {
	if engine == core.EngineShadowsocksRust {
		return mutateShadowsocksRust(currentContent, generatedContent, matchValue, operation)
	}
	if engine == core.EngineXray && matchValue != "" {
		var root map[string]any
		if json.Unmarshal([]byte(currentContent), &root) == nil {
			if entries, ok := root["inbounds"].([]any); ok {
				for _, raw := range entries {
					entry := mapValue(raw)
					if stringValue(entry["tag"]) == matchValue && stringValue(entry["protocol"]) == "wireguard" && !managedXrayWireGuardInbound(entry) {
						return "", fmt.Errorf("Xray WireGuard 入站 %q 包含自定义字段或多客户端配置，请使用完整源码编辑，避免覆盖", matchValue)
					}
				}
			}
		}
	}
	// Endpoints are a native sing-box root list, separate from inbounds. The
	// generic list mutator cannot enforce identity collisions across both lists,
	// so validate the candidate before replacing/appending it and mutate only
	// the endpoint list. Unknown fields on an existing endpoint are retained by
	// configschema's merge semantics; protocol-specific callers decide whether
	// an entry is safe to edit.
	if engine == core.EngineSingBox && strings.Contains(generatedContent, `"endpoints"`) {
		if err := validateSingBoxEntryMutation(currentContent, generatedContent, matchValue, operation); err != nil {
			return "", fmt.Errorf("sing-box endpoint identity check failed: %w", err)
		}
		endpointKeys := []string{
			"type", "tag", "system", "name", "mtu", "address", "private_key", "peers",
			"state_directory", "auth_key", "control_url", "ephemeral", "hostname", "accept_routes",
			"exit_node", "exit_node_allow_lan_access", "advertise_routes", "advertise_exit_node",
			"advertise_tags", "listen_port", "relay_server_port", "relay_server_static_endpoints",
			"system_interface", "system_interface_name", "system_interface_mtu", "udp_timeout", "ssh_server",
			"taildrop_directory", "workers", "on_demand", "detour", "bind_interface", "bind_address",
			"mode", "network", "address", "peer_address", "peer_address_ipv6", "topology", "duplicate_cn", "users", "static_key", "static_key_path", "key_direction", "tls", "cipher", "data_ciphers", "auth", "push", "max_clients",
			"routing_mark", "reuse_addr", "connect_timeout", "tcp_fast_open", "tcp_multi_path", "udp_fragment",
		}
		merged, err := configschema.MutateListItem(engine, currentContent, generatedContent, "endpoints", "tag", matchValue, operation, endpointKeys...)
		if err != nil {
			return "", fmt.Errorf("合并 sing-box endpoint 失败：%w", err)
		}
		if operation != "delete" {
			merged, err = enforceCentralLogging(engine, merged)
			if err != nil {
				return "", fmt.Errorf("启用集中内核日志失败：%w", err)
			}
		}
		return merged, nil
	}
	listKey, matchKey := "inbounds", "tag"
	managedKeys := []string{
		"tag", "listen", "port", "protocol", "settings", "streamSettings",
	}
	if engine == core.EngineMihomo {
		listKey, matchKey = "listeners", "name"
		managedKeys = []string{
			"name", "listen", "port", "type", "cipher", "password", "udp", "users",
			"up", "down", "alpn", "congestion-controller", "max-idle-time", "authentication-timeout",
			"certificate", "private-key", "reality-config", "decryption", "ws-path", "grpc-service-name", "xhttp-config", "network", "target",
			"routing-mark", "rule", "proxy", "psk", "version", "obfs-opts", "shadow-tls",
			"key", "aead-method", "padding-min", "padding-max", "table-type", "custom-table", "custom-tables",
			"handshake-timeout", "enable-pure-downlink", "httpmask", "disable-http-mask", "http-mask-mode", "path-root", "fallback", "mux-option",
		}
	} else if engine == core.EngineSingBox {
		managedKeys = []string{
			"tag", "listen", "listen_port", "type", "method", "password", "users", "up_mbps", "down_mbps",
			"congestion_control", "auth_timeout", "heartbeat", "tls", "transport", "network", "override_address", "override_port",
		}
	}
	var (
		merged string
		err    error
	)
	if operation == "upsert" {
		merged, err = configschema.MergeListItem(engine, currentContent, generatedContent, listKey, matchKey, matchValue, managedKeys...)
	} else {
		merged, err = configschema.MutateListItem(engine, currentContent, generatedContent, listKey, matchKey, matchValue, operation, managedKeys...)
	}
	if err != nil {
		return "", fmt.Errorf("合并服务端入站失败：%w", err)
	}
	if operation != "delete" {
		merged, err = enforceCentralLogging(engine, merged)
		if err != nil {
			return "", fmt.Errorf("启用集中内核日志失败：%w", err)
		}
	}
	return merged, nil
}

func enforceCentralLogging(engine core.Engine, content string) (string, error) {
	switch engine {
	case core.EngineMihomo:
		return configschema.MergeFragment(engine, content, "log-level", "info", false)
	case core.EngineXray:
		return configschema.MergeFragment(engine, content, "log", `{"loglevel":"info"}`, false)
	case core.EngineSingBox:
		return configschema.MergeFragment(engine, content, "log", `{"level":"info"}`, false)
	default:
		return content, nil
	}
}

func mutateShadowsocksRust(currentContent, generatedContent, matchValue, operation string) (string, error) {
	var generated map[string]any
	if err := json.Unmarshal([]byte(generatedContent), &generated); err != nil || generated == nil {
		return "", fmt.Errorf("ss-rust 生成配置必须是 JSON 对象")
	}
	if _, ok := parseShadowsocksRust(generatedContent); !ok {
		return "", fmt.Errorf("ss-rust 生成配置缺少完整服务端字段")
	}
	merged := currentContent
	if strings.TrimSpace(merged) == "" {
		merged = "{}"
	}
	var currentRoot map[string]any
	if err := json.Unmarshal([]byte(merged), &currentRoot); err != nil || currentRoot == nil {
		return "", fmt.Errorf("ss-rust 当前配置必须是 JSON 对象")
	}
	if listKey, entries, extended := shadowsocksRustExtendedEntries(currentRoot); extended {
		return mutateShadowsocksRustExtended(currentRoot, listKey, entries, generated, matchValue, operation)
	}
	current, currentOK := parseShadowsocksRust(merged)
	if operation == "add" || (operation == "upsert" && !currentOK) {
		if !currentOK {
			if _, hasServer := currentRoot["server_port"]; hasServer {
				return "", fmt.Errorf("ss-rust 现有服务端无法由预设解析，请使用源码编辑，避免覆盖自定义配置")
			}
		}
		entries := []any{}
		if currentOK {
			entry := shadowsocksRustGeneratedEntry(currentRoot)
			entry["id"] = current.Tag
			entries = append(entries, entry)
		}
		for _, key := range []string{"id", "server", "server_port", "password", "method", "plugin", "plugin_opts", "plugin_args", "plugin_mode"} {
			delete(currentRoot, key)
		}
		return mutateShadowsocksRustExtended(currentRoot, "servers", entries, generated, matchValue, operation)
	}
	switch operation {
	case "modify", "delete":
		if !currentOK {
			return "", fmt.Errorf("ss-rust %s 操作需要现有服务端配置", operation)
		}
		if matchValue != "" && matchValue != current.Tag {
			return "", fmt.Errorf("ss-rust 服务端入站 %q 不存在", matchValue)
		}
	}
	if operation == "delete" {
		for key := range generated {
			var err error
			merged, err = configschema.MergeFragment(core.EngineShadowsocksRust, merged, key, "", true)
			if err != nil {
				return "", fmt.Errorf("删除 ss-rust 配置字段 %s：%w", key, err)
			}
		}
		return merged, nil
	}
	for _, key := range []string{"dns", "outbound_bind_addr"} {
		if _, present := generated[key]; !present {
			if key == "dns" {
				if _, custom := currentRoot[key].(map[string]any); custom {
					continue
				}
			}
			var err error
			merged, err = configschema.MergeFragment(core.EngineShadowsocksRust, merged, key, "", true)
			if err != nil {
				return "", err
			}
		}
	}
	for key, value := range generated {
		fragment, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		merged, err = configschema.MergeFragment(core.EngineShadowsocksRust, merged, key, string(fragment), false)
		if err != nil {
			return "", fmt.Errorf("合并 ss-rust 配置字段 %s：%w", key, err)
		}
	}
	return merged, nil
}

func mutateShadowsocksRustExtended(root map[string]any, listKey string, entries []any, generated map[string]any, matchValue, operation string) (string, error) {
	for index, value := range entries {
		entry := mapValue(value)
		if entry == nil {
			return "", fmt.Errorf("ss-rust 扩展服务端入站必须是 JSON 对象")
		}
		entry["id"] = shadowsocksRustEntryTag(entry, index)
	}
	generatedEntry := shadowsocksRustGeneratedEntry(generated)
	// The preset does not edit these fields: retain existing per-port values
	// and let new ports inherit the global settings.
	delete(generatedEntry, "mode")
	delete(generatedEntry, "timeout")
	// Official ssserver only consumes DNS at the root. Keep script-written
	// per-server DNS as an unknown field instead of presenting it as effective.
	delete(generatedEntry, "dns")
	if acl, ok := root["acl"]; ok {
		generatedEntry["acl"] = acl
	}
	targetIndex := -1
	if matchValue != "" {
		for index, value := range entries {
			ssRustEntry := mapValue(value)
			if ssRustEntry != nil && shadowsocksRustEntryTag(ssRustEntry, index) == matchValue {
				targetIndex = index
				break
			}
		}
	}
	switch operation {
	case "add":
		if matchValue != "" {
			return "", fmt.Errorf("ss-rust 扩展配置新增操作不应携带现有入站标识")
		}
		entries = append(entries, generatedEntry)
	case "modify":
		if targetIndex < 0 {
			return "", fmt.Errorf("ss-rust 服务端入站 %q 不存在", matchValue)
		}
		currentEntry := mapValue(entries[targetIndex])
		if currentEntry == nil {
			return "", fmt.Errorf("ss-rust 扩展服务端入站必须是 JSON 对象")
		}
		delete(generatedEntry, "acl")
		entries[targetIndex] = mergeShadowsocksRustEntry(currentEntry, generatedEntry)
	case "delete":
		if targetIndex < 0 {
			return "", fmt.Errorf("ss-rust 服务端入站 %q 不存在", matchValue)
		}
		entries = append(entries[:targetIndex], entries[targetIndex+1:]...)
	case "upsert":
		if targetIndex < 0 {
			entries = append(entries, generatedEntry)
		} else {
			currentEntry := mapValue(entries[targetIndex])
			if currentEntry == nil {
				return "", fmt.Errorf("ss-rust 扩展服务端入站必须是 JSON 对象")
			}
			delete(generatedEntry, "acl")
			entries[targetIndex] = mergeShadowsocksRustEntry(currentEntry, generatedEntry)
		}
	default:
		return "", fmt.Errorf("不支持的 ss-rust 入站操作 %q", operation)
	}
	ports := make(map[int]bool)
	tags := make(map[string]bool)
	for index, value := range entries {
		tag := shadowsocksRustEntryTag(mapValue(value), index)
		if tags[tag] {
			return "", fmt.Errorf("ss-rust 入站标签 %q 已存在", tag)
		}
		tags[tag] = true
		port := intValue(mapValue(value)["server_port"])
		if port != 0 && ports[port] {
			return "", fmt.Errorf("ss-rust 端口 %d 已存在", port)
		}
		ports[port] = true
	}
	root[listKey] = entries
	if operation != "delete" {
		for _, key := range []string{"timeout", "mode", "fast_open", "no_delay"} {
			if _, present := root[key]; !present {
				root[key] = generated[key]
			}
		}
		root["ipv6_first"] = generated["ipv6_first"]
		if dns, present := generated["dns"]; present {
			root["dns"] = dns
		} else if _, custom := root["dns"].(map[string]any); !custom {
			delete(root, "dns")
		}
	}
	formatted, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(formatted) + "\n", nil
}

func shadowsocksRustGeneratedEntry(generated map[string]any) map[string]any {
	entry := make(map[string]any)
	for _, key := range []string{"id", "server", "server_port", "password", "method", "dns", "outbound_bind_addr", "mode", "timeout", "acl", "plugin", "plugin_opts", "plugin_args", "plugin_mode"} {
		if value, ok := generated[key]; ok {
			entry[key] = value
		}
	}
	return entry
}

func mergeShadowsocksRustEntry(current, generated map[string]any) map[string]any {
	delete(current, "outbound_bind_addr")
	for key, value := range generated {
		current[key] = value
	}
	return current
}

func shadowsocksRustEntryTag(entry map[string]any, index int) string {
	if value := strings.TrimSpace(stringValue(entry["id"])); value != "" {
		return value
	}
	for _, key := range []string{"remarks", "name"} {
		if value := strings.TrimSpace(stringValue(entry[key])); value != "" {
			return value
		}
	}
	return "ss-rust-" + fmt.Sprint(index+1)
}
