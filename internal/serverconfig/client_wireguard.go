package serverconfig

import (
	"net"
	"strconv"
	"strings"
)

// Keep manual client parameters and Mihomo exports on the same defaults.
// Call after validation so address families and key material are known.
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

func buildWireGuardMihomoYAML(input Input, address, name string) (string, error) {
	// Mihomo applies explicit allowed-ips only inside peers. Its shorthand
	// installs a default route for every local address family.
	peer := map[string]any{
		"server": address, "port": input.Port, "public-key": input.WireGuardServerPublicKey,
		"allowed-ips": splitWireGuardList(input.WireGuardAllowedIPs),
	}
	if input.WireGuardPresharedKey != "" {
		peer["pre-shared-key"] = input.WireGuardPresharedKey
	}
	proxy := map[string]any{
		"name": name, "type": ProtocolWireGuard, "server": address, "port": input.Port,
		"private-key": input.WireGuardClientPrivateKey, "peers": []any{peer},
		"udp": true, "mtu": input.WireGuardMTU,
	}
	for _, cidr := range splitWireGuardList(input.WireGuardClientAddress) {
		ip, _, _ := strings.Cut(cidr, "/")
		if strings.Contains(ip, ":") {
			proxy["ipv6"] = ip
		} else {
			proxy["ip"] = ip
		}
	}
	if input.WireGuardKeepalive > 0 {
		proxy["persistent-keepalive"] = input.WireGuardKeepalive
	}
	return marshalSingleLineYAML(proxy)
}

// Keep the primary profile stable for existing subscriptions. Every device also
// gets an independently named export, including when another device lacks a private key.
func buildWireGuardClientProfiles(input Input, address, serverName, nodeName string) (ClientProfile, error) {
	if err := validateWireGuardInput(input, false); err != nil {
		return ClientProfile{}, err
	}
	var primary ClientProfile
	var variants []ClientVariant
	for i, client := range input.wireGuardClients() {
		name := strings.TrimSpace(nodeName)
		if i > 0 {
			if name == "" {
				name = input.Tag
			}
			deviceIP, _, _ := strings.Cut(splitWireGuardList(client.Address)[0], "/")
			name += " · " + deviceIP
		}
		profile, err := BuildClientProfileNamed(input.withWireGuardClient(client), address, serverName, name)
		if err != nil {
			if client.PrivateKey != "" {
				return ClientProfile{}, err
			}
			profile = ClientProfile{Format: "Mihomo WireGuard YAML", URIError: err.Error(), MihomoError: err.Error()}
		}
		if i == 0 {
			primary = profile
		}
		variants = append(variants, ClientVariant{Name: client.Address, Profile: profile})
	}
	primary.Variants = variants
	return primary, nil
}
