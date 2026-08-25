package serverconfig

import (
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

func generateMihomo(input Input) (string, error) {
	listener := map[string]any{
		"name": input.Tag, "listen": input.Listen, "port": input.Port,
	}
	switch input.Protocol {
	case ProtocolSS2022:
		listener["type"] = "shadowsocks"
		listener["cipher"] = input.Method
		listener["password"] = input.Credential
		listener["udp"] = true
	case ProtocolVLESS:
		listener["type"] = "vless"
		user := map[string]any{"username": input.Username, "uuid": input.Credential}
		if input.Flow != "" {
			user["flow"] = input.Flow
		}
		listener["users"] = []any{user}
		mihomoTransport(listener, input)
	case ProtocolVMess:
		listener["type"] = "vmess"
		listener["users"] = []any{map[string]any{"username": input.Username, "uuid": input.Credential, "alterId": 0}}
		mihomoTransport(listener, input)
	case ProtocolTrojan:
		listener["type"] = "trojan"
		listener["users"] = []any{map[string]any{"username": input.Username, "password": input.Credential}}
		mihomoTransport(listener, input)
	case ProtocolHy2:
		listener["type"] = "hysteria2"
		listener["users"] = map[string]any{input.Username: input.Credential}
		listener["up"], listener["down"] = 100, 100
		listener["alpn"] = []string{"h3"}
	case ProtocolTUIC:
		listener["type"] = "tuic"
		listener["users"] = map[string]any{input.Credential: input.SecondaryCredential}
		listener["congestion-controller"] = "bbr"
		listener["max-idle-time"] = 15000
		listener["authentication-timeout"] = 1000
		listener["alpn"] = []string{"h3"}
	case ProtocolAnyTLS:
		listener["type"] = "anytls"
		listener["users"] = map[string]any{input.Username: input.Credential}
	case ProtocolPortForward:
		listener["type"] = "tunnel"
		listener["network"] = strings.Split(input.Network, ",")
		listener["target"] = forwardTarget(input)
	}
	if input.TLSEnabled {
		listener["certificate"] = input.CertificatePath
		listener["private-key"] = input.PrivateKeyPath
	}
	if input.RealityEnabled {
		listener["reality-config"] = map[string]any{
			"dest": input.RealityServerName + ":443", "private-key": input.RealityPrivateKey,
			"short-id": []string{input.RealityShortID}, "server-names": []string{input.RealityServerName},
		}
	}
	root := map[string]any{
		"log-level": "info", "listeners": []any{listener}, "rules": []string{"MATCH,DIRECT"},
	}
	value, err := yaml.Marshal(root)
	return string(value), err
}

func parseMihomo(content string) (Input, bool) {
	var root map[string]any
	if yaml.Unmarshal([]byte(content), &root) != nil {
		return Input{}, false
	}
	listener := firstSupportedInbound(core.EngineMihomo, root["listeners"], "type")
	if listener == nil {
		return Input{}, false
	}
	input := Input{
		Protocol: protocolKey(stringValue(listener["type"])), Tag: stringValue(listener["name"]),
		Listen: stringValue(listener["listen"]), Port: intValue(listener["port"]), Username: "default",
		Method: stringValue(listener["cipher"]), Credential: stringValue(listener["password"]), Transport: "raw",
		CertificatePath: stringValue(listener["certificate"]), PrivateKeyPath: stringValue(listener["private-key"]),
	}
	if input.Protocol == ProtocolPortForward {
		input.Network = networkListValue(listener["network"])
		input.TargetAddress, input.TargetPort, _ = splitForwardTarget(stringValue(listener["target"]))
	}
	input.TLSEnabled = input.CertificatePath != "" && input.PrivateKeyPath != ""
	if reality := mapValue(listener["reality-config"]); reality != nil {
		input.RealityEnabled = true
		input.RealityPrivateKey = stringValue(reality["private-key"])
		input.RealityShortID = firstString(reality["short-id"])
		input.RealityServerName = firstString(reality["server-names"])
		input.RealityPublicKey = realityPublicKey(input.RealityPrivateKey)
	}
	if value := stringValue(listener["ws-path"]); value != "" {
		input.Transport, input.TransportPath = "websocket", value
	}
	if value := stringValue(listener["grpc-service-name"]); value != "" {
		input.Transport, input.TransportPath = "grpc", value
	}
	if user := firstMap(listener["users"]); user != nil {
		input.Username = stringValue(user["username"])
		input.Credential = stringValue(user["uuid"])
		input.Flow = stringValue(user["flow"])
	}
	if input.Protocol == ProtocolTrojan {
		if user := firstMap(listener["users"]); user != nil {
			input.Username, input.Credential = stringValue(user["username"]), stringValue(user["password"])
		}
	}
	if input.Protocol == ProtocolHy2 {
		if users := mapValue(listener["users"]); len(users) > 0 {
			for name, password := range users {
				input.Username, input.Credential = name, stringValue(password)
				break
			}
		}
	}
	if input.Protocol == ProtocolTUIC {
		if users := mapValue(listener["users"]); len(users) > 0 {
			for uuid, password := range users {
				input.Credential, input.SecondaryCredential = uuid, stringValue(password)
				break
			}
		}
	}
	if input.Protocol == ProtocolAnyTLS {
		if users := mapValue(listener["users"]); len(users) > 0 {
			for name, password := range users {
				input.Username, input.Credential = name, stringValue(password)
				break
			}
		}
	}
	return input, parsedInputValid(input)
}

func mihomoTransport(listener map[string]any, input Input) {
	switch input.Transport {
	case "websocket":
		listener["ws-path"] = input.TransportPath
	case "grpc":
		listener["grpc-service-name"] = input.TransportPath
	}
}
