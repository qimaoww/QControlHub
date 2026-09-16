package serverconfig

import (
	"net"
	"strconv"
	"strings"
)

func wireguardClientConfig(input Input, address string) (string, error) {
	if err := validateWireGuardInput(input, true); err != nil {
		return "", err
	}
	endpoint := net.JoinHostPort(address, strconv.Itoa(input.Port))
	serverPub := input.WireGuardServerPublicKey
	if serverPub == "" {
		serverPub = wireguardServerPublic(input.WireGuardServerPrivateKey)
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
	var b strings.Builder
	b.WriteString("[Interface]\nPrivateKey = " + input.WireGuardClientPrivateKey + "\nAddress = " + strings.Join(addresses, ", ") + "\nMTU = " + strconv.Itoa(input.WireGuardMTU) + "\n\n[Peer]\nPublicKey = " + serverPub + "\n")
	if input.WireGuardPresharedKey != "" {
		b.WriteString("PresharedKey = " + input.WireGuardPresharedKey + "\n")
	}
	b.WriteString("AllowedIPs = " + strings.Join(routes, ", ") + "\nEndpoint = " + endpoint + "\n")
	if input.WireGuardKeepalive > 0 {
		b.WriteString("PersistentKeepalive = " + strconv.Itoa(input.WireGuardKeepalive) + "\n")
	}
	return b.String(), nil
}
