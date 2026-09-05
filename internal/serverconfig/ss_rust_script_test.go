package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSSRustScriptPresetNetworkRoundTrip(t *testing.T) {
	protocol := Protocols(core.EngineShadowsocksRust)[0]
	plan, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Protocol != ProtocolSS2022 || plan.Method != "2022-blake3-aes-128-gcm" || plan.Listen != "::" {
		t.Fatalf("script defaults: %+v", plan)
	}
	plan.SSRustDNS = "1.1.1.1"
	plan.SSRustOutboundBindAddr = "2001:db8::1"
	plan.SSRustIPv6First = true
	generated, err := Generate(core.EngineShadowsocksRust, plan)
	if err != nil {
		t.Fatal(err)
	}
	content, err := MutateGenerated(core.EngineShadowsocksRust, "{}", generated, "", "add")
	if err != nil {
		t.Fatal(err)
	}
	inputs := ParseAll(core.EngineShadowsocksRust, content)
	if len(inputs) != 1 || inputs[0].SSRustDNS != plan.SSRustDNS || inputs[0].SSRustOutboundBindAddr != plan.SSRustOutboundBindAddr || !inputs[0].SSRustIPv6First {
		t.Fatalf("network round trip: %+v\n%s", inputs, content)
	}
	replanned, err := RegeneratePlan(protocol, inputs[0])
	if err != nil || replanned.SSRustDNS != plan.SSRustDNS || !replanned.SSRustIPv6First {
		t.Fatalf("regenerated settings: %+v, %v", replanned, err)
	}
	plan.SSRustDNS, plan.SSRustOutboundBindAddr = "", ""
	generated, _ = Generate(core.EngineShadowsocksRust, plan)
	content, err = MutateGenerated(core.EngineShadowsocksRust, content, generated, inputs[0].Tag, "modify")
	if err != nil || strings.Contains(content, `"dns"`) || strings.Contains(content, `"outbound_bind_addr"`) {
		t.Fatalf("clear network overrides: %v\n%s", err, content)
	}
	plan.SSRustOutboundBindAddr = "eth0"
	if _, err := Generate(core.EngineShadowsocksRust, plan); err == nil {
		t.Fatal("accepted invalid outbound binding")
	}
}

func TestSSRustScriptMutationPreservesPolicyAndGlobalSettings(t *testing.T) {
	current := `{"servers":[{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"old-password","mode":"tcp_only","timeout":90,"acl":"/managed/per-port.acl","unknown":true}],"acl":"/managed/block_cn.acl","timeout":600,"mode":"udp_only","no_delay":false,"fast_open":true,"ipv6_first":true}`
	plan := Input{Protocol: ProtocolShadowsocks, Tag: "new", Listen: "::", Port: 20002, Method: "aes-256-gcm", Credential: "new-password-long-enough", SSRustIPv6First: true}
	generated, err := Generate(core.EngineShadowsocksRust, plan)
	if err != nil {
		t.Fatal(err)
	}
	modified, err := MutateGenerated(core.EngineShadowsocksRust, current, generated, "ss-rust-1", "modify")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal([]byte(modified), &root)
	entry := root["servers"].([]any)[0].(map[string]any)
	if root["timeout"] != float64(600) || root["mode"] != "udp_only" || root["no_delay"] != false || root["fast_open"] != true || entry["acl"] != "/managed/per-port.acl" || entry["mode"] != "tcp_only" || entry["timeout"] != float64(90) || entry["unknown"] != true {
		t.Fatalf("mutation lost unmanaged settings: %s", modified)
	}
	added, err := MutateGenerated(core.EngineShadowsocksRust, current, generated, "", "add")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(added), &root)
	entry = root["servers"].([]any)[1].(map[string]any)
	if entry["acl"] != "/managed/block_cn.acl" {
		t.Fatalf("new port lost inherited ACL: %s", added)
	}
	if _, err := MutateGenerated(core.EngineShadowsocksRust, added, generated, "", "add"); err == nil {
		t.Fatal("duplicate port accepted")
	}
}

func TestSSRustCustomDNSIsNotLostDuringPresetEdit(t *testing.T) {
	current := `{"servers":[{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"current-password","dns":{"nameservers":["1.1.1.1"]}}]}`
	inputs := ParseAll(core.EngineShadowsocksRust, current)
	if len(inputs) != 1 {
		t.Fatal("custom DNS prevented client/port discovery")
	}
	inputs[0].Port = 20002
	generated, err := Generate(core.EngineShadowsocksRust, inputs[0])
	if err != nil {
		t.Fatal(err)
	}
	modified, err := MutateGenerated(core.EngineShadowsocksRust, current, generated, inputs[0].Tag, "modify")
	if err != nil || !strings.Contains(modified, `"nameservers"`) {
		t.Fatalf("custom DNS was cleared: %v\n%s", err, modified)
	}
}
