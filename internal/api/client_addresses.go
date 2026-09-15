package api

import (
	"net/netip"
	"sort"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

type clientAddressCandidate struct {
	address     string
	source      string
	family      string
	profileOnly bool
}

func normalizeClientAddressMode(value string) string {
	if value == core.SubStoreAddressModeIPv4 || value == core.SubStoreAddressModeIPv6 {
		return value
	}
	return core.SubStoreAddressModeAuto
}

// clientProfileAddress resolves the automatic address used by one listening
// endpoint. A requested family without a matching candidate falls back to the
// first available candidate so a profile never renders an empty address.
func clientProfileAddress(candidates []clientAddressCandidate, mode string) clientAddressCandidate {
	if mode == core.SubStoreAddressModeIPv4 || mode == core.SubStoreAddressModeIPv6 {
		for _, candidate := range candidates {
			if candidate.family == mode {
				return candidate
			}
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return clientAddressCandidate{}
}

// buildClientAccessProfiles renders the effective profile of every listening
// endpoint. Only profile-scoped labels provide a display override: node-wide
// client display values are deliberately not inherited, so an upgraded node
// never has to clear the same legacy value on every port. The node-wide
// client address still participates as an automatic candidate.
func buildClientAccessProfiles(engine core.Engine, inputs []serverconfig.Input, candidates []clientAddressCandidate, serverName string, labels map[string]string) []clientAccessProfile {
	profiles := make([]clientAccessProfile, 0, len(inputs))
	for _, input := range inputs {
		mode := core.SubStoreAddressModeAuto
		if value, exists := labels[core.ClientProfileFamilyLabel(engine, input.Listen, input.Port)]; exists {
			mode = normalizeClientAddressMode(value)
		}
		address, overridden := strings.TrimSpace(labels[core.ClientProfileAddressLabel(engine, input.Listen, input.Port)]), false
		if address != "" {
			overridden = true
		} else {
			address = clientProfileAddress(candidates, mode).address
		}
		name, nameOverridden := "", false
		if value, exists := labels[core.ClientProfileNameLabel(engine, input.Listen, input.Port)]; exists {
			name, nameOverridden = value, true
		}
		profile, err := serverconfig.BuildClientProfileNamed(input, address, serverName, name)
		if err != nil {
			continue
		}
		protocol, found := serverconfig.FindProtocol(engine, input.Protocol)
		if !found {
			continue
		}
		profiles = append(profiles, clientAccessProfile{
			Tag: input.Tag, Protocol: protocol.Name, Port: input.Port, Profile: profile,
			ClientName: name, NameOverridden: nameOverridden,
			Address: address, AddressMode: mode, AddressOverridden: overridden,
		})
	}
	return profiles
}

func buildClientAccessAddressOptions(engine core.Engine, inputs []serverconfig.Input, candidates []clientAddressCandidate, serverName string, labelSets ...map[string]string) []clientAccessAddressOption {
	// A per-profile manual hostname may differ from every host-wide address.
	// Publish it only for that profile; do not use it as another port's
	// automatic candidate or discard the entire entry when all ports override.
	candidates = append([]clientAddressCandidate(nil), candidates...)
	if len(labelSets) > 0 {
		for _, input := range inputs {
			address := strings.TrimSpace(labelSets[0][core.ClientProfileAddressLabel(engine, input.Listen, input.Port)])
			found := address == ""
			for _, candidate := range candidates {
				found = found || candidate.address == address
			}
			if !found {
				candidates = append(candidates, clientAddressCandidate{address: address,
					source: "入站手动设置", family: clientAddressFamily(address), profileOnly: true})
			}
		}
	}
	options := make([]clientAccessAddressOption, 0, len(candidates))
	for _, candidate := range candidates {
		profiles := make([]clientAccessProfile, 0, len(inputs))
		for _, input := range inputs {
			if len(labelSets) > 0 {
				// A port with a manual address is pinned to exactly one candidate;
				// exposing it under another family would publish a second URI.
				if value := strings.TrimSpace(labelSets[0][core.ClientProfileAddressLabel(engine, input.Listen, input.Port)]); (value != "" && value != candidate.address) || (candidate.profileOnly && value == "") {
					continue
				}
			}
			name, overridden := "", false
			if len(labelSets) > 0 {
				if value, exists := labelSets[0][core.ClientProfileNameLabel(engine, input.Listen, input.Port)]; exists {
					name, overridden = value, true
				}
			}
			profile, err := serverconfig.BuildClientProfileNamed(input, candidate.address, serverName, name)
			if err != nil {
				continue
			}
			protocol, found := serverconfig.FindProtocol(engine, input.Protocol)
			if !found {
				continue
			}
			profiles = append(profiles, clientAccessProfile{Tag: input.Tag, Protocol: protocol.Name, Port: input.Port, Profile: profile, ClientName: name, NameOverridden: overridden})
		}
		if len(profiles) > 0 {
			options = append(options, clientAccessAddressOption{
				Address: candidate.address, Source: candidate.source, Family: candidate.family, Profiles: profiles,
			})
		}
	}
	return options
}

func clientAddressFamily(address string) string {
	address = strings.TrimSpace(address)
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		address = strings.TrimSuffix(strings.TrimPrefix(address, "["), "]")
	}
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return "hostname"
	}
	if parsed.Unmap().Is4() {
		return core.SubStoreAddressModeIPv4
	}
	return core.SubStoreAddressModeIPv6
}

func clientAddressCandidates(agent core.Agent) []clientAddressCandidate {
	result := make([]clientAddressCandidate, 0, 8)
	seen := make(map[string]struct{})
	labelSources := []struct {
		key    string
		source string
	}{
		{key: "client_address", source: "手动设置"},
		{key: "public_host", source: "节点公网域名"},
		{key: "public_ip", source: "节点公网 IP"},
	}
	for _, item := range labelSources {
		key := item.key
		if value := strings.TrimSpace(agent.Labels[key]); value != "" {
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				result = append(result, clientAddressCandidate{address: value, source: item.source, family: clientAddressFamily(value)})
			}
		}
	}
	// The Agent probes each family outbound, so both routable egress addresses
	// are known even when the control-plane connection itself used only one.
	for _, probed := range []struct {
		value      string
		provenance string
		wantIPv4   bool
	}{
		{agent.Metrics.PublicIPv4, agent.Metrics.PublicIPv4Source, true},
		{agent.Metrics.PublicIPv6, agent.Metrics.PublicIPv6Source, false},
	} {
		if probed.provenance != "" && probed.provenance != core.PublicIPProbeSourceAgent && probed.provenance != core.PublicIPProbeSourceControlPlane {
			continue
		}
		address := authn.NormalizePublicIP(probed.value)
		parsed, parseErr := netip.ParseAddr(address)
		if parseErr != nil || address == "" || parsed.Is4() != probed.wantIPv4 || netpolicy.IsCloudflareAddress(parsed) {
			continue
		}
		if _, exists := seen[address]; !exists {
			seen[address] = struct{}{}
			family := "IPv6"
			if probed.wantIPv4 {
				family = "IPv4"
			}
			source := "Agent 本地直连探测 · " + family
			if probed.provenance == core.PublicIPProbeSourceControlPlane {
				source = "控制面配置的 Agent 直连探测 · " + family
			}
			result = append(result, clientAddressCandidate{address: address, source: source, family: clientAddressFamily(address)})
		}
	}
	// Default-route interface addresses are the next fallback per family. Only
	// actual globally routable unicast addresses are accepted; private, CGNAT,
	// documentation, reserved, link-local, zoned and invalid values are dropped
	// so a non-routable interface can never be surfaced as a node address.
	for _, item := range publicInterfaceAddresses(agent.Metrics.NetworkInterfaces) {
		if _, exists := seen[item.address]; !exists {
			seen[item.address] = struct{}{}
			result = append(result, clientAddressCandidate{address: item.address, source: "Agent 默认路由接口 " + item.name, family: clientAddressFamily(item.address)})
		}
	}
	// The WSS observation is now only populated when the proxy chain resolved
	// unambiguously, so it is a strictly verified fallback; an ambiguous chain
	// clears the value on reconnect, never surfacing a relay as the node.
	if address := authn.NormalizePublicIP(agent.Metrics.ObservedPublicIP); address != "" {
		parsed, parseErr := netip.ParseAddr(address)
		if parseErr == nil && !netpolicy.IsCloudflareAddress(parsed) {
			family := "IPv4"
			if strings.Contains(address, ":") {
				family = "IPv6"
			}
			if _, exists := seen[address]; !exists {
				seen[address] = struct{}{}
				result = append(result, clientAddressCandidate{address: address, source: "已验证连接来源 · " + family, family: clientAddressFamily(address)})
			}
		}
	}
	return result
}

type publicInterfaceAddress struct {
	address string
	name    string
	family  int
}

// publicInterfaceAddresses returns the globally routable unicast addresses of
// the reported default-route interfaces, IPv4 before IPv6, de-duplicated and
// sorted. authn.NormalizePublicIP reuses the IANA special-purpose denylist so
// private, CGNAT, documentation, reserved, link-local and invalid values never
// become node address candidates.
func publicInterfaceAddresses(interfaces []core.HostNetworkInterface) []publicInterfaceAddress {
	addresses := make([]publicInterfaceAddress, 0, 8)
	for _, networkInterface := range interfaces {
		for _, raw := range networkInterface.Addresses {
			normalized := authn.NormalizePublicIP(raw)
			if normalized == "" {
				continue
			}
			parsed, err := netip.ParseAddr(normalized)
			if err != nil || netpolicy.IsCloudflareAddress(parsed) {
				continue
			}
			family := 1
			if !strings.Contains(normalized, ":") {
				family = 0
			}
			addresses = append(addresses, publicInterfaceAddress{address: normalized, name: networkInterface.Name, family: family})
		}
	}
	sort.SliceStable(addresses, func(i, j int) bool {
		if addresses[i].family != addresses[j].family {
			return addresses[i].family < addresses[j].family
		}
		if addresses[i].name != addresses[j].name {
			return addresses[i].name < addresses[j].name
		}
		return addresses[i].address < addresses[j].address
	})
	deduped := addresses[:0]
	seen := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		if _, exists := seen[address.address]; exists {
			continue
		}
		seen[address.address] = struct{}{}
		deduped = append(deduped, address)
	}
	return deduped
}

func firstLabel(agent core.Agent, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(agent.Labels[key]); value != "" {
			return value
		}
	}
	return ""
}
