package serverconfig

import (
	"reflect"
	"strings"
	"testing"

	"github.com/enfein/mieru/v3/apis/trafficpattern"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

func testMieruCustomPattern() *MieruCustomPattern {
	return &MieruCustomPattern{
		TCPFragment: true, MaxSleepMs: 11,
		NonceType: "PRINTABLE", NonceApplyToAllUDP: true, NonceMinLen: 5, NonceMaxLen: 9,
		PaddingMiddle: 70, PaddingEnd: 130, LowEntropyMode: 48, MaskRotation: "left_3",
	}
}

func TestMieruCustomPatternRoundTrip(t *testing.T) {
	patterns := map[string]*MieruCustomPattern{
		"same values as balanced": {
			TCPFragment: true, MaxSleepMs: 8, NonceType: "PRINTABLE", NonceApplyToAllUDP: true,
			NonceMinLen: 6, NonceMaxLen: 8, PaddingMiddle: 64, PaddingEnd: 128,
			LowEntropyMode: 56, MaskRotation: "right_7",
		},
		"mixed": testMieruCustomPattern(),
		"zeros": {
			NonceType: "RANDOM", MaskRotation: "none",
		},
		"fixed": {
			TCPFragment: true, MaxSleepMs: 100, NonceType: "FIXED", NonceFixedHex: "00010203, aabbccdd",
			NonceApplyToAllUDP: true, PaddingMiddle: 255, PaddingEnd: 255,
			LowEntropyMode: 32, MaskRotation: "right_15",
		},
	}
	for name, custom := range patterns {
		for _, transport := range []string{"TCP", "UDP"} {
			t.Run(name+"/"+transport, func(t *testing.T) {
				input := mihomoPresetPlan(t, ProtocolMieru)
				input.MieruTransport, input.MieruTrafficPattern, input.MieruCustomPattern = transport, mieruTrafficCustom, custom
				content, err := Generate(core.EngineMihomo, input)
				if err != nil {
					t.Fatal(err)
				}
				var root map[string]any
				if err := yaml.Unmarshal([]byte(content), &root); err != nil {
					t.Fatal(err)
				}
				encoded := stringValue(firstMap(root["listeners"])["traffic-pattern"])
				if encoded == "" {
					t.Fatal("custom traffic pattern was not written")
				}
				if name == "same values as balanced" && encoded == mieruTrafficPatterns[mieruTrafficBalanced] {
					t.Fatal("custom mode cannot be distinguished from the balanced preset")
				}
				message, err := trafficpattern.Decode(encoded)
				if err != nil {
					t.Fatal(err)
				}
				effective, err := trafficpattern.NewConfig(message)
				if err != nil {
					t.Fatalf("Mieru rejected custom pattern: %v", err)
				}
				if name == "same values as balanced" {
					balanced, err := trafficpattern.Decode(mieruTrafficPatterns[mieruTrafficBalanced])
					if err != nil {
						t.Fatal(err)
					}
					known, err := trafficpattern.NewConfig(balanced)
					if err != nil || !proto.Equal(effective.Effective().GetTcpFragment(), known.Effective().GetTcpFragment()) ||
						!proto.Equal(effective.Effective().GetNonce(), known.Effective().GetNonce()) ||
						!proto.Equal(effective.Effective().GetPadding(), known.Effective().GetPadding()) ||
						!proto.Equal(effective.Effective().GetLowEntropy(), known.Effective().GetLowEntropy()) {
						t.Fatalf("custom marker changed Mieru traffic behavior: %v", err)
					}
				}
				if message.TcpFragment.Enable == nil || message.TcpFragment.MaxSleepMs == nil ||
					message.Nonce.Type == nil || message.Nonce.ApplyToAllUDPPacket == nil || message.Nonce.MinLen == nil || message.Nonce.MaxLen == nil ||
					message.Padding.MaxMiddlePaddingLen == nil || message.Padding.MaxEndPaddingLen == nil ||
					message.LowEntropy.Mode == nil || message.LowEntropy.MaskRotation == nil {
					t.Fatal("custom pattern omitted an explicit field")
				}
				parsed, ok := Parse(core.EngineMihomo, content)
				if !ok || parsed.MieruTrafficPattern != mieruTrafficCustom || !reflect.DeepEqual(parsed.MieruCustomPattern, custom) {
					t.Fatalf("custom pattern did not reopen: %+v", parsed.MieruCustomPattern)
				}
				again, err := Generate(core.EngineMihomo, parsed)
				if err != nil || again != content {
					t.Fatalf("custom pattern changed on round trip: %v", err)
				}
				profile, err := BuildClientProfile(parsed, "edge.example.com", "")
				if err != nil {
					t.Fatal(err)
				}
				var proxy map[string]any
				if err := yaml.Unmarshal([]byte(profile.Mihomo), &proxy); err != nil || proxy["traffic-pattern"] != encoded {
					t.Fatalf("client custom pattern differs: %v, %+v", err, proxy)
				}
				protocol, _ := FindProtocol(core.EngineMihomo, ProtocolMieru)
				regenerated, err := RegeneratePlan(protocol, parsed)
				if err != nil || regenerated.MieruTrafficPattern != mieruTrafficCustom || !reflect.DeepEqual(regenerated.MieruCustomPattern, custom) {
					t.Fatalf("regeneration lost custom pattern: %v", err)
				}
			})
		}
	}
}

func TestMieruCustomPatternValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*MieruCustomPattern)
	}{
		{"missing", func(_ *MieruCustomPattern) {}},
		{"sleep negative", func(c *MieruCustomPattern) { c.MaxSleepMs = -1 }},
		{"sleep high", func(c *MieruCustomPattern) { c.MaxSleepMs = 101 }},
		{"nonce min negative", func(c *MieruCustomPattern) { c.NonceMinLen = -1 }},
		{"nonce max high", func(c *MieruCustomPattern) { c.NonceMaxLen = 13 }},
		{"nonce reversed", func(c *MieruCustomPattern) { c.NonceMinLen = 10 }},
		{"nonce type", func(c *MieruCustomPattern) { c.NonceType = "unknown" }},
		{"fixed empty", func(c *MieruCustomPattern) { c.NonceType = "FIXED" }},
		{"fixed invalid", func(c *MieruCustomPattern) { c.NonceType, c.NonceFixedHex = "FIXED", "xyz" }},
		{"fixed long", func(c *MieruCustomPattern) { c.NonceType, c.NonceFixedHex = "FIXED", strings.Repeat("ab", 13) }},
		{"padding negative", func(c *MieruCustomPattern) { c.PaddingMiddle = -1 }},
		{"padding high", func(c *MieruCustomPattern) { c.PaddingEnd = 256 }},
		{"entropy", func(c *MieruCustomPattern) { c.LowEntropyMode = 64 }},
		{"rotation", func(c *MieruCustomPattern) { c.MaskRotation = "left_16" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := mihomoPresetPlan(t, ProtocolMieru)
			input.MieruTrafficPattern = mieruTrafficCustom
			if test.name != "missing" {
				input.MieruCustomPattern = testMieruCustomPattern()
				test.edit(input.MieruCustomPattern)
			}
			if _, err := Generate(core.EngineMihomo, input); err == nil {
				t.Fatal("invalid custom server pattern accepted")
			}
			if _, err := BuildClientProfile(input, "edge.example.com", ""); err == nil {
				t.Fatal("invalid custom client pattern accepted")
			}
		})
	}
}

func TestMieruCustomPatternProtectsUnsupportedSource(t *testing.T) {
	input := mihomoPresetPlan(t, ProtocolMieru)
	content, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(*appctlpb.TrafficPattern)
	}{
		{"implicit values", func(p *appctlpb.TrafficPattern) { p.Padding.MaxEndPaddingLen = nil }},
		{"missing custom marker", func(p *appctlpb.TrafficPattern) { p.UnlockAll = nil }},
		{"unlock all", func(p *appctlpb.TrafficPattern) { p.UnlockAll = proto.Bool(true) }},
		{"seed", func(p *appctlpb.TrafficPattern) { p.Seed = proto.Int32(123) }},
		{"unknown field", func(p *appctlpb.TrafficPattern) { p.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) }},
		{"unknown nonce", func(p *appctlpb.TrafficPattern) { p.Nonce.Type = appctlpb.NonceType(99).Enum() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			pattern := mieruCustomProto(testMieruCustomPattern())
			test.edit(pattern)
			var root map[string]any
			if err := yaml.Unmarshal([]byte(content), &root); err != nil {
				t.Fatal(err)
			}
			firstMap(root["listeners"])["traffic-pattern"] = trafficpattern.Encode(pattern)
			custom, err := yaml.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := Parse(core.EngineMihomo, string(custom)); ok {
				t.Fatal("unsupported source pattern was exposed in the preset editor")
			}
			if _, err := MutateGenerated(core.EngineMihomo, string(custom), content, input.Tag, "modify"); err == nil {
				t.Fatal("unsupported source pattern could be overwritten")
			}
		})
	}
}

func TestMieruCustomPatternMutation(t *testing.T) {
	input := mihomoPresetPlan(t, ProtocolMieru)
	current, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	input.MieruTrafficPattern, input.MieruCustomPattern = mieruTrafficCustom, testMieruCustomPattern()
	generated, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	current, err = MutateGenerated(core.EngineMihomo, current, generated, input.Tag, "modify")
	if err != nil {
		t.Fatal(err)
	}
	if parsed, ok := Parse(core.EngineMihomo, current); !ok || parsed.MieruTrafficPattern != mieruTrafficCustom {
		t.Fatal("custom pattern mutation did not reopen")
	}
	input.MieruTrafficPattern = mieruTrafficOff
	generated, err = Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	current, err = MutateGenerated(core.EngineMihomo, current, generated, input.Tag, "modify")
	if err != nil || strings.Contains(current, "traffic-pattern:") {
		t.Fatalf("switching custom pattern to Off retained pattern: %v", err)
	}
}
