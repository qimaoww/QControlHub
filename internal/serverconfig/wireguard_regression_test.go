package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestWireGuardMetadataKeepsSourceAuthoritative(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			protocol, _ := FindProtocol(engine, ProtocolWireGuard)
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.WireGuardKeepalive = 0
			input.WireGuardAllowedIPs = "192.0.2.0/24"
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := MarshalClientMetadata(input)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{input.WireGuardServerPrivateKey, input.WireGuardPresharedKey} {
				if strings.Contains(metadata, secret) {
					t.Fatal("server-owned material copied into client metadata")
				}
			}
			parsed, ok := Parse(engine, content)
			if !ok {
				t.Fatal("generated server config did not parse")
			}
			parsed.WireGuardKeepalive = 25
			if err := ApplyClientMetadata(&parsed, metadata); err != nil {
				t.Fatal(err)
			}
			if parsed.WireGuardKeepalive != 0 || parsed.WireGuardClientPrivateKey != input.WireGuardClientPrivateKey ||
				parsed.WireGuardPresharedKey != input.WireGuardPresharedKey || parsed.WireGuardClientAddress != input.WireGuardClientAddress {
				t.Fatal("client-only metadata did not round trip")
			}
			raw, err := json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			if string(payload["wireguard_keepalive"]) != "0" {
				t.Fatal("disabled keepalive must remain an explicit zero in the API JSON")
			}
			profile, err := BuildClientProfile(parsed, "2001:db8::1", "")
			if err != nil || !strings.Contains(profile.URI, "Endpoint = [2001:db8::1]:") ||
				strings.Contains(profile.URI, "PersistentKeepalive") || strings.Contains(profile.URI, input.WireGuardServerPrivateKey) {
				t.Fatalf("native client export failed: %v", err)
			}
			replacement, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			parsed.WireGuardClientPublicKey = replacement.WireGuardClientPublicKey
			parsed.WireGuardClientPrivateKey = ""
			if err := ApplyClientMetadata(&parsed, metadata); err != nil || parsed.WireGuardClientPrivateKey != "" {
				t.Fatal("stale metadata hydrated a rotated peer")
			}
			if _, err := BuildClientProfile(parsed, "vpn.example.test", ""); err == nil {
				t.Fatal("rotated peer exported with a stale client private key")
			}
		})
	}
}

func TestWireGuardDoesNotClaimPublicSourceGeoBlocking(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			protocol, _ := FindProtocol(engine, ProtocolWireGuard)
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			policy := MainlandAccessPolicy{Engine: engine, Tag: input.Tag, Port: input.Port, Kind: "http", BlockMainlandSource: true}
			if _, err := ApplyMainlandAccessPolicyWithPrefixes(engine, content, policy, nil); err == nil {
				t.Fatal("a caller-controlled kind bypassed the WireGuard source-policy guard")
			}
			input.BlockMainlandSource = true
			if _, err := Generate(engine, input); err == nil {
				t.Fatal("generated ineffective public source blocking for tunneled traffic")
			}
			policy.BlockMainlandSource, policy.BlockMainlandDestination = false, true
			if _, err := ApplyMainlandAccessPolicyWithPrefixes(engine, content, policy, nil); err != nil {
				t.Fatalf("destination blocking must remain available: %v", err)
			}
		})
	}
}

func TestSingBoxWireGuardRejectsInvalidServerAndListenerAddresses(t *testing.T) {
	input, _ := wireGuardEndpointFixture(t)
	for _, address := range []string{"", "224.0.0.1/24", "10.66.66.2/24", "10.0.0.1/24,10.1.0.1/24", "::ffff:10.0.0.1/120", "fd00::1/64"} {
		t.Run(address, func(t *testing.T) {
			input := input
			input.WireGuardServerAddress = address
			if _, err := Generate(core.EngineSingBox, input); err == nil {
				t.Fatal("invalid server/client address pair accepted")
			}
		})
	}
	input.Listen = "127.0.0.1"
	if _, err := Generate(core.EngineSingBox, input); err == nil {
		t.Fatal("endpoint silently ignored a loopback-only listen address")
	}
}

func TestDiscoverSingBoxEntriesRejectsAmbiguousIdentity(t *testing.T) {
	for _, content := range []string{
		`{"endpoints":[{"tag":"wg","type":"wireguard","listen_port":51820.5}]}`,
		`{"endpoints":[{"tag":"wg","type":"wireguard","listen_port":"51820"}]}`,
		`{"endpoints":[{"tag":"wg","type":"wireguard","listen_port":-1}]}`,
		`{"endpoints":[{"tag":"wg","type":"wireguard","listen_port":65536}]}`,
		`{"endpoints":[{"tag":"wg","type":"wireguard","listen_port":51820,"Listen_Port":51821}]}`,
		`{"endpoints":[],"endpoints":[]}`,
		`{"endpoints":[null]}`,
		`{"endpoints":null}`,
		`{"inbounds":null}`,
		`{"endpoints":[{"tag":" wg","type":"wireguard"}]}`,
		`{"endpoints":[{"tag":"wg","type":"wireguard "}]}`,
	} {
		if _, err := DiscoverSingBoxEntries(content); err == nil {
			t.Fatalf("ambiguous identity accepted: %s", content)
		}
	}
}

func TestXrayWireGuardParserRejectsLossyNumericFields(t *testing.T) {
	protocol, _ := FindProtocol(core.EngineXray, ProtocolWireGuard)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	content, err := Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"level", "keepAlive", "port", "mtu", "preSharedKey", "email"} {
		t.Run(field, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal([]byte(content), &root); err != nil {
				t.Fatal(err)
			}
			inbound := mapValue(root["inbounds"].([]any)[0])
			settings := mapValue(inbound["settings"])
			peer := mapValue(settings["peers"].([]any)[0])
			switch field {
			case "port":
				inbound[field] = float64(input.Port) + 0.5
			case "mtu":
				settings[field] = float64(input.WireGuardMTU) + 0.5
			default:
				peer[field] = 0.5
			}
			raw, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			if parsed := ParseAll(core.EngineXray, string(raw)); len(parsed) != 0 {
				t.Fatal("invalid field would be silently coerced by preset editing")
			}
		})
	}
}

func TestSingBoxEndpointConfigFilesKeepNativeOrderAndPrecision(t *testing.T) {
	content := `{"inbounds":[{"type":"socks","tag":"web","listen_port":1080}],"endpoints":[{"type":"wireguard","tag":"wg","listen_port":51820},{"type":"tailscale","tag":"custom","custom_number":9007199254740993}],"outbounds":[{"type":"direct","tag":"direct"},{"type":"direct","tag":"qch-trf-1080-abcdef012345"},{"type":"direct","tag":"qch-trf-51820-abcdef012345"}]}`
	files, err := SplitConfigFiles(core.EngineSingBox, content)
	if err != nil || len(files) != 4 {
		t.Fatalf("split endpoints: %v", err)
	}
	for i, path := range []string{"common.json", "inbounds/web.json", "endpoints/wg.json", "endpoints/custom.json"} {
		if files[i].Path != path {
			t.Fatalf("file %d: %s", i, files[i].Path)
		}
	}
	if !strings.Contains(files[2].Content, "qch-trf-51820") || strings.Contains(files[0].Content, "qch-trf-") {
		t.Fatal("endpoint outbounds lost their owner")
	}
	merged, err := MergeConfigFiles(core.EngineSingBox, files)
	if err != nil || !strings.Contains(merged, "9007199254740993") {
		t.Fatalf("endpoint bundle lost precision: %v", err)
	}
	var before, after any
	_ = json.Unmarshal([]byte(content), &before)
	_ = json.Unmarshal([]byte(merged), &after)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("endpoint bundle changed native arrays or unknown fields")
	}
	if _, err := MergeConfigFiles(core.EngineXray, files); err == nil {
		t.Fatal("Xray accepted a sing-box endpoint file")
	}
	for _, path := range []string{"endpoints/../escape.json", "endpoints/.hidden.json"} {
		files[2].Path = path
		if _, err := MergeConfigFiles(core.EngineSingBox, files); err == nil {
			t.Fatal("unsafe endpoint filename accepted")
		}
	}
}
