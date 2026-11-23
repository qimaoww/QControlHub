package serverconfig

import "strings"

// managedXrayWireGuardInbound deliberately rejects settings that the preset
// cannot faithfully retain, including custom peer networks.
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
	if !ok || len(peers) == 0 {
		return Input{}, false
	}
	port, validPort := wireGuardConfigInteger(inbound["port"])
	mtu, validMTU := wireGuardConfigInteger(settings["mtu"])
	if !validPort || !validMTU {
		return Input{}, false
	}
	input := Input{
		Protocol: ProtocolWireGuard, Tag: stringValue(inbound["tag"]), Listen: stringValue(inbound["listen"]), Port: port, Transport: "raw",
		WireGuardServerPrivateKey: stringValue(settings["secretKey"]), WireGuardServerPublicKey: wireguardServerPublic(stringValue(settings["secretKey"])),
		WireGuardMTU: mtu,
	}
	for i, value := range peers {
		peer := mapValue(value)
		for key := range peer {
			if key != "publicKey" && key != "preSharedKey" && key != "allowedIPs" && key != "email" && key != "level" && key != "keepAlive" {
				return Input{}, false
			}
		}
		level, validLevel := wireGuardConfigInteger(peer["level"])
		keepalive, validKeepalive := wireGuardConfigInteger(peer["keepAlive"])
		if !validLevel || level != 0 || !validKeepalive || keepalive != 0 {
			return Input{}, false
		}
		for _, key := range []string{"preSharedKey", "email"} {
			if value, exists := peer[key]; exists {
				if _, ok := value.(string); !ok {
					return Input{}, false
				}
			}
		}
		rawAddresses, ok := peer["allowedIPs"].([]any)
		addresses := stringSliceValue(peer["allowedIPs"])
		if !ok || len(rawAddresses) != len(addresses) {
			return Input{}, false
		}
		client := WireGuardClient{PublicKey: stringValue(peer["publicKey"]), PresharedKey: stringValue(peer["preSharedKey"]), Email: stringValue(peer["email"]), Address: strings.Join(addresses, ",")}
		if i == 0 {
			input = input.withWireGuardClient(client)
		} else {
			input.WireGuardAdditionalClients = append(input.WireGuardAdditionalClients, client)
		}
	}
	if input.WireGuardMTU == 0 {
		input.WireGuardMTU = 1420
	}
	return input, validateWireGuardInput(input, false) == nil
}
