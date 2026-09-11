package serverconfig

import (
	"errors"
	"strings"
)

// buildMihomoClientYAML uses the same validated, normalized input as the URL
// builder. Only client credentials are copied; server private keys and paths
// must never enter a subscription. Keys follow Mihomo's adapter/outbound types.
func buildMihomoClientYAML(input Input, address, serverName, name string) (string, error) {
	if input.RealityMLDSA65Verify != "" {
		return "", errors.New("Mihomo 暂不支持此配置的 Reality ML-DSA-65 校验，请选择 URL 格式")
	}
	if input.Protocol == ProtocolSudoku {
		return buildSudokuMihomoYAML(input, address, name)
	}
	proxy := map[string]any{
		"name": name, "server": address, "port": input.Port, "udp": true,
	}
	switch input.Protocol {
	case ProtocolShadowsocks, ProtocolSS2022:
		proxy["type"], proxy["cipher"], proxy["password"] = "ss", input.Method, input.Credential
	case ProtocolVLESS, ProtocolVLESSXHTTP, ProtocolVLESSEncTCP, ProtocolVLESSEncXHTTP:
		proxy["type"], proxy["uuid"] = "vless", input.Credential
		if input.Flow != "" {
			proxy["flow"] = input.Flow
		}
		if isVLESSEncryptionProtocol(input.Protocol) {
			proxy["encryption"] = input.VLESSEncryption
		}
		if input.RealityEnabled {
			proxy["reality-opts"] = map[string]any{"public-key": input.RealityPublicKey, "short-id": input.RealityShortID}
			proxy["client-fingerprint"] = "chrome"
		}
	case ProtocolVMess:
		proxy["type"], proxy["uuid"], proxy["alterId"], proxy["cipher"] = "vmess", input.Credential, 0, "auto"
	case ProtocolTrojan:
		proxy["type"], proxy["password"] = "trojan", input.Credential
	case ProtocolHy2:
		proxy["type"], proxy["password"] = "hysteria2", input.Credential
		proxy["alpn"] = []string{"h3"}
	case ProtocolTUIC:
		proxy["type"], proxy["uuid"], proxy["password"] = "tuic", input.Credential, input.SecondaryCredential
		proxy["congestion-controller"], proxy["udp-relay-mode"] = "bbr", "native"
		proxy["alpn"] = []string{"h3"}
	case ProtocolAnyTLS:
		proxy["type"], proxy["password"] = "anytls", input.Credential
	case ProtocolSnell, ProtocolSnellShadowTLS:
		proxy["type"], proxy["psk"], proxy["version"] = "snell", input.Credential, input.SnellVersion
		proxy["udp"], proxy["reuse"], proxy["tfo"] = input.SnellUDP, input.SnellReuse, true
		if input.SnellObfsMode == SnellObfsShadowTLS {
			proxy["client-fingerprint"] = input.SnellClientFingerprint
			proxy["obfs-opts"] = map[string]any{
				"mode": "shadow-tls", "host": input.SnellObfsHost, "password": input.SnellShadowTLSPassword,
				"version": input.SnellShadowTLSVersion, "alpn": strings.Split(input.SnellShadowTLSALPN, ","),
			}
		}
	default:
		return "", errors.New("该协议不支持 Mihomo 同步格式")
	}
	proxyType := proxy["type"]
	if proxyType == "vless" || proxyType == "vmess" {
		if input.TLSEnabled || input.RealityEnabled {
			proxy["tls"] = true
		}
		if serverName != "" {
			proxy["servername"] = serverName
		}
	} else if serverName != "" {
		proxy["sni"] = serverName
	}
	if proxyType == "vless" || proxyType == "vmess" || proxyType == "trojan" {
		switch input.Transport {
		case "websocket":
			proxy["network"] = "ws"
			opts := map[string]any{"path": input.TransportPath}
			if serverName != "" {
				opts["headers"] = map[string]string{"Host": serverName}
			}
			proxy["ws-opts"] = opts
		case "grpc":
			proxy["network"] = "grpc"
			proxy["grpc-opts"] = map[string]string{"grpc-service-name": input.TransportPath}
		case "xhttp":
			proxy["network"] = "xhttp"
			proxy["xhttp-opts"] = map[string]string{"path": input.TransportPath, "mode": "auto"}
		}
	}
	return marshalSingleLineYAML(proxy)
}
