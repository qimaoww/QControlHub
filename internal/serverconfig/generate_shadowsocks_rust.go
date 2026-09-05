package serverconfig

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
)

func generateShadowsocksRust(input Input) (string, error) {
	input.SSRustDNS = strings.TrimSpace(input.SSRustDNS)
	input.SSRustOutboundBindAddr = strings.TrimSpace(input.SSRustOutboundBindAddr)
	if len(input.SSRustDNS) > 1024 || strings.ContainsAny(input.SSRustDNS, "\r\n\x00") {
		return "", errors.New("端口 DNS 不能超过 1024 字符或包含换行和空字符")
	}
	if input.SSRustOutboundBindAddr != "" && net.ParseIP(input.SSRustOutboundBindAddr) == nil {
		return "", errors.New("出站绑定地址必须是 IP 地址")
	}
	root := map[string]any{
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
	if input, ok := parseShadowsocksRustEntry(root, "ss-rust"); ok {
		return input, true
	}
	_, entries, ok := shadowsocksRustExtendedEntries(root)
	if !ok {
		return Input{}, false
	}
	for index, value := range entries {
		entry := mapValue(value)
		if input, ok := parseShadowsocksRustEntry(entry, shadowsocksRustEntryTag(entry, index)); ok {
			input.SSRustIPv6First, _ = root["ipv6_first"].(bool)
			return input, true
		}
	}
	return Input{}, false
}

func parseShadowsocksRustEntry(root map[string]any, tag string) (Input, bool) {
	if root == nil {
		return Input{}, false
	}
	method, password := stringValue(root["method"]), stringValue(root["password"])
	protocol := ProtocolShadowsocks
	if strings.HasPrefix(method, "2022-") {
		protocol = ProtocolSS2022
	}
	input := Input{
		Protocol: protocol, Tag: tag, Listen: stringValue(root["server"]),
		Port: intValue(root["server_port"]), Username: "default", Credential: password,
		Method: method, Transport: "raw",
		SSRustDNS: stringValue(root["dns"]), SSRustOutboundBindAddr: stringValue(root["outbound_bind_addr"]),
	}
	input.SSRustIPv6First, _ = root["ipv6_first"].(bool)
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
