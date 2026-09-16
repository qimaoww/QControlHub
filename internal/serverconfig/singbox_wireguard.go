package serverconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

func generateSingBoxEndpoint(input Input) (string, error) {
	if err := validateWireGuardInput(input, true); err != nil {
		return "", err
	}
	if err := validateSingBoxWireGuardAddress(input); err != nil {
		return "", err
	}
	allowed := splitWireGuardList(input.WireGuardClientAddress)
	addresses := splitWireGuardList(input.WireGuardServerAddress)
	peer := map[string]any{"public_key": input.WireGuardClientPublicKey, "allowed_ips": allowed}
	if input.WireGuardPresharedKey != "" {
		peer["pre_shared_key"] = input.WireGuardPresharedKey
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

func parseSingBoxWireGuardEndpoint(entry map[string]any) (Input, bool) {
	if stringValue(entry["type"]) != "wireguard" {
		return Input{}, false
	}
	for key := range entry {
		if !containsSharedKey("type tag system mtu address private_key listen_port peers", key) {
			return Input{}, false
		}
	}
	if value, exists := entry["system"]; exists && value != false {
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
	for key := range peer {
		if !containsSharedKey("public_key pre_shared_key allowed_ips persistent_keepalive_interval", key) {
			return Input{}, false
		}
	}
	addresses, validAddresses := wireGuardConfigStrings(entry["address"])
	allowed, validAllowed := wireGuardConfigStrings(peer["allowed_ips"])
	port, validPort := wireGuardConfigInteger(entry["listen_port"])
	mtu, validMTU := wireGuardConfigInteger(entry["mtu"])
	keepalive, validKeepalive := wireGuardConfigInteger(peer["persistent_keepalive_interval"])
	if !validAddresses || !validAllowed || !validPort || !validMTU || !validKeepalive || keepalive != 0 {
		return Input{}, false
	}
	if value, exists := peer["pre_shared_key"]; exists {
		if _, ok := value.(string); !ok {
			return Input{}, false
		}
	}
	input := Input{
		Protocol: ProtocolWireGuard, Tag: stringValue(entry["tag"]), Port: port, Listen: "::", Transport: "raw", Username: "default",
		WireGuardServerPrivateKey: stringValue(entry["private_key"]), WireGuardServerPublicKey: wireguardServerPublic(stringValue(entry["private_key"])),
		WireGuardServerAddress: strings.Join(addresses, ","), WireGuardClientPublicKey: stringValue(peer["public_key"]),
		WireGuardClientAddress: strings.Join(allowed, ","), WireGuardPresharedKey: stringValue(peer["pre_shared_key"]),
		WireGuardMTU: mtu,
	}
	if input.WireGuardMTU == 0 {
		input.WireGuardMTU = 1408
	}
	return input, validateWireGuardInput(input, false) == nil && validateSingBoxWireGuardAddress(input) == nil
}

func parseSingBoxEndpointProtocol(entry map[string]any) (Input, bool) {
	switch stringValue(entry["type"]) {
	case "wireguard":
		return parseSingBoxWireGuardEndpoint(entry)
	default:
		return Input{}, false
	}
}

func validateSingBoxWireGuardAddress(input Input) error {
	// Native WireGuard exposes listen_port, not a listen-address selector.
	if ip := net.ParseIP(input.Listen); ip == nil || !ip.IsUnspecified() {
		return fmt.Errorf("sing-box WireGuard 端点监听所有接口，不支持自定义监听地址")
	}
	addresses := splitWireGuardList(input.WireGuardServerAddress)
	if len(addresses) == 0 || len(addresses) > 2 {
		return fmt.Errorf("WireGuard 服务端须填写一个 IPv4 地址和/或一个 IPv6 地址")
	}
	families := map[int]bool{}
	local := map[netip.Addr]bool{}
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address)
		if err != nil || !prefix.Addr().IsGlobalUnicast() || prefix.Addr().Is4In6() || families[prefix.Addr().BitLen()] {
			return fmt.Errorf("WireGuard 服务端地址无效: %s", address)
		}
		families[prefix.Addr().BitLen()] = true
		local[prefix.Addr()] = true
	}
	for _, address := range splitWireGuardList(input.WireGuardClientAddress) {
		prefix, err := netip.ParsePrefix(address)
		if err != nil || local[prefix.Addr()] || !families[prefix.Addr().BitLen()] {
			return fmt.Errorf("WireGuard 客户端地址须与服务端地址分离，且服务端须配置相应地址族")
		}
	}
	if families[128] && input.WireGuardMTU < 1280 {
		return fmt.Errorf("WireGuard IPv6 接口 MTU 至少需要 1280")
	}
	return nil
}
