package serverconfig

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"slices"
	"strings"
)

const ProtocolSocks = "socks"
const ProtocolVLESSTLS = "vless-tls"
const ProtocolVLESSHTTP2Reality = "vless-http2-reality"

// Preset identity is separate from the native protocol, transport and security.
// These keys are advertised only by sing-box; existing cross-core keys stay valid.
type singBoxPreset struct {
	key, name, base, transport string
	tls, reality               bool
}

var singBoxPresets = []singBoxPreset{
	{ProtocolVLESSHTTP2Reality, "VLESS-HTTP2-REALITY", ProtocolVLESS, "http", false, true},
	{"vmess-tcp", "VMess-TCP", ProtocolVMess, "raw", false, false},
	{"vmess-ws", "VMess-WS", ProtocolVMess, "websocket", false, false},
	{"vmess-http", "VMess-HTTP", ProtocolVMess, "http", false, false},
	{"vmess-quic", "VMess-QUIC", ProtocolVMess, "quic", true, false},
}

func init() {
	for _, base := range []string{ProtocolVMess, ProtocolVLESS, ProtocolTrojan} {
		for _, transport := range []struct{ key, name, native string }{
			{"ws", "WS", "websocket"}, {"h2", "H2", "http"}, {"httpupgrade", "HTTPUpgrade", "httpupgrade"},
		} {
			name := map[string]string{ProtocolVMess: "VMess", ProtocolVLESS: "VLESS", ProtocolTrojan: "Trojan"}[base]
			singBoxPresets = append(singBoxPresets, singBoxPreset{base + "-" + transport.key + "-tls", name + "-" + transport.name + "-TLS", base, transport.native, true, false})
		}
	}
}

func singBoxPresetFor(key string) (singBoxPreset, bool) {
	for _, preset := range singBoxPresets {
		if preset.key == key {
			return preset, true
		}
	}
	return singBoxPreset{}, false
}

func nativeProtocol(key string) string {
	if key == ProtocolVLESSTLS || key == ProtocolXHTTPTLS || isXHTTPGroup(key) {
		return ProtocolVLESS
	}
	if preset, ok := singBoxPresetFor(key); ok {
		return preset.base
	}
	return key
}

func singBoxPresetProtocols() []Protocol {
	var result []Protocol
	for _, preset := range singBoxPresets {
		minimum := "1.0"
		if preset.reality {
			minimum = "1.2"
		}
		if preset.base == ProtocolVLESS && !preset.reality {
			minimum = "1.2"
		}
		if preset.transport == "httpupgrade" {
			minimum = "1.7"
		}
		dependency := ""
		if preset.tls {
			dependency = "；原生 TLS，需证书及匹配 SNI"
			if preset.transport == "websocket" || preset.transport == "httpupgrade" {
				dependency = "；默认 Caddy 自动证书反代，可选 Nginx 自签证书或原生 TLS"
			}
		}
		if preset.transport == "quic" {
			dependency += "；需 with_quic，使用 UDP 监听"
		}
		result = append(result, Protocol{Key: preset.key, Name: preset.name, Badge: strings.ToUpper(preset.base),
			Description: "sing-box " + minimum + "+" + dependency + "。参数与客户端配置同步保存。",
			Docs:        "https://sing-box.sagernet.org/configuration/shared/v2ray-transport/", DefaultPort: 443,
			Credential:         map[string]string{ProtocolVLESS: "用户 UUID", ProtocolVMess: "用户 UUID", ProtocolTrojan: "用户密码"}[preset.base],
			SupportsManagedTLS: preset.tls && (preset.transport == "websocket" || preset.transport == "httpupgrade"),
			Transports:         []string{preset.transport}, TransportConfig: true, SupportsTLS: preset.tls, RequiresTLS: preset.tls, DefaultTLS: preset.tls, UsesReality: preset.reality})
	}
	return result
}

func validateSingBoxInput(input Input, protocol Protocol) error {
	legacyVLESS := input.Protocol == ProtocolVLESS && !input.RealityEnabled && input.TLSEnabled
	if !legacyVLESS && !slices.Contains(protocol.Transports, input.Transport) {
		return fmt.Errorf("此 sing-box 方案不支持 %s 传输", input.Transport)
	}
	if preset, ok := singBoxPresetFor(input.Protocol); ok {
		if input.TLSEnabled != preset.tls || input.RealityEnabled != preset.reality {
			return fmt.Errorf("方案的 TLS / Reality 安全层不匹配，请重新选择方案")
		}
	}
	if input.TLSEnabled && input.RealityEnabled {
		return fmt.Errorf("TLS 证书与 Reality 不能同时启用")
	}
	if !legacyVLESS && input.RealityEnabled != protocol.UsesReality {
		return fmt.Errorf("此方案的 Reality 开关不匹配")
	}
	if !legacyVLESS && input.TLSEnabled && !protocol.SupportsTLS {
		return fmt.Errorf("此方案不支持 TLS")
	}
	if input.Transport == "quic" && !input.TLSEnabled {
		return fmt.Errorf("QUIC 传输必须启用 TLS")
	}
	if input.Transport == "http" || input.Transport == "httpupgrade" {
		if !strings.HasPrefix(input.TransportPath, "/") {
			return fmt.Errorf("HTTP / HTTPUpgrade 路径必须以 / 开头")
		}
	}
	if input.TransportHTTPMethod != "" {
		if input.Transport != "http" {
			return fmt.Errorf("HTTP Method 只适用于 HTTP / H2")
		}
		if len(input.TransportHTTPMethod) > 16 || strings.IndexFunc(input.TransportHTTPMethod, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
			return fmt.Errorf("HTTP Method 必须是大写字母，例如 PUT")
		}
	}
	if strings.ContainsAny(input.TransportPath+input.TransportHost+input.TLSServerName, "\r\n\x00") {
		return fmt.Errorf("传输参数不能包含控制字符")
	}
	if len(input.TransportHost) > 253 {
		return fmt.Errorf("HTTP Host 不能超过 253 字符")
	}
	if input.TLSServerName != "" {
		if _, err := NormalizeClientAddress(input.TLSServerName); err != nil {
			return fmt.Errorf("TLS SNI 必须是有效域名或 IP")
		}
	}
	if input.Flow != "" && !(nativeProtocol(input.Protocol) == ProtocolVLESS && input.Transport == "raw" && (input.RealityEnabled || input.TLSEnabled) && input.Flow == "xtls-rprx-vision") {
		return fmt.Errorf("此 sing-box 传输不能使用 Vision Flow")
	}
	alpn, err := validatedSingBoxALPN(input.TLSALPN)
	if err != nil {
		return err
	}
	if len(alpn) > 0 {
		if !input.TLSEnabled && !input.RealityEnabled {
			return fmt.Errorf("ALPN 需要 TLS 或 Reality")
		}
		if input.Transport == "http" && (input.TLSEnabled || input.RealityEnabled) && !slices.Contains(alpn, "h2") {
			return fmt.Errorf("HTTP2 传输的 ALPN 必须包含 h2")
		}
		if (input.Transport == "websocket" || input.Transport == "httpupgrade") && !slices.Contains(alpn, "http/1.1") {
			return fmt.Errorf("WebSocket / HTTPUpgrade 的 ALPN 必须包含 http/1.1")
		}
	}
	if input.TLSClientCertificate != "" {
		if !input.TLSEnabled {
			return fmt.Errorf("客户端信任证书只适用于 TLS")
		}
		remaining := []byte(input.TLSClientCertificate)
		count := 0
		for len(strings.TrimSpace(string(remaining))) > 0 {
			block, rest := pem.Decode(remaining)
			if block == nil || block.Type != "CERTIFICATE" {
				return fmt.Errorf("客户端信任证书必须为 PEM 公共证书，不能包含私钥")
			}
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				return fmt.Errorf("客户端信任证书无效: %w", err)
			}
			remaining = rest
			count++
		}
		if count == 0 {
			return fmt.Errorf("客户端信任证书为空")
		}
	}
	return nil
}

func classifySingBoxPreset(input *Input) {
	// Keep the established VMess and VLESS TCP Reality identities compatible.
	if input.Protocol == ProtocolVLESS && input.RealityEnabled && input.Transport == "raw" {
		return
	}
	if input.Protocol == ProtocolVMess && input.Transport == "websocket" && input.TLSEnabled {
		return
	}
	for _, preset := range singBoxPresets {
		if input.Protocol == preset.base && input.Transport == preset.transport && input.TLSEnabled == preset.tls && input.RealityEnabled == preset.reality {
			input.Protocol = preset.key
			return
		}
	}
	if input.Protocol == ProtocolVLESS && input.TLSEnabled {
		input.Protocol = ProtocolVLESSTLS
	}
}

func validatedSingBoxALPN(value string) ([]string, error) {
	var result []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item != "h2" && item != "http/1.1" && item != "h3" {
			return nil, fmt.Errorf("sing-box ALPN %q 无效；支持 h2、http/1.1、h3", item)
		}
		if !slices.Contains(result, item) {
			result = append(result, item)
		}
	}
	return result, nil
}

// A preset edits one account and one host. Keep richer imported configurations
// in the source editor rather than dropping fields on save or client export.
func singBoxPresetEditError(entry map[string]any) error {
	unsupported := func(field string) error {
		return fmt.Errorf("sing-box 入站 %q 的 %s 无法由单账号预设完整表示，请使用完整源码编辑", stringValue(entry["tag"]), field)
	}
	users, _ := entry["users"].([]any)
	if len(users) > 1 || stringValue(entry["type"]) == "shadowsocks" && len(users) != 0 {
		return unsupported("多用户配置")
	}
	if _, exists := entry["obfs"]; exists {
		return unsupported("自定义混淆")
	}
	if stringValue(entry["type"]) != "direct" && entry["network"] != nil && entry["network"] != "" {
		return unsupported("单独限制监听网络")
	}
	for key, value := range firstMap(entry["users"]) {
		if !slices.Contains([]string{"name", "username", "uuid", "password", "flow", "alterId"}, key) || key == "alterId" && intValue(value) != 0 {
			return unsupported("users." + key)
		}
	}
	tls := mapValue(entry["tls"])
	for key := range tls {
		if !slices.Contains([]string{"enabled", "server_name", "certificate_path", "key_path", "alpn", "reality"}, key) {
			return unsupported("tls." + key)
		}
	}
	if reality := mapValue(tls["reality"]); reality != nil {
		for key := range reality {
			if !slices.Contains([]string{"enabled", "private_key", "short_id", "handshake"}, key) {
				return unsupported("tls.reality." + key)
			}
		}
		if ids, ok := reality["short_id"].([]any); ok && len(ids) > 1 {
			return unsupported("Reality 多 Short ID")
		}
		handshake := mapValue(reality["handshake"])
		if stringValue(handshake["server"]) != stringValue(tls["server_name"]) || intValue(handshake["server_port"]) != 443 || len(handshake) != 2 {
			return unsupported("Reality 自定义握手目标")
		}
	}
	transport := mapValue(entry["transport"])
	allowed := map[string][]string{
		"": {}, "ws": {"type", "path", "headers", "early_data_header_name"},
		"http": {"type", "path", "host", "method"}, "httpupgrade": {"type", "path", "host"},
		"grpc": {"type", "service_name"}, "quic": {"type"},
	}
	keys, known := allowed[stringValue(transport["type"])]
	if !known {
		return unsupported("transport.type")
	}
	for key := range transport {
		if !slices.Contains(keys, key) {
			return unsupported("transport." + key)
		}
	}
	if hosts, ok := transport["host"].([]any); ok && len(hosts) > 1 {
		return unsupported("HTTP 多 Host")
	}
	for key, value := range mapValue(transport["headers"]) {
		if !strings.EqualFold(key, "Host") || stringValue(value) == "" {
			return unsupported("transport.headers." + key)
		}
	}
	if header := stringValue(transport["early_data_header_name"]); header != "" && header != "Sec-WebSocket-Protocol" {
		return unsupported("WebSocket 自定义 early data header")
	}
	return nil
}
