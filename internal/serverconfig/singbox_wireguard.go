package serverconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

func generateSingBoxEndpoint(input Input) (string, error) {
	if input.Protocol == ProtocolTailscale {
		return generateSingBoxTailscaleEndpoint(input)
	}
	if input.Protocol == ProtocolOpenVPNServer {
		return generateSingBoxOpenVPNEndpoint(input)
	}
	if err := validateWireGuardInput(input, true); err != nil {
		return "", err
	}
	allowed := splitWireGuardList(input.WireGuardClientAddress)
	addresses := splitWireGuardList(input.WireGuardServerAddress)
	if len(addresses) == 0 {
		return "", fmt.Errorf("WireGuard 服务端地址不能为空")
	}
	for _, address := range addresses {
		if _, _, err := net.ParseCIDR(address); err != nil {
			return "", fmt.Errorf("WireGuard 服务端地址无效: %s", address)
		}
	}
	peer := map[string]any{"public_key": input.WireGuardClientPublicKey, "allowed_ips": allowed}
	if input.WireGuardPresharedKey != "" {
		peer["pre_shared_key"] = input.WireGuardPresharedKey
	}
	if input.WireGuardKeepalive > 0 {
		peer["persistent_keepalive_interval"] = input.WireGuardKeepalive
	}
	endpoint := map[string]any{
		"type": "wireguard", "tag": input.Tag, "system": false,
		"mtu": input.WireGuardMTU, "address": addresses,
		"private_key": input.WireGuardServerPrivateKey, "listen_port": input.Port,
		"peers": []any{peer},
	}
	root := map[string]any{"log": map[string]any{"level": "info"}, "endpoints": []any{endpoint}, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}}
	value, err := json.MarshalIndent(root, "", "  ")
	return string(value) + "\n", err
}

func generateSingBoxTailscaleEndpoint(input Input) (string, error) {
	if input.TailscaleStateDirectory == "" {
		return "", fmt.Errorf("Tailscale 状态目录不能为空")
	}
	endpoint := map[string]any{"type": "tailscale", "tag": input.Tag, "state_directory": input.TailscaleStateDirectory, "hostname": input.TailscaleHostname}
	if input.TailscaleControlURL != "" {
		endpoint["control_url"] = input.TailscaleControlURL
	}
	if input.Port > 0 {
		endpoint["listen_port"] = input.Port
	}
	root := map[string]any{"log": map[string]any{"level": "info"}, "endpoints": []any{endpoint}, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}}
	value, err := json.MarshalIndent(root, "", "  ")
	return string(value) + "\n", err
}

func generateSingBoxOpenVPNEndpoint(input Input) (string, error) {
	if input.OpenVPNServerCertificatePath == "" || input.OpenVPNServerKeyPath == "" || input.OpenVPNClientCAPath == "" {
		return "", fmt.Errorf("OpenVPN 证书路径不能为空")
	}
	if input.OpenVPNAddress == "" {
		input.OpenVPNAddress = "10.77.0.1/24"
	}
	endpoint := map[string]any{"type": "openvpn-server", "tag": input.Tag, "listen_port": input.Port, "network": "udp", "address": []string{input.OpenVPNAddress}, "mode": "tls", "max_clients": 1024,
		"users": []any{map[string]any{"username": input.OpenVPNUsername, "password": input.OpenVPNPassword}},
		"tls":   map[string]any{"certificate_path": input.OpenVPNServerCertificatePath, "key_path": input.OpenVPNServerKeyPath, "client_certificate_path": input.OpenVPNClientCAPath, "verify_client_certificate": "require"}}
	root := map[string]any{"log": map[string]any{"level": "info"}, "endpoints": []any{endpoint}, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}}
	value, err := json.MarshalIndent(root, "", "  ")
	return string(value) + "\n", err
}

func parseSingBoxWireGuardEndpoint(entry map[string]any) (Input, bool) {
	if stringValue(entry["type"]) != "wireguard" {
		return Input{}, false
	}
	peers, ok := entry["peers"].([]any)
	if !ok || len(peers) != 1 {
		return Input{}, false
	}
	peer := mapValue(peers[0])
	if peer == nil || stringValue(entry["private_key"]) == "" || stringValue(peer["public_key"]) == "" {
		return Input{}, false
	}
	input := Input{Protocol: ProtocolWireGuard, Tag: stringValue(entry["tag"]), Port: intValue(entry["listen_port"]), Listen: "0.0.0.0", Transport: "raw", Username: "default", WireGuardServerPrivateKey: stringValue(entry["private_key"]), WireGuardServerPublicKey: wireguardServerPublic(stringValue(entry["private_key"])), WireGuardServerAddress: strings.Join(stringSliceValue(entry["address"]), ","), WireGuardClientPublicKey: stringValue(peer["public_key"]), WireGuardClientAddress: strings.Join(stringSliceValue(peer["allowed_ips"]), ","), WireGuardPresharedKey: stringValue(peer["pre_shared_key"]), WireGuardMTU: intValue(entry["mtu"]), WireGuardKeepalive: intValue(peer["persistent_keepalive_interval"])}
	if input.WireGuardMTU == 0 {
		input.WireGuardMTU = 1408
	}
	return input, validateWireGuardInput(input, false) == nil
}

func parseSingBoxEndpointProtocol(entry map[string]any) (Input, bool) {
	switch stringValue(entry["type"]) {
	case "wireguard":
		return parseSingBoxWireGuardEndpoint(entry)
	case "tailscale":
		input := Input{Protocol: ProtocolTailscale, Tag: stringValue(entry["tag"]), Port: intValue(entry["listen_port"]), TailscaleStateDirectory: stringValue(entry["state_directory"]), TailscaleAuthKey: stringValue(entry["auth_key"]), TailscaleControlURL: stringValue(entry["control_url"]), TailscaleHostname: stringValue(entry["hostname"])}
		return input, input.Tag != "" && input.TailscaleStateDirectory != ""
	case "openvpn-server":
		tls := mapValue(entry["tls"])
		users := firstMap(entry["users"])
		input := Input{Protocol: ProtocolOpenVPNServer, Tag: stringValue(entry["tag"]), Port: intValue(entry["listen_port"]), OpenVPNAddress: firstString(entry["address"]), OpenVPNUsername: stringValue(users["username"]), OpenVPNPassword: stringValue(users["password"]), OpenVPNServerCertificatePath: stringValue(tls["certificate_path"]), OpenVPNServerKeyPath: stringValue(tls["key_path"]), OpenVPNClientCAPath: stringValue(tls["client_certificate_path"])}
		return input, input.Tag != "" && input.Port > 0 && input.OpenVPNServerCertificatePath != ""
	default:
		return Input{}, false
	}
}
