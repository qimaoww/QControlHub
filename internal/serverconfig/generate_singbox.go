package serverconfig

import (
	"encoding/json"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func generateSingBox(input Input) (string, error) {
	inbound := map[string]any{
		"tag": input.Tag, "listen": input.Listen, "listen_port": input.Port,
	}
	switch input.Protocol {
	case ProtocolSS2022:
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
	}
	if input.TLSEnabled {
		inbound["tls"] = map[string]any{
			"enabled": true, "certificate_path": input.CertificatePath, "key_path": input.PrivateKeyPath,
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
		inbound["transport"] = map[string]any{"type": "ws", "path": input.TransportPath}
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
		return Input{}, false
	}
	input := Input{
		Protocol: protocolKey(stringValue(inbound["type"])), Tag: stringValue(inbound["tag"]),
		Listen: stringValue(inbound["listen"]), Port: intValue(inbound["listen_port"]), Username: "default",
		Method: stringValue(inbound["method"]), Credential: stringValue(inbound["password"]), Transport: "raw",
	}
	if user := firstMap(inbound["users"]); user != nil {
		input.Username = stringValue(user["name"])
		input.Credential = stringValue(user["uuid"])
		input.Flow = stringValue(user["flow"])
		if input.Protocol == ProtocolTrojan || input.Protocol == ProtocolHy2 || input.Protocol == ProtocolAnyTLS {
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
	case "grpc":
		input.Transport = "grpc"
		input.TransportPath = stringValue(transport["service_name"])
	}
	tls := mapValue(inbound["tls"])
	if enabled, _ := tls["enabled"].(bool); enabled {
		input.TLSEnabled = true
		input.CertificatePath = stringValue(tls["certificate_path"])
		input.PrivateKeyPath = stringValue(tls["key_path"])
	}
	if reality := mapValue(tls["reality"]); reality != nil {
		input.RealityEnabled = true
		input.TLSEnabled = false
		input.CertificatePath, input.PrivateKeyPath = "", ""
		input.RealityPrivateKey = stringValue(reality["private_key"])
		input.RealityPublicKey = realityPublicKey(input.RealityPrivateKey)
		input.RealityShortID = firstString(reality["short_id"])
		input.RealityServerName = stringValue(tls["server_name"])
	}
	return input, input.Protocol != "" && input.Tag != "" && input.Port != 0 && input.Credential != ""
}
