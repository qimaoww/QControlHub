package serverconfig

import (
	"encoding/json"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func generateXray(input Input) (string, error) {
	inbound := map[string]any{
		"tag": input.Tag, "listen": input.Listen, "port": input.Port,
	}
	switch input.Protocol {
	case ProtocolSS2022:
		inbound["protocol"] = "shadowsocks"
		inbound["settings"] = map[string]any{
			"network": "tcp,udp", "method": input.Method, "password": input.Credential,
		}
	case ProtocolVLESS:
		inbound["protocol"] = "vless"
		user := map[string]any{"id": input.Credential, "level": 0, "email": input.Username}
		if input.Flow != "" {
			user["flow"] = input.Flow
		}
		inbound["settings"] = map[string]any{"users": []any{user}, "decryption": "none"}
	case ProtocolVMess:
		inbound["protocol"] = "vmess"
		inbound["settings"] = map[string]any{
			"users": []any{map[string]any{"id": input.Credential, "level": 0, "email": input.Username}},
		}
	case ProtocolTrojan:
		inbound["protocol"] = "trojan"
		inbound["settings"] = map[string]any{"users": []any{map[string]any{"password": input.Credential, "email": input.Username, "level": 0}}}
	case ProtocolHy2:
		inbound["protocol"] = "hysteria"
		inbound["settings"] = map[string]any{"version": 2, "users": []any{map[string]any{"auth": input.Credential, "email": input.Username, "level": 0}}}
	case ProtocolPortForward:
		inbound["protocol"] = "tunnel"
		inbound["settings"] = map[string]any{
			"allowedNetwork": input.Network, "rewriteAddress": input.TargetAddress,
			"rewritePort": input.TargetPort, "followRedirect": false, "userLevel": 0,
		}
	}
	if input.Protocol != ProtocolSS2022 && input.Protocol != ProtocolPortForward {
		stream := xrayStream(input)
		if input.Protocol == ProtocolHy2 {
			stream["network"] = "hysteria"
		}
		inbound["streamSettings"] = stream
	}
	root := map[string]any{
		"log":      map[string]any{"loglevel": "info"},
		"inbounds": []any{inbound},
		"outbounds": []any{
			map[string]any{"protocol": "freedom", "tag": "direct"},
			map[string]any{"protocol": "blackhole", "tag": "block"},
		},
	}
	value, err := json.MarshalIndent(root, "", "  ")
	return string(value) + "\n", err
}

func xrayStream(input Input) map[string]any {
	network := "raw"
	stream := map[string]any{}
	switch input.Transport {
	case "websocket":
		network = "ws"
		stream["wsSettings"] = map[string]any{"path": input.TransportPath}
	case "grpc":
		network = "grpc"
		stream["grpcSettings"] = map[string]any{"serviceName": input.TransportPath}
	}
	stream["network"] = network
	if input.TLSEnabled {
		stream["security"] = "tls"
		stream["tlsSettings"] = map[string]any{"certificates": []any{map[string]any{
			"certificateFile": input.CertificatePath, "keyFile": input.PrivateKeyPath,
		}}}
	}
	if input.RealityEnabled {
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{
			"show": false, "target": input.RealityServerName + ":443", "xver": 0, "minClientVer": "0.0.0",
			"serverNames": []string{input.RealityServerName}, "privateKey": input.RealityPrivateKey,
			"shortIds": []string{input.RealityShortID},
		}
	}
	return stream
}

func parseXray(content string) (Input, bool) {
	var root map[string]any
	if json.Unmarshal([]byte(content), &root) != nil {
		return Input{}, false
	}
	inbound := firstSupportedInbound(core.EngineXray, root["inbounds"], "protocol")
	if inbound == nil {
		return Input{}, false
	}
	input := Input{
		Protocol: protocolKey(stringValue(inbound["protocol"])), Tag: stringValue(inbound["tag"]),
		Listen: stringValue(inbound["listen"]), Port: intValue(inbound["port"]), Username: "default", Transport: "raw",
	}
	settings := mapValue(inbound["settings"])
	input.Method, input.Credential = stringValue(settings["method"]), stringValue(settings["password"])
	if input.Protocol == ProtocolPortForward {
		input.Network = networkListValue(stringValue(settings["allowedNetwork"]))
		input.TargetAddress = stringValue(settings["rewriteAddress"])
		input.TargetPort = intValue(settings["rewritePort"])
		if input.TargetAddress == "" || input.TargetPort == 0 {
			input.Network = networkListValue(stringValue(settings["network"]))
			input.TargetAddress = stringValue(settings["address"])
			input.TargetPort = intValue(settings["port"])
		}
		if input.Network == "" {
			input.Network = "tcp"
		}
	}
	if user := firstMap(settings["users"]); user != nil {
		input.Username = stringValue(user["email"])
		input.Credential = stringValue(user["id"])
		input.Flow = stringValue(user["flow"])
		if input.Protocol == ProtocolTrojan {
			input.Credential = stringValue(user["password"])
		}
		if input.Protocol == ProtocolHy2 {
			input.Credential = stringValue(user["auth"])
		}
	}
	stream := mapValue(inbound["streamSettings"])
	switch stringValue(stream["network"]) {
	case "ws":
		input.Transport = "websocket"
		input.TransportPath = stringValue(mapValue(stream["wsSettings"])["path"])
	case "grpc":
		input.Transport = "grpc"
		input.TransportPath = stringValue(mapValue(stream["grpcSettings"])["serviceName"])
	}
	if stringValue(stream["security"]) == "tls" {
		input.TLSEnabled = true
		if certificate := firstMap(mapValue(stream["tlsSettings"])["certificates"]); certificate != nil {
			input.CertificatePath = stringValue(certificate["certificateFile"])
			input.PrivateKeyPath = stringValue(certificate["keyFile"])
		}
	}
	if stringValue(stream["security"]) == "reality" {
		reality := mapValue(stream["realitySettings"])
		input.RealityEnabled = true
		input.RealityPrivateKey = stringValue(reality["privateKey"])
		input.RealityPublicKey = realityPublicKey(input.RealityPrivateKey)
		input.RealityShortID = firstString(reality["shortIds"])
		input.RealityServerName = firstString(reality["serverNames"])
	}
	return input, parsedInputValid(input)
}
