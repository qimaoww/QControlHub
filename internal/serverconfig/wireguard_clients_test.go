package serverconfig

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestWireGuardDeviceProfiles(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			protocol, _ := FindProtocol(engine, ProtocolWireGuard)
			first, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			first.ClientAddress, first.ClientPort = "relay.example.test", 40000
			first.WireGuardServerAddress += ",fd00::1/120"
			first.WireGuardClientAddress += ",fd00::2/128"
			first.WireGuardAllowedIPs = ""
			multi, err := AddWireGuardClient(engine, first)
			if err != nil {
				t.Fatal(err)
			}
			peer := multi.WireGuardAdditionalClients[0]
			if peer.Address != "10.66.66.3/32,fd00::3/128" {
				t.Fatal(peer.Address)
			}
			before, err := BuildClientProfile(first, "origin.example.test", "")
			if err != nil {
				t.Fatal(err)
			}
			source, err := Generate(engine, multi)
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := MarshalClientMetadata(multi)
			if err != nil {
				t.Fatal(err)
			}
			diagnostic, err := json.Marshal(core.RedactDiagnosticValue(map[string]any{"metadata": metadata, "content": source}))
			if err != nil || strings.Contains(string(diagnostic), peer.PrivateKey) || strings.Contains(string(diagnostic), peer.PresharedKey) {
				t.Fatal("device secrets exposed through diagnostics", err)
			}
			inputs := ParseAll(engine, source)
			if len(inputs) != 1 || !CanEditNativePreset(engine, inputs[0]) {
				t.Fatal("multi-peer native config is not editable")
			}
			if err := ApplyClientMetadata(&inputs[0], metadata); err != nil {
				t.Fatal(err)
			}
			again, err := Generate(engine, inputs[0])
			if err != nil || source != again {
				t.Fatal("native multi-peer round trip", err)
			}
			after, err := BuildClientProfile(inputs[0], "origin.example.test", "")
			if err != nil || len(after.Variants) != 2 || after.URI != before.URI || after.Variants[0].Profile.URI != before.URI {
				t.Fatal("primary export changed", err)
			}
			second := after.Variants[1].Profile.URI
			if !strings.Contains(second, peer.PrivateKey) || strings.Contains(second, first.WireGuardClientPrivateKey) || !strings.Contains(second, "fd00::3") || strings.Count(second, first.ClientAddress) != 2 {
				t.Fatal("incorrect device export")
			}

			// Reordering native peers must retain the association with each private key.
			var doc map[string]any
			if err := json.Unmarshal([]byte(source), &doc); err != nil {
				t.Fatal(err)
			}
			var settings map[string]any
			if engine == core.EngineXray {
				settings = mapValue(mapValue(doc["inbounds"].([]any)[0])["settings"])
			} else {
				settings = mapValue(doc["endpoints"].([]any)[0])
			}
			peers := settings["peers"].([]any)
			peers[0], peers[1] = peers[1], peers[0]
			raw, _ := json.Marshal(doc)
			reordered := ParseAll(engine, string(raw))
			if len(reordered) != 1 {
				t.Fatal("reordered peers not recognized")
			}
			if err := ApplyClientMetadata(&reordered[0], metadata); err != nil || reordered[0].WireGuardClientPrivateKey != peer.PrivateKey || reordered[0].WireGuardAdditionalClients[0].PrivateKey != first.WireGuardClientPrivateKey || reordered[0].ClientPort != first.ClientPort {
				t.Fatal("metadata attached to wrong peer", err)
			}

			for _, duplicate := range []WireGuardClient{
				{Address: peer.Address, PublicKey: first.WireGuardClientPublicKey, PrivateKey: first.WireGuardClientPrivateKey},
				{Address: "fd00:0:0:0:0:0:0:2/128", PublicKey: peer.PublicKey, PrivateKey: peer.PrivateKey},
			} {
				invalid := multi
				invalid.WireGuardAdditionalClients = []WireGuardClient{duplicate}
				if _, err := Generate(engine, invalid); err == nil {
					t.Fatal("duplicate peer identity accepted")
				}
			}
			multi.WireGuardClientPrivateKey = ""
			partial, err := BuildClientProfile(multi, "origin.example.test", "")
			if err != nil || partial.URI != "" || partial.Variants[1].Profile.URI != second {
				t.Fatal("a native peer without a private key blocked the managed client", err)
			}
			multi.WireGuardAdditionalClients[0].PrivateKey = ""
			if _, err := GenerateNativePresetEdit(engine, multi, first); err == nil {
				t.Fatal("new peer with missing private key accepted")
			}
		})
	}
}

func TestWireGuardClientAddressAllocation(t *testing.T) {
	protocol, _ := FindProtocol(core.EngineSingBox, ProtocolWireGuard)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.WireGuardClientAddress = "10.66.66.254/32"
	next, err := AddWireGuardClient(core.EngineSingBox, input)
	if err != nil || next.WireGuardAdditionalClients[0].Address != "10.66.66.2/32" {
		t.Fatal("address allocation did not wrap within subnet", err)
	}
	input.WireGuardClientAddress = "10.66.66.2/32"
	input.WireGuardServerAddress = "10.66.66.1/30"
	if _, err := AddWireGuardClient(core.EngineSingBox, input); !errors.Is(err, ErrInvalidPlanInput) {
		t.Fatal("allocated a reserved or occupied address", err)
	}
}

func TestWireGuardAutofilledClients(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		engine                         core.Engine
		server, primary, second, third string
	}{
		{"reopened Xray preset", core.EngineXray, "", "10.66.66.254/32", "10.66.66.2/32", "10.66.66.3/32"},
		{"custom Xray subnet", core.EngineXray, "", "10.77.0.8/32", "10.77.0.9/32", "10.77.0.10/32"},
		{"routed primary", core.EngineSingBox, "10.7.0.1/24", "10.8.0.2/32", "10.7.0.2/32", "10.7.0.3/32"},
		{"host-only interface", core.EngineSingBox, "10.7.0.3/32", "10.7.0.2/32", "10.7.0.4/32", "10.7.0.5/32"},
		{"dual stack wrap", core.EngineSingBox, "10.7.0.1/29,fd00::1/125", "10.7.0.6/32,fd00::7/128", "10.7.0.2/32,fd00::2/128", "10.7.0.3/32,fd00::3/128"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			protocol, _ := FindProtocol(tc.engine, ProtocolWireGuard)
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.WireGuardServerAddress, input.WireGuardClientAddress = tc.server, tc.primary
			keys := map[string]bool{input.WireGuardClientPublicKey: true}
			secrets := map[string]bool{input.WireGuardPresharedKey: true}
			for _, want := range []string{tc.second, tc.third} {
				input, err = AddWireGuardClient(tc.engine, input)
				if err != nil {
					t.Fatal(err)
				}
				client := input.WireGuardAdditionalClients[len(input.WireGuardAdditionalClients)-1]
				if client.Address != want || client.PrivateKey == "" || client.PublicKey != wireguardServerPublic(client.PrivateKey) || keys[client.PublicKey] || client.PresharedKey == "" || secrets[client.PresharedKey] {
					t.Fatalf("invalid generated credentials or tunnel address, got %s, want %s", client.Address, want)
				}
				keys[client.PublicKey], secrets[client.PresharedKey] = true, true
				if _, err := Generate(tc.engine, input); err != nil {
					t.Fatalf("autofilled plan cannot be saved: %v", err)
				}
			}
		})
	}
}
