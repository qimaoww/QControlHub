package serverconfig

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

func TestSnellPresetIdentityRoundTrips(t *testing.T) {
	for _, preset := range snellPresets {
		t.Run(preset.key, func(t *testing.T) {
			input := mihomoPresetPlan(t, preset.key)
			input.SnellUDP, input.SnellReuse = false, false
			content, err := Generate(core.EngineMihomo, input)
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := MarshalClientMetadata(input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(core.EngineMihomo, content)
			if !ok || parsed.SourceOnly || parsed.Protocol != preset.key || parsed.SnellVersion != preset.version || parsed.SnellMode != preset.mode || parsed.SnellUDP {
				t.Fatalf("round trip: %+v", parsed)
			}
			if err := ApplyClientMetadata(&parsed, metadata); err != nil {
				t.Fatal(err)
			}
			if parsed.SnellReuse {
				t.Fatal("false reuse lost")
			}
			regenerated, err := Generate(core.EngineMihomo, parsed)
			if err != nil {
				t.Fatal(err)
			}
			var original, actual any
			_ = yaml.Unmarshal([]byte(content), &original)
			_ = yaml.Unmarshal([]byte(regenerated), &actual)
			if !reflect.DeepEqual(original, actual) {
				t.Fatalf("round trip changed native semantics\n%s\n%s", content, regenerated)
			}
			profile, err := BuildClientProfile(parsed, "2001:db8::1", "")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(profile.URI, "reuse = false") || !strings.Contains(profile.URI, "udp-relay = false") {
				t.Fatal("false client settings lost")
			}
			var client map[string]any
			_ = yaml.Unmarshal([]byte(profile.Mihomo), &client)
			if client["udp"] != false || client["reuse"] != false || intValue(client["version"]) != preset.version {
				t.Fatal("Mihomo export mismatch")
			}
			if preset.version == 6 && (client["mode"] != preset.mode || !strings.Contains(profile.URI, "mode = "+preset.mode)) {
				t.Fatal("v6 mode lost")
			}
			fresh, err := RegeneratePlan(mustSnellProtocol(t, preset.key), input)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Credential == input.Credential || fresh.SnellVersion != preset.version || fresh.SnellMode != preset.mode || fresh.SnellUDP || fresh.SnellReuse {
				t.Fatal("credential regeneration changed fixed options or reused PSK")
			}
		})
	}
}

func mustSnellProtocol(t *testing.T, key string) Protocol {
	t.Helper()
	p, ok := FindProtocol(core.EngineMihomo, key)
	if !ok {
		t.Fatal(key)
	}
	return p
}

func TestSnellNativeSamplesAndBoundaries(t *testing.T) {
	// Independent, hand-written Mihomo samples; omitted udp defaults to false.
	for _, mode := range []string{"", "default", "unshaped"} {
		source := "listeners:\n- {type: snell, name: independent, listen: '::', port: 21777, version: 6, psk: 'abcdefghijkl', mode: '" + mode + "'}\nrules: ['MATCH,DIRECT']\n"
		parsed, ok := Parse(core.EngineMihomo, source)
		if !ok || parsed.SourceOnly || parsed.SnellUDP {
			t.Fatalf("native sample: %+v", parsed)
		}
		if _, err := Generate(core.EngineMihomo, parsed); err != nil {
			t.Fatal(err)
		}
	}
	input := mihomoPresetPlan(t, ProtocolSnellV6)
	for _, item := range []struct {
		key   string
		valid bool
	}{
		{strings.Repeat("a", 11), false}, {strings.Repeat("a", 12), true}, {strings.Repeat("a", 255), true}, {strings.Repeat("a", 256), false},
		{strings.Repeat("界", 4), true}, {strings.Repeat("界", 86), false}, {"spaces,\"quote\\slash", true}, {"line\nbreak-12345", false}, {"nul\x00byte-123456", false}, {"tab\tbyte-123456", false},
	} {
		input.Credential = item.key
		_, err := Generate(core.EngineMihomo, input)
		if (err == nil) != item.valid {
			t.Fatalf("PSK bytes=%d validity=%v err=%v", len(item.key), item.valid, err)
		}
		if item.valid {
			profile, err := BuildClientProfile(input, "node.example.test", "")
			if err != nil {
				t.Fatal(err)
			}
			if strings.ContainsAny(item.key, "\"\\") {
				if profile.Format != "Mihomo Snell YAML" || profile.URIError == "" {
					t.Fatal("ambiguous Surge escaping was advertised as compatible")
				}
			} else if strings.ContainsAny(item.key, " ,") && !strings.Contains(profile.URI, "psk = "+surgeValue(item.key)) {
				t.Fatal("Surge escaping lost")
			}
		}
	}
	for _, edit := range []func(*Input){
		func(p *Input) { p.SnellVersion = 5 }, func(p *Input) { p.SnellMode = "unshaped" }, func(p *Input) { p.SnellMode = "unsafe-raw" }, func(p *Input) { p.SnellObfsMode = "http" }, func(p *Input) { p.Transport = "websocket"; p.TransportPath = "/" }, func(p *Input) { p.TLSEnabled = true },
	} {
		p := mihomoPresetPlan(t, ProtocolSnellV6)
		edit(&p)
		if _, err := Generate(core.EngineMihomo, p); err == nil {
			t.Fatal("incompatible preset accepted")
		}
		if _, err := RegeneratePlan(mustSnellProtocol(t, ProtocolSnellV6), p); err == nil {
			t.Fatal("regeneration silently replaced an incompatible preset identity")
		}
	}
	for _, mode := range []string{"unsafe-raw", "unknown"} {
		parsed, ok := Parse(core.EngineMihomo, "listeners: [{name: source, type: snell, listen: 0.0.0.0, port: 21777, version: 6, psk: abcdefghijkl, mode: "+mode+"}]")
		if ok && !parsed.SourceOnly {
			t.Fatal("unsafe native source exposed as editable preset")
		}
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		if _, ok := FindProtocol(engine, ProtocolSnellV6); ok {
			t.Fatal("unsupported engine advertised Snell")
		}
	}
}

func TestSnellMultipleInboundsAndMetadataIsolation(t *testing.T) {
	content := "listeners: []\nrules: ['MATCH,DIRECT']\ncustom-root: keep\n"
	inputs := []Input{}
	for _, key := range []string{ProtocolSnell, ProtocolSnellV6, ProtocolSnellV6, ProtocolSnellV6Unshaped, ProtocolSnellV6ShadowTLS, ProtocolVLESSEncPlain} {
		input := mihomoPresetPlan(t, key)
		if len(inputs) > 0 && (input.Credential == inputs[0].Credential || input.Tag == inputs[0].Tag) {
			t.Fatal("reused identity")
		}
		generated, err := Generate(core.EngineMihomo, input)
		if err != nil {
			t.Fatal(err)
		}
		content, err = MutateGenerated(core.EngineMihomo, content, generated, "", "add")
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input)
	}
	target := inputs[2]
	original := target.Tag
	target.Tag = "renamed"
	target.Credential = strings.Repeat("x", 24)
	target.Protocol, target.SnellMode, target.SnellVersion = ProtocolSnell, "", 5
	generated, err := Generate(core.EngineMihomo, target)
	if err != nil {
		t.Fatal(err)
	}
	content, err = MutateGenerated(core.EngineMihomo, content, generated, original, "modify")
	if err != nil {
		t.Fatal(err)
	}
	got := ParseAll(core.EngineMihomo, content)
	if len(got) != len(inputs) || !strings.Contains(content, "custom-root: keep") {
		t.Fatal("inbounds or root fields lost")
	}
	for i, input := range inputs {
		if i != 2 && (got[i].Tag != input.Tag || got[i].Credential != input.Credential || got[i].Protocol != input.Protocol) {
			t.Fatal("unselected inbound changed")
		}
	}
	if got[2].SnellMode != "" {
		t.Fatal("stale v6 mode survived v5 switch")
	}
	meta, _ := MarshalClientMetadata(inputs[1])
	changed := inputs[1]
	changed.Credential = strings.Repeat("z", 24)
	changed.SnellReuse = false
	if err := ApplyClientMetadata(&changed, meta); err != nil || changed.SnellReuse {
		t.Fatal("stale credential metadata applied")
	}
	if _, err = DeletePresetInbound(core.EngineMihomo, content, original); err == nil {
		t.Fatal("deleted old identity")
	}
	content, err = DeletePresetInbound(core.EngineMihomo, content, target.Tag)
	if err != nil {
		t.Fatal(err)
	}
	remaining := ParseAll(core.EngineMihomo, content)
	if len(remaining) != len(inputs)-1 {
		t.Fatal("delete removed siblings")
	}
	for _, sibling := range remaining {
		for i, original := range inputs {
			if i != 2 && sibling.Tag == original.Tag && sibling.Credential != original.Credential {
				t.Fatal("delete changed sibling")
			}
		}
	}
}

func TestSnellNativeAmbiguityPreservesSource(t *testing.T) {
	base := "listeners: [{name: raw, type: snell, listen: 127.0.0.1, port: 21777, version: 6, psk: abcdefghijkl, %s}]"
	for _, extra := range []string{"mode: 123", "mode: null", "udp: invalid", "res-tls: {enable: true}", "jls-config: {enable: true, password: native-password}",
		"shadow-tls: {enable: true, version: 3, strict-mode: true, handshake: {dest: 'cover.example:443', extra: retain}, users: [{name: user, password: password123}]}",
		"shadow-tls: {enable: true, version: 3, strict-mode: true, handshake: {dest: 'cover.example:443'}, users: [{name: user, password: password123, extra: retain}]}",
		"shadow-tls: {enable: true, version: 3, strict-mode: true, handshake: {dest: 'cover.example:443'}, users: [{name: first, password: password123}, {name: second, password: password456}]}"} {
		source := fmt.Sprintf(base, extra)
		parsed, ok := Parse(core.EngineMihomo, source)
		if ok && !parsed.SourceOnly {
			t.Fatalf("ambiguous input became editable: %s", extra)
		}
		if ok {
			if _, err := BuildClientProfile(parsed, "node.example.test", ""); err == nil {
				t.Fatalf("ambiguous input produced a partial client export: %s", extra)
			}
		}
		if _, err := MutateGenerated(core.EngineMihomo, source, "", "raw", "modify"); err == nil {
			t.Fatal("opaque source overwritten")
		}
	}
	input := mihomoPresetPlan(t, ProtocolSnellV6ShadowTLS)
	metadata, _ := MarshalClientMetadata(input)
	input.SnellShadowTLSPassword = "a-different-password"
	input.SnellReuse = false
	if err := ApplyClientMetadata(&input, metadata); err != nil || input.SnellReuse {
		t.Fatal("stale ShadowTLS metadata applied")
	}
}
