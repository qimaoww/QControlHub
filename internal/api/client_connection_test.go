package api

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestPresetClientAddressOptionsMatchActualExports(t *testing.T) {
	protocol, _ := serverconfig.FindProtocol(core.EngineXray, serverconfig.ProtocolSS2022)
	input, err := serverconfig.NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.ClientAddress = "preset.example.test"
	inputs := []serverconfig.Input{input}
	candidates := []clientAddressCandidate{{address: "192.0.2.1", family: "ipv4"}, {address: "2001:db8::1", family: "ipv6"}}
	for _, manual := range []string{"", "profile.example.test"} {
		labels := map[string]string{}
		expected := input.ClientAddress
		if manual != "" {
			labels[core.ClientProfileAddressLabel(core.EngineXray, input.Listen, input.Port)] = manual
			expected = manual
		}
		profile, err := buildClientAccessProfile(core.EngineXray, input, candidates, "", labels)
		if err != nil || profile.Address != expected || !strings.Contains(profile.Profile.URI, expected) {
			t.Fatalf("display and profile diverged: %+v %v", profile, err)
		}
		options := buildClientAccessAddressOptions(core.EngineXray, inputs, candidates, "", labels)
		if len(options) != 1 || options[0].Address != expected || len(options[0].Profiles) != 1 || !strings.Contains(options[0].Profiles[0].Profile.URI, expected) {
			t.Fatalf("address options diverged: %+v", options)
		}
	}
	if input.ClientAddress != "preset.example.test" {
		t.Fatal("export modified stored inbound")
	}
}
