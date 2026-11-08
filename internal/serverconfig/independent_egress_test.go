package serverconfig

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSingBoxDefaultOutboundDiagnostics(t *testing.T) {
	const inbound = `"inbounds":[{"type":"http","tag":"in","listen_port":21001}]`
	for _, test := range []struct{ name, content, want string }{
		{"missing", `{` + inbound + `}`, "no configured outbounds"},
		{"generated-only", `{` + inbound + `,"outbounds":[{"type":"direct","tag":"qch-trf-21001-test"}]}`, "no original outbounds"},
		{"unknown-final", `{` + inbound + `,"outbounds":[{"type":"direct","tag":"original"}],"route":{"final":"missing"}}`, "unknown default outbound"},
		{"implicit-final", `{` + inbound + `,"outbounds":[{"type":"direct","tag":"first"},{"type":"direct","tag":"second"}]}`, ""},
		{"explicit-final", `{` + inbound + `,"outbounds":[{"type":"direct","tag":"first"},{"type":"direct","tag":"second"}],"route":{"final":"second"}}`, ""},
		{"empty-object", `{}`, "no configured outbounds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := PrepareIndependentEgress(core.EngineSingBox, test.content)
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("want %q, got %v", test.want, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			source, err := PresetAccountingSource(core.EngineSingBox, plan.Content)
			if err != nil {
				t.Fatal(err)
			}
			original, _ := decodeAccountingRoot(core.EngineSingBox, test.content)
			restored, _ := decodeAccountingRoot(core.EngineSingBox, source)
			a, _ := json.Marshal(original["outbounds"])
			b, _ := json.Marshal(restored["outbounds"])
			if string(a) != string(b) || stringValue(mapValue(original["route"])["final"]) != stringValue(mapValue(restored["route"])["final"]) {
				t.Fatal("default or original order changed")
			}
		})
	}
	outs := make([]string, 65)
	for i := range outs {
		outs[i] = fmt.Sprintf(`{"type":"direct","tag":"exit-%d"}`, i)
	}
	if _, err := PrepareIndependentEgress(core.EngineSingBox, `{`+inbound+`,"outbounds":[`+strings.Join(outs, ",")+`]}`); err == nil || !strings.Contains(err.Error(), "original outbound count exceeds 64") {
		t.Fatalf("wrong limit error: %v", err)
	}
}

func TestIndependentEgressRejectsBypasses(t *testing.T) {
	for _, test := range []struct {
		name    string
		engine  core.Engine
		content string
	}{
		{"mihomo-direct-mode", core.EngineMihomo, "mode: direct\nlisteners: [{name: a, type: http, port: 21001}]\n"},
		{"mihomo-global-mode", core.EngineMihomo, "mode: global\nlisteners: [{name: a, type: http, port: 21001}]\n"},
		{"mihomo-shared-group", core.EngineMihomo, "listeners: [{name: a, type: http, port: 21001}]\nproxy-groups: [{name: dynamic, type: select, include-all: true}]\nrules: ['MATCH,dynamic']\n"},
		{"mihomo-tun", core.EngineMihomo, "listeners: [{name: a, type: http, port: 21001}]\ntun: {enable: true}\n"},
		{"mihomo-tunnel", core.EngineMihomo, "listeners: [{name: a, type: http, port: 21001}]\ntunnels: [{network: tcp, address: '0.0.0.0:21002', target: 'example.test:80', proxy: DIRECT}]\n"},
		{"mihomo-mutable-api", core.EngineMihomo, "listeners: [{name: a, type: http, port: 21001}]\nexternal-controller: 127.0.0.1:9090\n"},
		{"mihomo-empty-disguised-ingress", core.EngineMihomo, "listeners: []\nmixed-port: 21001\n"},
		{"mihomo-empty-disguised-tun", core.EngineMihomo, "listeners: []\ntun: {enable: true}\n"},
		{"sing-box-direct-action", core.EngineSingBox, `{"inbounds":[{"tag":"a","type":"http","listen_port":21001}],"outbounds":[{"type":"direct","tag":"d"}],"route":{"rules":[{"action":"direct"}]}}`},
		{"sing-box-implicit-route", core.EngineSingBox, `{"inbounds":[{"tag":"a","type":"http","listen_port":21001}],"outbounds":[{"type":"direct","tag":"d"}],"route":{"rules":[{"action":"route","domain":["example.test"]}]}}`},
		{"sing-box-cased-detour", core.EngineSingBox, `{"inbounds":[{"tag":"a","type":"http","listen_port":21001}],"outbounds":[{"type":"direct","tag":"d","Detour":"other"},{"type":"direct","tag":"other"}]}`},
		{"sing-box-duplicate-field-fallback", core.EngineSingBox, `{"inbounds":[{"tag":"a","type":"http","listen_port":21001}],"outbounds":[{"type":"direct","tag":"d"}],"route":{"rules":[{"action":"direct","action":"route","outbound":"d"}]}}`},
		{"sing-box-inbound-detour", core.EngineSingBox, `{"inbounds":[{"tag":"a","type":"http","listen_port":21001,"detour":"b"},{"tag":"b","type":"http","listen_port":21002}],"outbounds":[{"type":"direct","tag":"d"}]}`},
		{"sing-box-empty-mutable-api", core.EngineSingBox, `{"inbounds":[],"experimental":{"clash_api":{"external_controller":"127.0.0.1:9090"}}}`},
		{"xray-duplicate-field", core.EngineXray, `{"inbounds":[{"tag":"a","port":21001,"Port":21002}],"outbounds":[{"protocol":"freedom"}]}`},
		{"xray-fallback", core.EngineXray, `{"inbounds":[{"tag":"a","port":21001,"protocol":"trojan","settings":{"fallbacks":[{"dest":22}]}}],"outbounds":[{"protocol":"freedom"}]}`},
		{"xray-reverse", core.EngineXray, `{"inbounds":[{"tag":"a","port":21001}],"outbounds":[{"protocol":"freedom"}],"reverse":{"bridges":[]}}`},
		{"xray-dynamic-listener", core.EngineXray, `{"inbounds":[{"tag":"a","port":21001,"protocol":"vmess","allocate":{"strategy":"random","concurrency":4}}],"outbounds":[{"protocol":"freedom"}]}`},
		{"xray-empty-mutable-api", core.EngineXray, `{"inbounds":[],"api":{"tag":"api","listen":"127.0.0.1:10085","services":["HandlerService"]}}`},
		{"ss-rust-manager", core.EngineShadowsocksRust, `{"server_port":21001,"manager_address":"127.0.0.1:9001"}`},
		{"ss-rust-double-listener", core.EngineShadowsocksRust, `{"server_port":21001,"servers":[{"server_port":21002}]}`},
		{"ss-rust-empty-disguised-manager", core.EngineShadowsocksRust, `{"servers":[],"manager_address":"127.0.0.1:9001"}`},
		{"ss-rust-empty-disguised-listener", core.EngineShadowsocksRust, `{"servers":[],"server_port":21001}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateIndependentEgress(test.engine, test.content); err == nil {
				t.Fatal("accepted an independent-egress bypass")
			}
		})
	}
}

func TestIndependentEgressAllowsExplicitFinalInboundDeletion(t *testing.T) {
	for engine, content := range map[core.Engine]string{
		core.EngineMihomo:          "listeners: []\nproxies: []\nrules: ['MATCH,DIRECT']\n",
		core.EngineXray:            `{"inbounds":[],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`,
		core.EngineSingBox:         `{"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}]}`,
		core.EngineShadowsocksRust: `{"servers":[],"mode":"tcp_and_udp","timeout":300,"ipv6_first":false}`,
	} {
		plan, err := PrepareIndependentEgress(engine, content)
		if err != nil || plan.Source != "disabled" || len(plan.Ports) != 0 || plan.Content != content {
			t.Fatalf("%s final inbound deletion rejected: %+v %v", engine, plan, err)
		}
	}
}

func TestIndependentEgressAccountsForProxyUsingStatisticsName(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		content := `{"inbounds":[{"tag":"a","protocol":"http","port":21001},{"tag":"qch-stat-api","protocol":"http","port":21002}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`
		if engine == core.EngineSingBox {
			content = `{"inbounds":[{"tag":"a","type":"http","listen_port":21001},{"tag":"qch-stat-api","type":"http","listen_port":21002}],"outbounds":[{"type":"direct","tag":"direct"}]}`
		}
		plan, err := PrepareAccounting(engine, content)
		if err != nil || len(plan.Ports) != 2 || plan.Ports[1].Port != 21002 || len(plan.Ports[1].Outbounds) != 1 {
			t.Fatalf("%s statistics name hid a real proxy: %+v %v", engine, plan.Ports, err)
		}
	}
}

func TestIndependentEgressMihomoDefaultIsPerListener(t *testing.T) {
	plan, err := PrepareAccounting(core.EngineMihomo, "listeners: [{name: a, type: http, port: 21001}, {name: b, type: http, port: 21002}]\nrules: [DOMAIN,example.test,DIRECT]\n")
	if err == nil {
		t.Fatal("malformed routing array accepted")
	}
	plan, err = PrepareAccounting(core.EngineMihomo, "listeners: [{name: a, type: http, port: 21001}, {name: b, type: http, port: 21002}]\nrules: ['DOMAIN,example.test,DIRECT']\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range plan.Ports {
		if !strings.Contains(plan.Content, "IN-NAME,"+port.Inbound+","+accountingTag(port.Port, "DIRECT")) {
			t.Fatalf("listener %s has no independent default exit", port.Inbound)
		}
	}
	again, err := PrepareAccounting(core.EngineMihomo, plan.Content)
	if err != nil || again.Content != plan.Content {
		t.Fatalf("default route is not repeatable: %v", err)
	}
}
