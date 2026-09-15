package serverconfig

import "strings"

// managedXrayWireGuardInbound deliberately rejects settings that the preset
// cannot faithfully retain, including additional peers and custom networks.
func managedXrayWireGuardInbound(inbound map[string]any) bool {
	_, ok := parseXrayWireGuardInbound(inbound)
	return ok
}

func parseXrayWireGuardInbound(inbound map[string]any) (Input, bool) {
	if stringValue(inbound["protocol"]) != ProtocolWireGuard || inbound["streamSettings"] != nil {
		return Input{}, false
	}
	settings := mapValue(inbound["settings"])
	for key := range settings {
		if key != "secretKey" && key != "peers" && key != "mtu" {
			return Input{}, false
		}
	}
	peers, ok := settings["peers"].([]any)
	if !ok || len(peers) != 1 {
		return Input{}, false
	}
	peer := mapValue(peers[0])
	for key := range peer {
		if key != "publicKey" && key != "preSharedKey" && key != "allowedIPs" && key != "email" && key != "level" && key != "keepAlive" {
			return Input{}, false
		}
	}
	if intValue(peer["level"]) != 0 || intValue(peer["keepAlive"]) != 0 {
		return Input{}, false
	}
	rawAddresses, ok := peer["allowedIPs"].([]any)
	addresses := stringSliceValue(peer["allowedIPs"])
	if !ok || len(rawAddresses) != len(addresses) {
		return Input{}, false
	}
	input := Input{
		Protocol: ProtocolWireGuard, Tag: stringValue(inbound["tag"]), Listen: stringValue(inbound["listen"]), Port: intValue(inbound["port"]), Transport: "raw", Username: stringValue(peer["email"]),
		WireGuardServerPrivateKey: stringValue(settings["secretKey"]), WireGuardServerPublicKey: wireguardServerPublic(stringValue(settings["secretKey"])),
		WireGuardClientPublicKey: stringValue(peer["publicKey"]), WireGuardPresharedKey: stringValue(peer["preSharedKey"]), WireGuardClientAddress: strings.Join(addresses, ","), WireGuardMTU: intValue(settings["mtu"]),
	}
	if input.WireGuardMTU == 0 {
		input.WireGuardMTU = 1420
	}
	return input, validateWireGuardInput(input, false) == nil
}
