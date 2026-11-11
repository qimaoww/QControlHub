package serverconfig

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

func udp443TestPlan(t *testing.T) Input {
	t.Helper()
	protocol, _ := FindProtocol(core.EngineMihomo, ProtocolVLESS)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.RealityServerName = "tls.example.test"
	return input
}
func udp443Bool(v bool) *bool { return &v }
func TestMihomoUDP443DefaultsAndCompatibility(t *testing.T) {
	input := udp443TestPlan(t)
	if input.Port != 443 || input.BlockUDP443 == nil || !*input.BlockUDP443 {
		t.Fatal("missing target defaults")
	}
	input.Port = 8443
	input.BlockUDP443 = udp443Bool(false)
	protocol, _ := FindProtocol(core.EngineMihomo, ProtocolVLESS)
	rotated, err := RegeneratePlan(protocol, input)
	if err != nil || rotated.Port != 8443 || rotated.BlockUDP443 == nil || *rotated.BlockUDP443 {
		t.Fatal("rotation reset choices", err)
	}
	input.BlockUDP443 = nil
	source, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := Parse(core.EngineMihomo, source)
	if !ok || parsed.Port != 8443 || parsed.BlockUDP443 == nil || *parsed.BlockUDP443 {
		t.Fatal("legacy configuration migrated")
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		p, _ := FindProtocol(engine, ProtocolVLESS)
		other, err := NewPlan(p)
		if err != nil || other.Port < 20000 || other.BlockUDP443 != nil {
			t.Fatal("changed other core", engine, err)
		}
		other.BlockUDP443 = udp443Bool(false)
		if _, err = Generate(engine, other); err == nil {
			t.Fatal("accepted unsupported field", engine)
		}
	}
}
func TestMihomoUDP443LifecycleAndAccounting(t *testing.T) {
	input := udp443TestPlan(t)
	input.Tag = "target"
	input.Port = 8443
	generate := func(input Input) string {
		t.Helper()
		source, err := Generate(core.EngineMihomo, input)
		if err != nil {
			t.Fatal(err)
		}
		return source
	}
	sibling := input
	sibling.Tag = "other"
	sibling.Port = 8444
	sibling.BlockUDP443 = nil
	source := generate(sibling)
	// A similar user-authored rule has no reserved provider and must survive.
	custom := "AND,((IN-NAME,target),(NETWORK,UDP),(DST-PORT,443)),REJECT"
	source = strings.Replace(source, "    - MATCH,DIRECT", "    - "+custom+"\n    - MATCH,DIRECT", 1)
	var err error
	source, err = MutateGenerated(core.EngineMihomo, source, generate(input), "", "add")
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false, true, true} {
		input.BlockUDP443 = udp443Bool(enabled)
		source, err = MutateGenerated(core.EngineMihomo, source, generate(input), input.Tag, "modify")
		if err != nil {
			t.Fatal(err)
		}
		plan, err := PlanPresetAccounting(core.EngineMihomo, source)
		if err != nil {
			t.Fatal(err)
		}
		parsed := ParseAll(core.EngineMihomo, plan.Content)
		if len(parsed) != 2 || parsed[1].BlockUDP443 == nil || *parsed[1].BlockUDP443 != enabled || *parsed[0].BlockUDP443 {
			t.Fatalf("readback lost context: %+v", parsed)
		}
		if strings.Count(plan.Content, udp443Rule(input.Tag)) != map[bool]int{true: 1, false: 0}[enabled] || !strings.Contains(plan.Content, custom) {
			t.Fatal("non-idempotent or removed custom rule", plan.Content)
		}
		source, err = PresetAccountingSource(core.EngineMihomo, plan.Content)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Rename only the owned rule; the custom rule deliberately retains its safe
	// refusal boundary. Remove it to exercise a valid managed rename.
	source = strings.ReplaceAll(source, "    - "+custom+"\n", "")
	input.Tag = "renamed"
	source, err = MutateGenerated(core.EngineMihomo, source, generate(input), "target", "modify")
	if err != nil {
		t.Fatal(err)
	}
	source, err = ReconcilePresetInboundReferences(core.EngineMihomo, source, "target", "renamed")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(source, "IN-NAME,target") || !strings.Contains(source, udp443Rule("renamed")) {
		t.Fatal("stale reference", source)
	}
	source, err = DeletePresetInbound(core.EngineMihomo, source, "renamed")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(source, udp443Provider) {
		t.Fatal("delete leaked policy", source)
	}
}
func TestMihomoUDP443RejectsBypassAndOwnershipCollisions(t *testing.T) {
	input := udp443TestPlan(t)
	for _, key := range []string{"proxy", "rule", "mode", "provider", "foreign"} {
		t.Run(key, func(t *testing.T) {
			source, err := Generate(core.EngineMihomo, input)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			_ = yaml.Unmarshal([]byte(source), &root)
			switch key {
			case "proxy", "rule":
				firstMap(root["listeners"])[key] = "DIRECT"
			case "mode":
				root["mode"] = "global"
			case "provider":
				mapValue(root["rule-providers"])[udp443Provider] = map[string]any{"type": "inline", "behavior": "classical", "payload": []string{"DST-PORT,53"}}
			case "foreign":
				root["rules"] = append(stringSliceValue(root["rules"]), "RULE-SET,"+udp443Provider+",DIRECT")
			}
			if err := applyUDP443Mihomo(root, input.Tag, true); err == nil {
				t.Fatal("accepted unsafe route")
			}
		})
	}
}

func TestMihomoUDP443SourceSafety(t *testing.T) {
	input := udp443TestPlan(t)
	source, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = yaml.Unmarshal([]byte(source), &root)
	root["rules"] = []string{"MATCH,DIRECT", udp443Rule(input.Tag)}
	if _, err := prepareMihomoAccounting(root); err == nil {
		t.Fatal("accepted bypassed policy")
	}
	root["rules"] = []string{udp443Rule(input.Tag), "MATCH,DIRECT"}
	firstMap(root["listeners"])["type"] = "trojan"
	if _, err := prepareMihomoAccounting(root); err == nil {
		t.Fatal("accepted policy after incompatible native protocol switch")
	}
}

func TestMihomoUDP443NativeSwitchAndSibling(t *testing.T) {
	one := udp443TestPlan(t)
	one.Tag = "first"
	one.Port = 8443
	source, err := Generate(core.EngineMihomo, one)
	if err != nil {
		t.Fatal(err)
	}
	two := one
	two.Tag = "second"
	two.Port = 8444
	generated, err := Generate(core.EngineMihomo, two)
	if err != nil {
		t.Fatal(err)
	}
	source, err = MutateGenerated(core.EngineMihomo, source, generated, "", "add")
	if err != nil {
		t.Fatal(err)
	}
	// Native editing removes the first managed policy when changing protocol,
	// while retaining the other inbound's independent enabled state.
	source, err = ReplaceInboundSource(core.EngineMihomo, source, "first", "name: first\ntype: socks\nlisten: 127.0.0.1\nport: 8443\n")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = yaml.Unmarshal([]byte(source), &root)
	if udp443MihomoEnabled(root, "first") || !udp443MihomoEnabled(root, "second") {
		t.Fatal("native switch changed wrong policy", source)
	}
	if _, err = PlanPresetAccounting(core.EngineMihomo, source); err != nil {
		t.Fatal(err)
	}
}

func TestMihomoUDP443MainlandAndClientExport(t *testing.T) {
	input := udp443TestPlan(t)
	input.BlockMainlandDestination, input.BlockMainlandSource = true, true
	source, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanPresetAccounting(core.EngineMihomo, source)
	if err != nil {
		t.Fatal(err)
	}
	parsed := ParseAll(core.EngineMihomo, plan.Content)
	if len(parsed) != 1 || !parsed[0].BlockMainlandDestination || !parsed[0].BlockMainlandSource || parsed[0].BlockUDP443 == nil || !*parsed[0].BlockUDP443 {
		t.Fatal("combined policies lost on accounting readback", plan.Content)
	}
	for _, port := range []int{443, 8443} {
		input.ClientPort = port
		profile, err := BuildClientProfile(input, "proxy.example.test", "")
		if err != nil {
			t.Fatal(err)
		}
		outbound, err := BuildClientOutbound(core.EngineMihomo, input, "proxy.example.test", "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(profile.URI, fmt.Sprintf(":%d?", port)) || !strings.Contains(string(outbound), fmt.Sprintf(`"port":%d`, port)) || strings.Contains(profile.URI+string(outbound), "block_udp") || strings.Contains(string(outbound), udp443Provider) {
			t.Fatal("client export changed port or leaked server policy", profile.URI, string(outbound))
		}
	}
}

func TestMihomoUDP443PreservesProviderNamesWithSharedPrefix(t *testing.T) {
	input := udp443TestPlan(t)
	source, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := yaml.Unmarshal([]byte(source), &root); err != nil {
		t.Fatal(err)
	}
	custom := "RULE-SET," + udp443Provider + "-custom,DIRECT"
	root["rules"] = append(stringSliceValue(root["rules"]), custom)
	for _, enabled := range []bool{true, false, true} {
		if err := applyUDP443Mihomo(root, input.Tag, enabled); err != nil {
			t.Fatal("unrelated provider mistaken for reserved provider", err)
		}
		if !strings.Contains(strings.Join(stringSliceValue(root["rules"]), "\n"), custom) {
			t.Fatal("removed unrelated rule")
		}
	}
}
