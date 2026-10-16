package api

import (
	"net/url"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestManagedTLSClientDomainDefaultsAndOverrides(t *testing.T) {
	protocol, _ := serverconfig.FindProtocol(core.EngineSingBox, "vless-ws-tls")
	one, _ := serverconfig.NewPlan(protocol)
	one.TLSServerName = "one.example.test"
	one.Port = 23001
	two, _ := serverconfig.NewPlan(protocol)
	two.TLSServerName = "two.example.test"
	two.Port = 23002
	inputs := []serverconfig.Input{one, two}
	candidates := []clientAddressCandidate{{address: "198.51.100.1", family: core.SubStoreAddressModeIPv4}}
	labels := map[string]string{}
	check := func(wantOne, wantTwo string) {
		t.Helper()
		profiles := buildClientAccessProfiles(core.EngineSingBox, inputs, candidates, "", labels)
		if len(profiles) != 2 {
			t.Fatalf("missing profiles: %+v", profiles)
		}
		for index, want := range []string{wantOne, wantTwo} {
			uri, err := url.Parse(profiles[index].Profile.URI)
			if err != nil || profiles[index].Address != want || uri.Hostname() != want || uri.Port() != "443" || uri.Query().Get("sni") != inputs[index].TLSServerName || uri.Query().Get("path") != inputs[index].TransportPath {
				t.Fatalf("wrong managed endpoint: %+v", profiles[index])
			}
		}
	}
	check(one.TLSServerName, two.TLSServerName)
	labels["client_address"] = candidates[0].address
	check(candidates[0].address, candidates[0].address)
	delete(labels, "client_address")
	// A manual address may reuse a sibling's domain while keeping its own SNI.
	labels[core.ClientProfileAddressLabel(core.EngineSingBox, two.Listen, two.Port)] = one.TLSServerName
	for _, option := range buildClientAccessAddressOptions(core.EngineSingBox, inputs, candidates, "", labels) {
		if option.Address == one.TLSServerName && len(option.Profiles) != 2 {
			t.Fatal("manual override to another inbound's domain was discarded")
		}
	}
	delete(labels, core.ClientProfileAddressLabel(core.EngineSingBox, two.Listen, two.Port))
	labels[core.ClientProfileAddressLabel(core.EngineSingBox, one.Listen, one.Port)] = "cdn.example.test"
	check("cdn.example.test", two.TLSServerName)
	delete(labels, core.ClientProfileAddressLabel(core.EngineSingBox, one.Listen, one.Port))
	labels[core.ClientProfileFamilyLabel(core.EngineSingBox, one.Listen, one.Port)] = core.SubStoreAddressModeIPv4
	check("198.51.100.1", two.TLSServerName)
	options := buildClientAccessAddressOptions(core.EngineSingBox, inputs, candidates, "", labels)
	for _, option := range options {
		if option.Family == "hostname" && (len(option.Profiles) != 1 || option.Profiles[0].Tag != map[string]string{one.TLSServerName: one.Tag, two.TLSServerName: two.Tag}[option.Address]) {
			t.Fatalf("one inbound's domain leaked to another: %+v", option)
		}
	}
	// A configured managed domain is sufficient even before public-IP probing.
	profiles := buildClientAccessProfiles(core.EngineSingBox, inputs, nil, "", nil)
	if len(profiles) != 2 || len(buildClientAccessAddressOptions(core.EngineSingBox, inputs, nil, "")) != 2 {
		t.Fatal("domain-only deployment still requires a node client address")
	}
	one.TLSMode = "native"
	profiles = buildClientAccessProfiles(core.EngineSingBox, []serverconfig.Input{one}, candidates, "", nil)
	if len(profiles) != 1 || profiles[0].Address != candidates[0].address {
		t.Fatal("native TLS address selection changed")
	}
}
