package serverconfig

import (
	"net/url"
	"strconv"
)

func clientTransport(value string) string {
	switch value {
	case "websocket":
		return "ws"
	case "grpc":
		return "grpc"
	case "xhttp":
		return "xhttp"
	default:
		return "tcp"
	}
}

func addTransportQuery(query url.Values, input Input) {
	switch input.Transport {
	case "websocket":
		query.Set("path", input.TransportPath)
	case "grpc":
		query.Set("serviceName", input.TransportPath)
	case "xhttp":
		query.Set("path", input.TransportPath)
		query.Set("mode", "auto")
	}
}

func clientFields(input Input, address, serverName string) []ClientField {
	fields := []ClientField{
		{Label: "协议", Value: input.Protocol},
		{Label: "服务器", Value: address},
		{Label: "端口", Value: strconv.Itoa(input.Port)},
	}
	if input.Username != "" && input.Username != "default" {
		fields = append(fields, ClientField{Label: "用户备注", Value: input.Username})
	}
	credentialLabel := "密码"
	switch input.Protocol {
	case ProtocolShadowsocks:
		credentialLabel = "密码"
	case ProtocolSS2022:
		credentialLabel = "Base64 PSK"
	case ProtocolVLESS, ProtocolVLESSXHTTP, ProtocolVLESSEncTCP, ProtocolVLESSEncXHTTP, ProtocolVLESSEncPlain, ProtocolVMess, ProtocolTUIC:
		credentialLabel = "用户 UUID"
	case ProtocolSudoku:
		credentialLabel = "Master Public Key"
	case ProtocolWireGuard:
		credentialLabel = "客户端公钥"
	}
	if input.Protocol != ProtocolSudoku {
		credential := input.Credential
		secret := true
		if input.Protocol == ProtocolWireGuard {
			credential, secret = input.WireGuardClientPublicKey, false
		}
		fields = append(fields, ClientField{Label: credentialLabel, Value: credential, Secret: secret})
	}
	if input.Protocol == ProtocolWireGuard {
		fields = append(fields, ClientField{Label: "客户端私钥", Value: input.WireGuardClientPrivateKey, Secret: true}, ClientField{Label: "服务端公钥", Value: input.WireGuardServerPublicKey}, ClientField{Label: "客户端地址", Value: input.WireGuardClientAddress}, ClientField{Label: "AllowedIPs", Value: input.WireGuardAllowedIPs}, ClientField{Label: "MTU", Value: strconv.Itoa(input.WireGuardMTU)})
	}
	if input.SecondaryCredential != "" {
		fields = append(fields, ClientField{Label: "用户密码", Value: input.SecondaryCredential, Secret: true})
	}
	if input.Method != "" {
		fields = append(fields, ClientField{Label: "加密方法", Value: input.Method})
	}
	if isSnellProtocol(input.Protocol) {
		fields = append(fields,
			ClientField{Label: "UDP over TCP", Value: strconv.FormatBool(input.SnellUDP)},
			ClientField{Label: "TCP Fast Open", Value: "true"},
			ClientField{Label: "伪装模式", Value: input.SnellObfsMode},
		)
	}
	if input.Protocol == ProtocolSudoku {
		fields = append(fields,
			ClientField{Label: "Available Private Key", Value: input.SudokuClientKey, Secret: true},
			ClientField{Label: "HTTPMask", Value: input.SudokuHTTPMaskMode},
			ClientField{Label: "Sudoku Multiplex", Value: input.SudokuMultiplex},
		)
	}
	fields = append(fields, ClientField{Label: "传输", Value: input.Transport})
	if isVLESSEncryptionProtocol(input.Protocol) {
		fields = append(fields, ClientField{Label: "VLESS Encryption", Value: input.VLESSEncryption})
	}
	if input.TransportPath != "" {
		fields = append(fields, ClientField{Label: "路径 / ServiceName", Value: input.TransportPath})
	}
	security := "无"
	if input.RealityEnabled {
		security = "Reality"
	} else if input.TLSEnabled {
		security = "TLS"
	}
	fields = append(fields, ClientField{Label: "传输安全", Value: security})
	if serverName != "" {
		fields = append(fields, ClientField{Label: "TLS ServerName", Value: serverName})
	}
	if input.RealityEnabled {
		fields = append(fields,
			ClientField{Label: "Reality Public Key", Value: input.RealityPublicKey},
			ClientField{Label: "Reality Short ID", Value: input.RealityShortID},
			ClientField{Label: "客户端指纹", Value: "chrome"},
		)
		if input.RealityMLDSA65Verify != "" {
			fields = append(fields, ClientField{Label: "ML-DSA-65 Verify", Value: input.RealityMLDSA65Verify})
		}
	}
	return fields
}
