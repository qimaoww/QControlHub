package serverconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// MutateSSRustPort is the scope-separated preset path. Older API clients can
// still explicitly edit DNS/IPv6 through MutateGenerated; this path never
// resets any existing global value (or its absence) while editing a port.
func MutateSSRustPort(current, generated, tag, operation string) (string, error) {
	if operation != "add" && operation != "modify" && operation != "delete" {
		return "", errors.New("不支持的端口操作")
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal([]byte(current), &original); err != nil || original == nil {
		return "", errors.New("SS Rust 配置必须是 JSON 对象")
	}
	if original["server_port"] != nil {
		root, entry, commit, err := ssRustFieldTarget(current, "ss-rust", "server")
		if err != nil {
			return "", err
		}
		if err := commit(entry); err != nil {
			return "", err
		}
		encoded, _ := json.Marshal(root)
		current = string(encoded)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(current), &root); err != nil {
		return "", err
	}
	listKey := "servers"
	if root["shadowsocks"] != nil {
		listKey = "shadowsocks"
	}
	if root["server"] != nil || (root["servers"] != nil && root["shadowsocks"] != nil) {
		return "", errors.New("混合服务端格式请使用完整源码编辑")
	}
	entries := []map[string]json.RawMessage{}
	if value, exists := root[listKey]; exists {
		if err := json.Unmarshal(value, &entries); err != nil || entries == nil {
			return "", errors.New("SS Rust 服务端列表必须是对象数组")
		}
	}
	var incoming map[string]json.RawMessage
	if err := json.Unmarshal([]byte(generated), &incoming); err != nil {
		return "", err
	}
	if _, ok := parseShadowsocksRust(generated); !ok {
		return "", errors.New("生成配置缺少完整服务端字段")
	}
	if operation == "add" {
		if tag != "" {
			return "", errors.New("新增端口不能携带现有端口标识")
		}
		entry := make(map[string]json.RawMessage)
		for _, key := range []string{"server", "server_port", "method", "password", "outbound_bind_addr"} {
			if value, exists := incoming[key]; exists {
				entry[key] = value
			}
		}
		// The managed unit's --acl supersedes root.acl. Explicitly carry the
		// imported ACL onto new ports so they retain the imported policy too.
		if acl, present := root["acl"]; present {
			entry["acl"] = acl
		}
		entries = append(entries, entry)
	} else {
		_, selected, _, err := ssRustFieldTarget(current, tag, "server")
		if err != nil {
			return "", err
		}
		index := -1
		for i, entry := range entries {
			if string(entry["server_port"]) == string(selected["server_port"]) && ssRustRawEntryTag(entry, i) == tag {
				index = i
				break
			}
		}
		if index < 0 {
			return "", errors.New("端口标识不存在")
		}
		if operation == "delete" {
			entries = append(entries[:index], entries[index+1:]...)
		} else {
			for _, key := range []string{"server", "server_port", "method", "password", "outbound_bind_addr"} {
				delete(entries[index], key)
				if value, exists := incoming[key]; exists {
					entries[index][key] = value
				}
			}
		}
	}
	ports := make(map[int]bool)
	for _, entry := range entries {
		if entry == nil {
			return "", errors.New("SS Rust 服务端条目必须是对象")
		}
		var port int
		if err := json.Unmarshal(entry["server_port"], &port); err == nil && port != 0 {
			if ports[port] {
				return "", fmt.Errorf("SS Rust 端口 %d 已存在", port)
			}
			ports[port] = true
		}
	}
	root[listKey], _ = json.Marshal(entries)
	encoded, err := json.MarshalIndent(root, "", "  ")
	return string(encoded) + "\n", err
}

// SSRustInboundField returns only the selected port's explicit value. Global
// defaults are returned separately so removing an override is not confused
// with disabling the option.
func SSRustInboundField(content, tag, key string) (fragment string, present bool, inherited string, err error) {
	root, entry, _, err := ssRustFieldTarget(content, tag, key)
	if err != nil {
		return "", false, "", err
	}
	encoded, _ := json.Marshal(entry)
	fragment, present, err = configschema.Fragment(core.EngineShadowsocksRust, string(encoded), key)
	if ssRustFieldScope(key) == "override" {
		encoded, _ = json.Marshal(root)
		inherited, _, _ = configschema.Fragment(core.EngineShadowsocksRust, string(encoded), key)
	}
	return
}

// MergeSSRustInboundField preserves every other port and all root defaults.
// Legacy single-server documents are normalized to the extended format before
// a port override is edited; this avoids silently editing a global default.
func MergeSSRustInboundField(content, tag, key, fragment string, remove bool) (string, error) {
	if err := ValidateSSRustFieldValue(key, fragment, remove); err != nil {
		return "", err
	}
	if remove && (key == "server" || key == "server_port" || key == "method" || key == "password") {
		return "", errors.New("不能删除端口必需字段；请使用删除入站操作")
	}
	root, entry, commit, err := ssRustFieldTarget(content, tag, key)
	if err != nil {
		return "", err
	}
	encoded, _ := json.Marshal(entry)
	merged, err := configschema.MergeFragment(core.EngineShadowsocksRust, string(encoded), key, fragment, remove)
	if err != nil {
		return "", err
	}
	var updated map[string]json.RawMessage
	if err := json.Unmarshal([]byte(merged), &updated); err != nil {
		return "", err
	}
	if err := commit(updated); err != nil {
		return "", err
	}
	if key == "server_port" && !remove {
		for _, listKey := range []string{"servers", "shadowsocks"} {
			var entries []map[string]json.RawMessage
			if err := json.Unmarshal(root[listKey], &entries); err != nil {
				continue
			}
			ports := make(map[int]bool)
			for _, item := range entries {
				var port int
				if err := json.Unmarshal(item["server_port"], &port); err != nil {
					continue
				}
				if ports[port] {
					return "", fmt.Errorf("SS Rust 端口 %d 已存在", port)
				}
				ports[port] = true
			}
		}
	}
	encoded, err = json.MarshalIndent(root, "", "  ")
	return string(encoded) + "\n", err
}

// The field editor has no native ssserver check mode to lean on. Reject
// obvious invalid typed values before saving/restarting a running service.
func ValidateSSRustFieldValue(key, fragment string, remove bool) error {
	if remove {
		return nil
	}
	invalid := func() error { return fmt.Errorf("SS Rust 字段 %s 的类型或值无效", key) }
	if !json.Valid([]byte(fragment)) || strings.TrimSpace(fragment) == "null" {
		return invalid()
	}
	var text string
	switch key {
	case "server", "password", "method", "mode", "plugin", "plugin_opts", "plugin_mode", "outbound_bind_addr", "outbound_bind_interface", "acl":
		if err := json.Unmarshal([]byte(fragment), &text); err != nil || strings.ContainsAny(text, "\x00\r\n") {
			return invalid()
		}
		if text == "" && key != "plugin" && key != "plugin_opts" {
			return invalid()
		}
		if (key == "mode" || key == "plugin_mode") && text != "tcp_only" && text != "udp_only" && text != "tcp_and_udp" {
			return invalid()
		}
		if key == "outbound_bind_addr" && net.ParseIP(text) == nil {
			return invalid()
		}
		if key == "acl" && !filepath.IsAbs(text) {
			return invalid()
		}
	case "server_port", "timeout", "udp_timeout", "keep_alive", "outbound_fwmark":
		var value uint64
		if err := json.Unmarshal([]byte(fragment), &value); err != nil {
			return invalid()
		}
		if key == "server_port" && (value == 0 || value > 65535) {
			return invalid()
		}
		if key == "outbound_fwmark" && value > 0xffffffff {
			return invalid()
		}
	case "fast_open", "no_delay", "ipv6_first", "outbound_udp_allow_fragmentation", "inbound_udp_allow_fragmentation":
		var value bool
		if err := json.Unmarshal([]byte(fragment), &value); err != nil {
			return invalid()
		}
	case "plugin_args":
		var value []string
		if err := json.Unmarshal([]byte(fragment), &value); err != nil {
			return invalid()
		}
	case "dns":
		if json.Unmarshal([]byte(fragment), &text) != nil {
			var object map[string]json.RawMessage
			if json.Unmarshal([]byte(fragment), &object) != nil || object == nil {
				return invalid()
			}
		}
	case "security":
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(fragment), &object) != nil || object == nil {
			return invalid()
		}
	}
	return nil
}

func ssRustFieldScope(key string) string {
	catalog, _ := configschema.CatalogFor(core.EngineShadowsocksRust)
	for _, field := range catalog.Fields {
		if field.Key == key {
			return field.Scope
		}
	}
	return ""
}

func ssRustFieldTarget(content, tag, key string) (map[string]json.RawMessage, map[string]json.RawMessage, func(map[string]json.RawMessage) error, error) {
	fail := func(err error) (map[string]json.RawMessage, map[string]json.RawMessage, func(map[string]json.RawMessage) error, error) {
		return nil, nil, nil, err
	}
	if scope := ssRustFieldScope(key); scope != "inbound" && scope != "override" {
		return fail(fmt.Errorf("%s 不支持端口级设置", key))
	}
	if strings.TrimSpace(tag) == "" {
		return fail(errors.New("必须选择现有端口"))
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil || root == nil {
		return fail(errors.New("SS Rust 配置必须是 JSON 对象"))
	}
	listKey := ""
	for _, candidate := range []string{"servers", "shadowsocks"} {
		if _, exists := root[candidate]; exists {
			if listKey != "" || root["server_port"] != nil || root["server"] != nil {
				return fail(errors.New("混合服务端格式请使用完整源码编辑"))
			}
			listKey = candidate
		}
	}
	if listKey == "" {
		if _, ok := parseShadowsocksRust(content); !ok || tag != "ss-rust" {
			return fail(fmt.Errorf("SS Rust 端口 %q 不存在", tag))
		}
		entry := make(map[string]json.RawMessage)
		for name, value := range root {
			if ssRustFieldScope(name) == "inbound" {
				entry[name] = value
			}
		}
		// Keep the original UI identity when switching to servers[].
		entry["id"] = json.RawMessage(`"ss-rust"`)
		return root, entry, func(updated map[string]json.RawMessage) error {
			for name := range root {
				if ssRustFieldScope(name) == "inbound" {
					delete(root, name)
				}
			}
			value, err := json.Marshal([]any{updated})
			root["servers"] = value
			return err
		}, nil
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(root[listKey], &entries); err != nil || entries == nil {
		return fail(errors.New("SS Rust 服务端列表必须是对象数组"))
	}
	target := -1
	for index, entry := range entries {
		if entry == nil {
			return fail(errors.New("SS Rust 服务端条目必须是对象"))
		}
		if ssRustRawEntryTag(entry, index) == tag {
			if target >= 0 {
				return fail(errors.New("端口标识重复，请先通过完整源码设置唯一 id"))
			}
			target = index
		}
	}
	if target < 0 {
		return fail(fmt.Errorf("SS Rust 端口 %q 不存在", tag))
	}
	return root, entries[target], func(updated map[string]json.RawMessage) error {
		entries[target] = updated
		value, err := json.Marshal(entries)
		root[listKey] = value
		return err
	}, nil
}

func ssRustRawEntryTag(entry map[string]json.RawMessage, index int) string {
	identity := make(map[string]any)
	for _, name := range []string{"id", "remarks", "name"} {
		var value string
		_ = json.Unmarshal(entry[name], &value)
		identity[name] = value
	}
	return shadowsocksRustEntryTag(identity, index)
}
