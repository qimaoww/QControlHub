package serverconfig

import (
	"encoding/json"
	"errors"
	"net"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func generateShadowsocksRust(input Input) (string, error) {
	input.SSRustDNS = strings.TrimSpace(input.SSRustDNS)
	input.SSRustOutboundBindAddr = strings.TrimSpace(input.SSRustOutboundBindAddr)
	if len(input.SSRustDNS) > 1024 || strings.ContainsAny(input.SSRustDNS, "\r\n\x00") {
		return "", errors.New("全局 DNS 不能超过 1024 字符或包含换行和空字符")
	}
	if input.SSRustOutboundBindAddr != "" && net.ParseIP(input.SSRustOutboundBindAddr) == nil {
		return "", errors.New("出站绑定地址必须是 IP 地址")
	}
	root := map[string]any{
		"id":          input.Tag,
		"server":      input.Listen,
		"server_port": input.Port,
		"password":    input.Credential,
		"method":      input.Method,
		"mode":        "tcp_and_udp",
		"timeout":     300,
		"no_delay":    true,
		"fast_open":   false,
		"ipv6_first":  input.SSRustIPv6First,
	}
	if input.SSRustDNS != "" {
		root["dns"] = input.SSRustDNS
	}
	if input.SSRustOutboundBindAddr != "" {
		root["outbound_bind_addr"] = input.SSRustOutboundBindAddr
	}
	value, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(value) + "\n", nil
}

func parseShadowsocksRust(content string) (Input, bool) {
	var root map[string]any
	if json.Unmarshal([]byte(content), &root) != nil || root == nil {
		return Input{}, false
	}
	if input, ok := parseShadowsocksRustEntry(root, shadowsocksRustSingleTag(root)); ok {
		return input, true
	}
	_, entries, ok := shadowsocksRustExtendedEntries(root)
	if !ok {
		return Input{}, false
	}
	for index, value := range entries {
		entry := mapValue(value)
		if input, ok := parseShadowsocksRustEntry(entry, shadowsocksRustEntryTag(entry, index)); ok {
			input.SSRustDNS = stringValue(root["dns"])
			input.SSRustIPv6First, _ = root["ipv6_first"].(bool)
			return input, true
		}
	}
	return Input{}, false
}

func shadowsocksRustSingleTag(root map[string]any) string {
	if name := strings.TrimSpace(shadowsocksRustName(root)); name != "" {
		return name
	}
	return "ss-rust"
}

func shadowsocksRustName(root map[string]any) string {
	for _, key := range []string{"id", "remarks", "name"} {
		if value := stringValue(root[key]); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func shadowsocksRustListen(root map[string]any) string {
	if root["server"] != nil {
		return stringValue(root["server"])
	}
	return stringValue(root["address"])
}

func shadowsocksRustPort(root map[string]any) any {
	if root["server_port"] != nil {
		return root["server_port"]
	}
	return root["port"]
}

// InheritShadowsocksRustGlobals seeds new inbound plans from the current root
// configuration so adding a port does not reset DNS or IPv6 preference.
func InheritShadowsocksRustGlobals(input Input, content string) Input {
	var root map[string]any
	if json.Unmarshal([]byte(content), &root) == nil {
		input.SSRustDNS = stringValue(root["dns"])
		input.SSRustIPv6First, _ = root["ipv6_first"].(bool)
	}
	return input
}

func shadowsocksRustProtocol(method string) string {
	if strings.HasPrefix(method, "2022-") {
		return ProtocolSS2022
	}
	return ProtocolShadowsocks
}

func parseShadowsocksRustEntry(root map[string]any, tag string) (Input, bool) {
	if root == nil {
		return Input{}, false
	}
	method, password := stringValue(root["method"]), stringValue(root["password"])
	input := Input{
		Protocol: shadowsocksRustProtocol(method), Tag: tag, Listen: shadowsocksRustListen(root),
		Port: intValue(shadowsocksRustPort(root)), Username: "default", Credential: password,
		Method: method, Transport: "raw",
		SSRustDNS: stringValue(root["dns"]), SSRustOutboundBindAddr: stringValue(root["outbound_bind_addr"]),
	}
	input.SSRustIPv6First, _ = root["ipv6_first"].(bool)
	users, _ := root["users"].([]any)
	input.presetUnsupported = stringValue(root["plugin"]) != "" || len(users) > 0 || boolValue(root["disabled"])
	input.presetUnsupported = input.presetUnsupported || root["address"] != nil && root["server"] != nil || root["port"] != nil && root["server_port"] != nil
	input.clientUnsupported = input.presetUnsupported
	input.SourceOnly = !matchesParsedPreset(core.EngineShadowsocksRust, input)
	return input, input.Listen != "" && input.Port != 0 && input.Credential != "" && method != ""
}

func shadowsocksRustExtendedEntries(root map[string]any) (string, []any, bool) {
	for _, key := range []string{"servers", "shadowsocks"} {
		entries, ok := root[key].([]any)
		if ok {
			return key, entries, true
		}
	}
	return "", nil, false
}
