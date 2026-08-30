package serverconfig

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMainlandAccessPoliciesApplyPerInboundAndPreserveUnrelatedRules(t *testing.T) {
	t.Parallel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		engine := engine
		t.Run(string(engine), func(t *testing.T) {
			t.Parallel()
			protocol, ok := FindProtocol(engine, ProtocolVLESS)
			if !ok {
				t.Fatalf("%s VLESS preset was not published", engine)
			}
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			content = addUnrelatedMainlandTestRule(t, engine, content)
			original := decodeTrafficConfiguration(engine, content)
			updated, err := ApplyMainlandAccessPolicyWithPrefixes(engine, content, MainlandAccessPolicy{
				Tag: input.Tag, Port: input.Port, Engine: engine,
				BlockMainlandDestination: true, BlockMainlandSource: true,
			}, []string{"1.1.8.0/24", "240e::/16"})
			if err != nil {
				t.Fatal(err)
			}
			policies := DiscoverMainlandAccessPolicies(engine, updated)
			if len(policies) != 1 || !policies[0].BlockMainlandDestination || !policies[0].BlockMainlandSource {
				t.Fatalf("DiscoverMainlandAccessPolicies(%s) = %+v\n%s", engine, policies, updated)
			}
			if !strings.Contains(updated, "example.org") {
				t.Fatalf("%s update discarded an unrelated routing rule:\n%s", engine, updated)
			}
			updated, err = ApplyMainlandAccessPolicyWithPrefixes(engine, updated, MainlandAccessPolicy{Tag: input.Tag, Port: input.Port, Engine: engine}, nil)
			if err != nil {
				t.Fatal(err)
			}
			policies = DiscoverMainlandAccessPolicies(engine, updated)
			if len(policies) != 1 || policies[0].BlockMainlandDestination || policies[0].BlockMainlandSource {
				t.Fatalf("disabled DiscoverMainlandAccessPolicies(%s) = %+v\n%s", engine, policies, updated)
			}
			if !strings.Contains(updated, "example.org") {
				t.Fatalf("%s disable discarded an unrelated routing rule:\n%s", engine, updated)
			}
			restored := decodeTrafficConfiguration(engine, updated)
			if !reflect.DeepEqual(restored, original) {
				t.Fatalf("%s disable did not restore the original configuration\noriginal: %#v\nrestored: %#v", engine, original, restored)
			}
		})
	}
}

func TestMainlandAccessPolicyRejectsTagPortMismatch(t *testing.T) {
	t.Parallel()
	protocol, _ := FindProtocol(core.EngineMihomo, ProtocolVLESS)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	content, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyMainlandAccessPolicyWithPrefixes(core.EngineMihomo, content, MainlandAccessPolicy{
		Tag: input.Tag, Port: input.Port + 1, BlockMainlandDestination: true,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "不存在匹配") {
		t.Fatalf("tag/port mismatch error = %v", err)
	}
}

func TestShadowsocksRustMainlandPolicyRemainsOutsideCoreJSON(t *testing.T) {
	t.Parallel()
	protocol, ok := FindProtocol(core.EngineShadowsocksRust, ProtocolShadowsocks)
	if !ok {
		t.Fatal("ss-rust Shadowsocks preset was not published")
	}
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	content, err := Generate(core.EngineShadowsocksRust, input)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := ApplyMainlandAccessPolicyWithPrefixes(core.EngineShadowsocksRust, content, MainlandAccessPolicy{
		Tag: "ss-rust", Port: input.Port, Engine: core.EngineShadowsocksRust, BlockMainlandSource: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated != content || strings.Contains(updated, "qch-mainland") {
		t.Fatalf("ss-rust policy polluted native config:\n%s", updated)
	}
	policies := DiscoverMainlandAccessPolicies(core.EngineShadowsocksRust, updated)
	if len(policies) != 1 || policies[0].Tag != "ss-rust" || policies[0].Port != input.Port {
		t.Fatalf("ss-rust inbound discovery = %+v", policies)
	}
	if _, err := ApplyMainlandAccessPolicyWithPrefixes(core.EngineShadowsocksRust, content, MainlandAccessPolicy{
		Tag: input.Tag, Port: input.Port, Engine: core.EngineShadowsocksRust, BlockMainlandDestination: true,
	}, nil); err == nil {
		t.Fatal("ss-rust destination restriction was accepted")
	}
}

func TestMainlandRulesUseExternalCoreResources(t *testing.T) {
	t.Parallel()
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		protocol, ok := FindProtocol(engine, ProtocolVLESS)
		if !ok {
			t.Fatalf("%s VLESS preset missing", engine)
		}
		input, err := NewPlan(protocol)
		if err != nil {
			t.Fatal(err)
		}
		content, err := Generate(engine, input)
		if err != nil {
			t.Fatal(err)
		}
		content, err = ApplyMainlandAccessPolicyWithPrefixes(engine, content, MainlandAccessPolicy{
			Tag: input.Tag, Port: input.Port, Engine: engine, BlockMainlandDestination: true,
		}, []string{"1.1.1.0/24"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(content, "1.1.1.0/24") {
			t.Fatalf("%s embedded CIDR data in generated config:\n%s", engine, content)
		}
		if engine == core.EngineXray && !strings.Contains(content, "geoip:cn") {
			t.Fatalf("Xray resource reference missing:\n%s", content)
		}
		if engine == core.EngineSingBox && (!strings.Contains(content, `"type": "remote"`) || !strings.Contains(content, mainlandSingBoxRuleSetURL)) {
			t.Fatalf("sing-box remote rule-set missing:\n%s", content)
		}
	}
}

func TestMainlandAccessPoliciesRemainIndependentAcrossInboundPorts(t *testing.T) {
	t.Parallel()
	prefixes := []string{"1.1.8.0/24", "240e::/16"}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		engine := engine
		t.Run(string(engine), func(t *testing.T) {
			t.Parallel()
			protocol, _ := FindProtocol(engine, ProtocolVLESS)
			first, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			second, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			firstContent, err := Generate(engine, first)
			if err != nil {
				t.Fatal(err)
			}
			secondContent, err := Generate(engine, second)
			if err != nil {
				t.Fatal(err)
			}
			content, err := MutateGenerated(engine, firstContent, secondContent, "", "add")
			if err != nil {
				t.Fatal(err)
			}
			original := decodeTrafficConfiguration(engine, content)
			content, err = ApplyMainlandAccessPolicyWithPrefixes(engine, content, MainlandAccessPolicy{
				Tag: first.Tag, Port: first.Port, BlockMainlandDestination: true,
			}, prefixes)
			if err != nil {
				t.Fatal(err)
			}
			content, err = ApplyMainlandAccessPolicyWithPrefixes(engine, content, MainlandAccessPolicy{
				Tag: second.Tag, Port: second.Port, BlockMainlandSource: true,
			}, prefixes)
			if err != nil {
				t.Fatal(err)
			}
			policies := DiscoverMainlandAccessPolicies(engine, content)
			byTag := make(map[string]MainlandAccessPolicy, len(policies))
			for _, policy := range policies {
				byTag[policy.Tag] = policy
			}
			if !byTag[first.Tag].BlockMainlandDestination || byTag[first.Tag].BlockMainlandSource ||
				byTag[second.Tag].BlockMainlandDestination || !byTag[second.Tag].BlockMainlandSource {
				t.Fatalf("independent policies for %s = %+v", engine, policies)
			}
			content, err = ApplyMainlandAccessPolicyWithPrefixes(engine, content, MainlandAccessPolicy{
				Tag: first.Tag, Port: first.Port,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			policies = DiscoverMainlandAccessPolicies(engine, content)
			byTag = make(map[string]MainlandAccessPolicy, len(policies))
			for _, policy := range policies {
				byTag[policy.Tag] = policy
			}
			if byTag[first.Tag].BlockMainlandDestination || !byTag[second.Tag].BlockMainlandSource {
				t.Fatalf("disabling first policy changed another %s inbound: %+v", engine, policies)
			}
			content, err = ApplyMainlandAccessPolicyWithPrefixes(engine, content, MainlandAccessPolicy{
				Tag: second.Tag, Port: second.Port,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if restored := decodeTrafficConfiguration(engine, content); !reflect.DeepEqual(restored, original) {
				t.Fatalf("%s did not restore shared resources after the last port was disabled\noriginal: %#v\nrestored: %#v", engine, original, restored)
			}
		})
	}
}

func TestXHTTPPlansBlockMainlandDestinationByDefault(t *testing.T) {
	t.Parallel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray} {
		for _, key := range []string{ProtocolVLESSXHTTP, ProtocolVLESSEncXHTTP} {
			protocol, ok := FindProtocol(engine, key)
			if !ok {
				t.Fatalf("%s %s preset was not published", engine, key)
			}
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			if !input.BlockMainlandDestination || input.BlockMainlandSource {
				t.Fatalf("%s %s access defaults = %+v", engine, key, input)
			}
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(engine, content)
			if !ok || !parsed.BlockMainlandDestination || parsed.BlockMainlandSource {
				t.Fatalf("Parse(%s/%s) access defaults = %+v, %v\n%s", engine, key, parsed, ok, content)
			}
		}
	}
}

func TestMainlandAccessPolicyDoesNotClaimReservedResourcesWithoutOwnedRules(t *testing.T) {
	t.Parallel()
	prefixes := []string{"1.1.8.0/24", "240e::/16"}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		engine := engine
		t.Run(string(engine), func(t *testing.T) {
			t.Parallel()
			protocol, _ := FindProtocol(engine, ProtocolVLESS)
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			root := decodeTrafficConfiguration(engine, content)
			switch engine {
			case core.EngineMihomo:
				root["rule-providers"] = map[string]any{
					mainlandMihomoProviderTag: map[string]any{
						"type": "http", "behavior": "ipcidr", "format": "text", "url": ChinaRoutesURL,
						"path": "./ruleset/qch-chnroutes2-cn.txt", "interval": 3600,
					},
				}
			case core.EngineXray:
				outbounds, _ := root["outbounds"].([]any)
				root["outbounds"] = append(outbounds, map[string]any{
					"protocol": "blackhole", "tag": mainlandXrayBlockTag,
				})
			case core.EngineSingBox:
				root["route"] = map[string]any{"rule_set": []any{map[string]any{
					"type": "inline", "tag": mainlandSingBoxRuleSetTag,
					"rules": []any{map[string]any{"domain_suffix": []any{"example.org"}}},
				}}}
			}
			content, err = marshalMainlandConfiguration(engine, root)
			if err != nil {
				t.Fatal(err)
			}
			preserved, err := ApplyMainlandAccessPolicyWithPrefixes(engine, content, MainlandAccessPolicy{
				Tag: input.Tag, Port: input.Port,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(preserved, mainlandMihomoProviderTag) && !strings.Contains(preserved, mainlandXrayBlockTag) && !strings.Contains(preserved, mainlandSingBoxRuleSetTag) {
				t.Fatalf("%s removed an unowned reserved resource:\n%s", engine, preserved)
			}
			_, err = ApplyMainlandAccessPolicyWithPrefixes(engine, content, MainlandAccessPolicy{
				Tag: input.Tag, Port: input.Port, BlockMainlandDestination: true,
			}, prefixes)
			if err == nil || !strings.Contains(err.Error(), "占用") {
				t.Fatalf("%s reserved resource collision error = %v", engine, err)
			}
		})
	}
}

func TestXrayMainlandDestinationRestoresOwnedDomainStrategy(t *testing.T) {
	t.Parallel()
	protocol, _ := FindProtocol(core.EngineXray, ProtocolVLESS)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	content, err := Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	content, err = ApplyMainlandAccessPolicyWithPrefixes(core.EngineXray, content, MainlandAccessPolicy{
		Tag: input.Tag, Port: input.Port, BlockMainlandDestination: true,
	}, []string{"1.1.8.0/24", "240e::/16"})
	if err != nil {
		t.Fatal(err)
	}
	root := decodeTrafficConfiguration(core.EngineXray, content)
	routing := mapValue(root["routing"])
	if stringValue(routing["domainStrategy"]) != "IPIfNonMatch" || !xrayHasStrategyMarker(routing) {
		t.Fatalf("enabled routing did not record the owned strategy: %+v", routing)
	}
	content, err = ApplyMainlandAccessPolicyWithPrefixes(core.EngineXray, content, MainlandAccessPolicy{
		Tag: input.Tag, Port: input.Port,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	root = decodeTrafficConfiguration(core.EngineXray, content)
	routing = mapValue(root["routing"])
	if routing != nil && (routing["domainStrategy"] != nil || xrayHasStrategyMarker(routing)) {
		t.Fatalf("disable retained QControlHub-owned routing state: %+v", routing)
	}
}

func TestXrayMainlandDestinationPreservesOperatorDomainStrategy(t *testing.T) {
	t.Parallel()
	protocol, _ := FindProtocol(core.EngineXray, ProtocolVLESS)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	content, err := Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	root := decodeTrafficConfiguration(core.EngineXray, content)
	root["routing"] = map[string]any{
		"domainStrategy": "AsIs",
		"rules":          []any{map[string]any{"type": "field", "domain": []any{"full:example.org"}, "outboundTag": "direct"}},
	}
	content, err = marshalMainlandConfiguration(core.EngineXray, root)
	if err != nil {
		t.Fatal(err)
	}
	content, err = ApplyMainlandAccessPolicyWithPrefixes(core.EngineXray, content, MainlandAccessPolicy{
		Tag: input.Tag, Port: input.Port, BlockMainlandDestination: true,
	}, []string{"1.1.8.0/24", "240e::/16"})
	if err != nil {
		t.Fatal(err)
	}
	content, err = ApplyMainlandAccessPolicyWithPrefixes(core.EngineXray, content, MainlandAccessPolicy{
		Tag: input.Tag, Port: input.Port,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	root = decodeTrafficConfiguration(core.EngineXray, content)
	routing := mapValue(root["routing"])
	if stringValue(routing["domainStrategy"]) != "AsIs" || xrayHasStrategyMarker(routing) || !strings.Contains(content, "example.org") {
		t.Fatalf("operator routing state was not preserved: %+v", routing)
	}
}

func TestXrayMainlandSourceDoesNotChangeDomainStrategy(t *testing.T) {
	t.Parallel()
	protocol, _ := FindProtocol(core.EngineXray, ProtocolVLESS)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.BlockMainlandSource = true
	content, err := Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	root := decodeTrafficConfiguration(core.EngineXray, content)
	routing := mapValue(root["routing"])
	if routing == nil || routing["domainStrategy"] != nil || xrayHasStrategyMarker(routing) {
		t.Fatalf("source-only policy changed domain strategy: %+v", routing)
	}
}

func TestXrayMainlandDestinationPreservesLaterOperatorStrategyChange(t *testing.T) {
	t.Parallel()
	protocol, _ := FindProtocol(core.EngineXray, ProtocolVLESS)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	content, err := Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	content, err = ApplyMainlandAccessPolicyWithPrefixes(core.EngineXray, content, MainlandAccessPolicy{
		Tag: input.Tag, Port: input.Port, BlockMainlandDestination: true,
	}, []string{"1.1.8.0/24", "240e::/16"})
	if err != nil {
		t.Fatal(err)
	}
	root := decodeTrafficConfiguration(core.EngineXray, content)
	mapValue(root["routing"])["domainStrategy"] = "AsIs"
	content, err = marshalMainlandConfiguration(core.EngineXray, root)
	if err != nil {
		t.Fatal(err)
	}
	content, err = ApplyMainlandAccessPolicyWithPrefixes(core.EngineXray, content, MainlandAccessPolicy{
		Tag: input.Tag, Port: input.Port,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	root = decodeTrafficConfiguration(core.EngineXray, content)
	routing := mapValue(root["routing"])
	if stringValue(routing["domainStrategy"]) != "AsIs" || xrayHasStrategyMarker(routing) {
		t.Fatalf("later operator strategy change was not preserved: %+v", routing)
	}
}

func xrayHasStrategyMarker(routing map[string]any) bool {
	rules, _ := routing["rules"].([]any)
	for _, value := range rules {
		rule, _ := value.(map[string]any)
		if stringValue(rule["ruleTag"]) == mainlandXrayStrategyMarkerTag {
			return true
		}
	}
	return false
}

func addUnrelatedMainlandTestRule(t *testing.T, engine core.Engine, content string) string {
	t.Helper()
	root := decodeTrafficConfiguration(engine, content)
	switch engine {
	case core.EngineMihomo:
		rules := stringSliceValue(root["rules"])
		root["rules"] = append([]string{"DOMAIN,example.org,DIRECT"}, rules...)
	case core.EngineXray:
		routing := map[string]any{"rules": []any{map[string]any{
			"type": "field", "domain": []any{"full:example.org"}, "outboundTag": "direct",
		}}}
		root["routing"] = routing
	case core.EngineSingBox:
		root["route"] = map[string]any{"rules": []any{map[string]any{
			"domain": []any{"example.org"}, "action": "route", "outbound": "direct",
		}}}
	}
	formatted, err := marshalMainlandConfiguration(engine, root)
	if err != nil {
		t.Fatal(err)
	}
	return formatted
}
