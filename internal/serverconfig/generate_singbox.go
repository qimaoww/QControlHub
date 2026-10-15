package serverconfig

import (
	"encoding/json"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func generateSingBox(input Input) (string, error) {
	if input.Protocol == ProtocolWireGuard {
		return generateSingBoxEndpoint(input)
	}
	inbound := map[string]any{
		"tag": input.Tag, "listen": input.Listen, "listen_port": input.Port,
	}
	switch nativeProtocol(input.Protocol) {
	case ProtocolShadowsocks, ProtocolSS2022:
		inbound["type"] = "shadowsocks"
		inbound["method"] = input.Method
		inbound["password"] = input.Credential
	case ProtocolVLESS:
		inbound["type"] = "vless"
		user := map[string]any{"name": input.Username, "uuid": input.Credential}
		if input.Flow != "" {
			user["flow"] = input.Flow
		}
		inbound["users"] = []any{user}
		singBoxTransport(inbound, input)
	case ProtocolVMess:
		inbound["type"] = "vmess"
		inbound["users"] = []any{map[string]any{"name": input.Username, "uuid": input.Credential, "alterId": 0}}
		singBoxTransport(inbound, input)
	case ProtocolTrojan:
		inbound["type"] = "trojan"
		inbound["users"] = []any{map[string]any{"name": input.Username, "password": input.Credential}}
		singBoxTransport(inbound, input)
	case ProtocolHy2:
		inbound["type"] = "hysteria2"
		inbound["up_mbps"], inbound["down_mbps"] = 100, 100
		inbound["users"] = []any{map[string]any{"name": input.Username, "password": input.Credential}}
	case ProtocolTUIC:
		inbound["type"] = "tuic"
		inbound["users"] = []any{map[string]any{"name": input.Username, "uuid": input.Credential, "password": input.SecondaryCredential}}
		inbound["congestion_control"] = "cubic"
		inbound["auth_timeout"] = "3s"
		inbound["heartbeat"] = "10s"
	case ProtocolAnyTLS:
		inbound["type"] = "anytls"
		inbound["users"] = []any{map[string]any{"name": input.Username, "password": input.Credential}}
	case ProtocolSocks:
		inbound["type"] = "socks"
		inbound["users"] = []any{map[string]any{"username": input.Username, "password": input.Credential}}
	case ProtocolPortForward:
		inbound["type"] = "direct"
		if input.Network != "tcp,udp" {
			inbound["network"] = input.Network
		}
		inbound["override_address"] = input.TargetAddress
		inbound["override_port"] = input.TargetPort
	}
	if input.TLSEnabled {
		inbound["tls"] = map[string]any{
			"enabled": true, "certificate_path": input.CertificatePath, "key_path": input.PrivateKeyPath,
			"server_name": input.TLSServerName,
		}
	}
	if input.RealityEnabled {
		inbound["tls"] = map[string]any{
			"enabled": true, "server_name": input.RealityServerName,
			"reality": map[string]any{
				"enabled": true, "handshake": map[string]any{"server": input.RealityServerName, "server_port": 443},
				"private_key": input.RealityPrivateKey, "short_id": []string{input.RealityShortID},
			},
		}
	}
	if tls := mapValue(inbound["tls"]); tls != nil && input.TLSALPN != "" {
		tls["alpn"], _ = validatedSingBoxALPN(input.TLSALPN)
	}
	root := map[string]any{
		"$schema":   "https://sing-box.sagernet.org/schema.json",
		"log":       map[string]any{"level": "info"},
		"inbounds":  []any{inbound},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
	}
	value, err := json.MarshalIndent(root, "", "  ")
	return string(value) + "\n", err
}

func singBoxTransport(inbound map[string]any, input Input) {
	switch input.Transport {
	case "websocket":
		inbound["transport"] = map[string]any{"type": "ws", "path": input.TransportPath, "early_data_header_name": "Sec-WebSocket-Protocol"}
		if input.TransportHost != "" {
			mapValue(inbound["transport"])["headers"] = map[string]any{"Host": input.TransportHost}
		}
	case "http":
		t := map[string]any{"type": "http", "path": input.TransportPath}
		if input.TransportHTTPMethod != "" {
			t["method"] = input.TransportHTTPMethod
		}
		if input.TransportHost != "" {
			t["host"] = []string{input.TransportHost}
		}
		inbound["transport"] = t
	case "httpupgrade":
		inbound["transport"] = map[string]any{"type": "httpupgrade", "path": input.TransportPath, "host": input.TransportHost}
	case "quic":
		inbound["transport"] = map[string]any{"type": "quic"}
	case "grpc":
		inbound["transport"] = map[string]any{"type": "grpc", "service_name": input.TransportPath}
	}
}

func parseSingBox(content string) (Input, bool) {
	var root map[string]any
	if json.Unmarshal([]byte(content), &root) != nil {
		return Input{}, false
	}
	inbound := firstSupportedInbound(core.EngineSingBox, root["inbounds"], "type")
	if inbound == nil {
		if endpoints, ok := root["endpoints"].([]any); ok {
			for _, raw := range endpoints {
				entry := mapValue(raw)
				if parsed, valid := parseSingBoxEndpointProtocol(entry); valid {
					parsed.BlockMainlandDestination, parsed.BlockMainlandSource = mainlandSingBoxFlags(root, parsed.Tag)
					return parsed, true
				}
			}
		}
		return Input{}, false
	}
	input := Input{
		Protocol: protocolKey(stringValue(inbound["type"])), Tag: stringValue(inbound["tag"]),
		Listen: stringValue(inbound["listen"]), Port: intValue(inbound["listen_port"]), Username: "default",
		Method: stringValue(inbound["method"]), Credential: stringValue(inbound["password"]), Transport: "raw",
	}
	if input.Protocol == ProtocolSS2022 && !strings.HasPrefix(input.Method, "2022-") {
		input.Protocol = ProtocolShadowsocks
	}
	if input.Protocol == ProtocolPortForward {
		input.Network = networkListValue(stringValue(inbound["network"]))
		if input.Network == "" {
			input.Network = "tcp,udp"
		}
		input.TargetAddress = stringValue(inbound["override_address"])
		input.TargetPort = intValue(inbound["override_port"])
	}
	if user := firstMap(inbound["users"]); user != nil {
		input.Username = stringValue(user["name"])
		input.Credential = stringValue(user["uuid"])
		input.Flow = stringValue(user["flow"])
		if input.Protocol == ProtocolTrojan || input.Protocol == ProtocolHy2 || input.Protocol == ProtocolAnyTLS {
			input.Credential = stringValue(user["password"])
		}
		if input.Protocol == ProtocolSocks {
			input.Username = stringValue(user["username"])
			input.Credential = stringValue(user["password"])
		}
		if input.Protocol == ProtocolTUIC {
			input.Credential = stringValue(user["uuid"])
			input.SecondaryCredential = stringValue(user["password"])
		}
	}
	transport := mapValue(inbound["transport"])
	switch stringValue(transport["type"]) {
	case "ws":
		input.Transport = "websocket"
		input.TransportPath = stringValue(transport["path"])
	case "http":
		input.Transport = "http"
		input.TransportPath = stringValue(transport["path"])
		input.TransportHost = firstString(transport["host"])
		input.TransportHTTPMethod = stringValue(transport["method"])
		if host := stringValue(transport["host"]); host != "" {
			input.TransportHost = host
		}
	case "httpupgrade":
		input.Transport = "httpupgrade"
		input.TransportPath = stringValue(transport["path"])
		input.TransportHost = stringValue(transport["host"])
	case "quic":
		input.Transport = "quic"
	case "grpc":
		input.Transport = "grpc"
		input.TransportPath = stringValue(transport["service_name"])
	}
	if input.Transport == "websocket" {
		for key, value := range mapValue(transport["headers"]) {
			if strings.EqualFold(key, "host") {
				input.TransportHost = stringValue(value)
			}
		}
	}
	if input.TransportPath == "" && (input.Transport == "http" || input.Transport == "websocket" || input.Transport == "httpupgrade") {
		input.TransportPath = "/"
	}
	tls := mapValue(inbound["tls"])
	if enabled, _ := tls["enabled"].(bool); enabled {
		input.TLSEnabled = true
		input.TLSServerName = stringValue(tls["server_name"])
		input.TLSALPN = strings.Join(stringSliceValue(tls["alpn"]), ",")
		input.CertificatePath = stringValue(tls["certificate_path"])
		input.PrivateKeyPath = stringValue(tls["key_path"])
	}
	if reality := mapValue(tls["reality"]); reality["enabled"] == true {
		input.RealityEnabled = true
		input.TLSServerName = ""
		input.TLSEnabled = false
		input.CertificatePath, input.PrivateKeyPath = "", ""
		input.RealityPrivateKey = stringValue(reality["private_key"])
		input.RealityPublicKey = realityPublicKey(input.RealityPrivateKey)
		input.RealityShortID = firstString(reality["short_id"])
		input.RealityServerName = stringValue(tls["server_name"])
	}
	classifySingBoxPreset(&input)
	input.BlockMainlandDestination, input.BlockMainlandSource = mainlandSingBoxFlags(root, input.Tag)
	return input, parsedInputValid(input)
}
