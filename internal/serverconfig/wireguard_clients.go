package serverconfig

import (
	"fmt"
	"net/netip"
	"strings"
)

// AddWireGuardClient extends an unsaved preset without rotating its listener or peers.
func AddWireGuardClient(input Input) (Input, error) {
	if err := validateWireGuardInput(input, false); err != nil {
		return Input{}, fmt.Errorf("%w: %v", ErrInvalidPlanInput, err)
	}
	used := map[netip.Addr]bool{}
	var networks []netip.Prefix
	for _, cidr := range splitWireGuardList(input.WireGuardServerAddress) {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return Input{}, fmt.Errorf("%w: WireGuard 服务端地址无效", ErrInvalidPlanInput)
		}
		networks = append(networks, prefix)
		used[prefix.Addr()] = true
	}
	for _, client := range input.wireGuardClients() {
		for _, cidr := range splitWireGuardList(client.Address) {
			prefix, _ := netip.ParsePrefix(cidr) // validated above
			used[prefix.Addr()] = true
		}
	}
	var addresses []string
	for _, cidr := range splitWireGuardList(input.WireGuardClientAddress) {
		primary, _ := netip.ParsePrefix(cidr)
		var pool netip.Prefix
		for _, network := range networks {
			if network.Contains(primary.Addr()) {
				pool = network.Masked()
				break
			}
		}
		next := primary.Addr().Next()
		// Prefer the next host, wrapping once within a configured subnet.
		var allocated netip.Addr
		for i := 0; i <= len(used)+2; i++ {
			if pool.IsValid() && !pool.Contains(next) {
				next = pool.Addr()
			}
			reserved := pool.IsValid() && next.Is4() && pool.Bits() < 31 && (next == pool.Addr() || !pool.Contains(next.Next()))
			if next.IsGlobalUnicast() && !used[next] && !reserved {
				allocated = next
				break
			}
			next = next.Next()
		}
		if !allocated.IsValid() {
			return Input{}, fmt.Errorf("%w: WireGuard 地址池没有可用客户端地址", ErrInvalidPlanInput)
		}
		addresses = append(addresses, netip.PrefixFrom(allocated, allocated.BitLen()).String())
	}
	private, public, err := newWireGuardKeyPair()
	if err != nil {
		return Input{}, err
	}
	psk, err := newWireGuardSecret()
	if err != nil {
		return Input{}, err
	}
	input.WireGuardAdditionalClients = append(input.WireGuardAdditionalClients, WireGuardClient{Address: strings.Join(addresses, ","), PrivateKey: private, PublicKey: public, PresharedKey: psk})
	return input, nil
}
