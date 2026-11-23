package serverconfig

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const defaultWireGuardServerAddress = "10.66.66.1/24"

// AddWireGuardClient extends an unsaved preset without rotating its listener or peers.
func AddWireGuardClient(engine core.Engine, input Input) (Input, error) {
	if err := validateWireGuardInput(input, false); err != nil {
		return Input{}, fmt.Errorf("%w: %v", ErrInvalidPlanInput, err)
	}
	if engine == core.EngineSingBox {
		if err := validateSingBoxWireGuardAddress(input); err != nil {
			return Input{}, fmt.Errorf("%w: %v", ErrInvalidPlanInput, err)
		}
	}
	used := map[netip.Addr]bool{}
	networks := map[int]netip.Prefix{}
	serverAddress := input.WireGuardServerAddress
	if engine == core.EngineXray && serverAddress == "" {
		// Xray does not serialize the preset interface address in its inbound.
		serverAddress = defaultWireGuardServerAddress
	}
	for _, cidr := range splitWireGuardList(serverAddress) {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return Input{}, fmt.Errorf("%w: WireGuard 服务端地址无效", ErrInvalidPlanInput)
		}
		networks[prefix.Addr().BitLen()] = prefix
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
		pool := networks[primary.Addr().BitLen()]
		// A sing-box interface subnet can supply new hosts even when its
		// original peer uses a separately routed host address. A host-only
		// interface or custom Xray peer has no declared client subnet.
		if !pool.Contains(primary.Addr()) && (engine != core.EngineSingBox || pool.IsSingleIP()) {
			pool = netip.Prefix{}
		}
		allocated := nextWireGuardClientAddress(primary.Addr(), pool, used)
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

func nextWireGuardClientAddress(primary netip.Addr, network netip.Prefix, used map[netip.Addr]bool) netip.Addr {
	pool := network.Masked()
	next := primary.Next()
	if pool.IsValid() && !pool.Contains(primary) {
		next = network.Addr().Next()
	}
	// At most len(used) assigned addresses and two reserved ends can
	// precede an available host, even when wrapping within the subnet.
	for i := 0; i <= len(used)+2; i++ {
		if pool.IsValid() && !pool.Contains(next) {
			next = pool.Addr()
		}
		reserved := pool.IsValid() && pool.Bits() < next.BitLen()-1 &&
			(next == pool.Addr() || next.Is4() && !pool.Contains(next.Next()))
		if next.IsGlobalUnicast() && !used[next] && !reserved {
			return next
		}
		next = next.Next()
	}
	return netip.Addr{}
}
