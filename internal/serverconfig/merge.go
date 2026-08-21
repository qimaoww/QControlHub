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

func mutateGenerated(engine core.Engine, currentContent, generatedContent, matchValue, operation string) (string, error) {
	if engine == core.EngineShadowsocksRust {
		return mutateShadowsocksRust(currentContent, generatedContent, matchValue, operation)
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
			"certificate", "private-key", "reality-config", "ws-path", "grpc-service-name",
		}
	} else if engine == core.EngineSingBox {
		managedKeys = []string{
			"tag", "listen", "listen_port", "type", "method", "password", "users", "up_mbps", "down_mbps",
			"congestion_control", "auth_timeout", "heartbeat", "tls", "transport",
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
	switch operation {
	case "add":
		if currentOK {
			return "", fmt.Errorf("ss-rust 配置只支持单个服务端入站")
		}
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
	generatedEntry := shadowsocksRustGeneratedEntry(generated)
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
			entries[targetIndex] = mergeShadowsocksRustEntry(currentEntry, generatedEntry)
		}
	default:
		return "", fmt.Errorf("不支持的 ss-rust 入站操作 %q", operation)
	}
	root[listKey] = entries
	if operation != "delete" {
		if noDelay, ok := generated["no_delay"]; ok {
			root["no_delay"] = noDelay
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
	for _, key := range []string{"server", "server_port", "password", "method", "mode", "timeout", "acl", "plugin", "plugin_opts", "plugin_args", "plugin_mode"} {
		if value, ok := generated[key]; ok {
			entry[key] = value
		}
	}
	return entry
}

func mergeShadowsocksRustEntry(current, generated map[string]any) map[string]any {
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
