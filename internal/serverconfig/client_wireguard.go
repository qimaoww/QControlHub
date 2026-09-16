package serverconfig

import (
	"net"
	"strconv"
	"strings"
)

// Keep manual client parameters and native exports on the same defaults.
// Callers validate the input before preparing either representation.
func normalizeWireGuardClient(input *Input) {
	if input.WireGuardServerPublicKey == "" {
		input.WireGuardServerPublicKey = wireguardServerPublic(input.WireGuardServerPrivateKey)
	}
	addresses := splitWireGuardList(input.WireGuardClientAddress)
	routes := splitWireGuardList(input.WireGuardAllowedIPs)
	if len(routes) == 0 {
		for _, address := range addresses {
			ip, _, _ := net.ParseCIDR(address)
			if ip.To4() != nil {
				routes = append(routes, "0.0.0.0/0")
			} else {
				routes = append(routes, "::/0")
			}
		}
	}
	input.WireGuardClientAddress = strings.Join(addresses, ", ")
	input.WireGuardAllowedIPs = strings.Join(routes, ", ")
}

func wireguardClientFields(input Input) []ClientField {
	normalizeWireGuardClient(&input)
	fields := []ClientField{
		{Label: "客户端私钥", Value: input.WireGuardClientPrivateKey, Secret: true},
		{Label: "服务端公钥", Value: input.WireGuardServerPublicKey},
	}
	if input.WireGuardPresharedKey != "" {
		fields = append(fields, ClientField{Label: "预共享密钥（PSK）", Value: input.WireGuardPresharedKey, Secret: true})
	}
	return append(fields,
		ClientField{Label: "客户端地址", Value: input.WireGuardClientAddress},
		ClientField{Label: "AllowedIPs", Value: input.WireGuardAllowedIPs},
		ClientField{Label: "MTU", Value: strconv.Itoa(input.WireGuardMTU)},
		ClientField{Label: "PersistentKeepalive", Value: strconv.Itoa(input.WireGuardKeepalive)},
	)
}

func wireguardClientConfig(input Input, address string) (string, error) {
	if err := validateWireGuardInput(input, true); err != nil {
		return "", err
	}
	normalizeWireGuardClient(&input)
	endpoint := net.JoinHostPort(address, strconv.Itoa(input.Port))
	var b strings.Builder
	b.WriteString("[Interface]\nPrivateKey = " + input.WireGuardClientPrivateKey + "\nAddress = " + input.WireGuardClientAddress + "\nMTU = " + strconv.Itoa(input.WireGuardMTU) + "\n\n[Peer]\nPublicKey = " + input.WireGuardServerPublicKey + "\n")
	if input.WireGuardPresharedKey != "" {
		b.WriteString("PresharedKey = " + input.WireGuardPresharedKey + "\n")
	}
	b.WriteString("AllowedIPs = " + input.WireGuardAllowedIPs + "\nEndpoint = " + endpoint + "\n")
	if input.WireGuardKeepalive > 0 {
		b.WriteString("PersistentKeepalive = " + strconv.Itoa(input.WireGuardKeepalive) + "\n")
	}
	return b.String(), nil
}
