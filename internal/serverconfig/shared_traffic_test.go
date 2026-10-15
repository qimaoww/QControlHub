package serverconfig

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedTrafficEndpointsFailClosed(t *testing.T) {
	for _, test := range []struct {
		engine  core.Engine
		content string
		valid   bool
	}{
		{core.EngineMihomo, "listeners: [{name: one, type: trojan, port: 14443}]", true},
		{core.EngineXray, `{"inbounds":[{"tag":"qch-stat-api","protocol":"vless","port":14443}]}`, true},
		{core.EngineSingBox, `{"inbounds":[{"tag":"one","type":"hysteria2","listen_port":14443}]}`, true},
		{core.EngineShadowsocksRust, `{"server":"::","server_port":14443,"password":"test","method":"aes-256-gcm"}`, true},
		{core.EngineMihomo, "listeners: [{name: one, type: trojan, port: 14443}]\nexternal-controller: 0.0.0.0:9090", false},
		{core.EngineMihomo, "listeners: [{name: one, type: trojan, port: 14443}]\ntun: {enable: true}", false},
		{core.EngineMihomo, "listeners: [{name: one, type: future, port: 14443}]", false},
		{core.EngineMihomo, "listeners: [{name: one, type: trojan, port: 14443}]\ndns: {listen: ':53'}", false},
		{core.EngineXray, `{"inbounds":[{"tag":"one","protocol":"vless","port":"10000-20000"}]}`, false},
		{core.EngineXray, `{"inbounds":[{"tag":"one","protocol":"vless","port":14443,"Port":22222}]}`, false},
		{core.EngineXray, `{"inbounds":[{"tag":"one","protocol":"vless","port":14443,"port":22222}]}`, false},
		{core.EngineXray, `{"inbounds":[{"tag":"one","protocol":"vless","port":14443,"allocate":{"strategy":"random"}}]}`, false},
		{core.EngineXray, `{"api":{"listen":"127.0.0.1:10085","services":["HandlerService"]},"inbounds":[{"tag":"one","protocol":"vless","port":14443}]}`, false},
		{core.EngineXray, `{"inbounds":[{"tag":"one","protocol":"vless","port":10085}]}`, false},
		{core.EngineSingBox, `{"inbounds":[{"type":"tun","listen_port":14443}]}`, false},
		{core.EngineSingBox, `{"inbounds":[{"type":"vless","listen_port":14443}],"services":[{"type":"ssm-api"}]}`, false},
		{core.EngineSingBox, `{"inbounds":[{"type":"vless","listen_port":14443}],"experimental":{"clash_api":{"external_controller":"127.0.0.1:9090"}}}`, false},
		{core.EngineShadowsocksRust, `{"server_port":14443,"plugin":"custom"}`, false},
		{core.EngineShadowsocksRust, `{"server_port":14443,"manager_address":"127.0.0.1:6001"}`, false},
		{core.EngineShadowsocksRust, `{"server_port":14443,"servers":[{"server_port":14444}]}`, false},
	} {
		t.Run(string(test.engine)+"/"+test.content, func(t *testing.T) {
			endpoints, err := SharedTrafficEndpoints(test.engine, test.content)
			if (err == nil) != test.valid {
				t.Fatalf("ports=%+v err=%v valid=%v", endpoints, err, test.valid)
			}
			if test.valid && (len(endpoints) != 1 || endpoints[0].Port != 14443 || endpoints[0].Protocol != core.TrafficProtocolBoth) {
				t.Fatalf("incomplete coverage: %+v", endpoints)
			}
		})
	}
	var entries []string
	for port := 20000; port < 20257; port++ {
		entries = append(entries, fmt.Sprintf("{name: p%d, type: trojan, port: %d}", port, port))
	}
	if _, err := SharedTrafficEndpoints(core.EngineMihomo, "listeners: ["+strings.Join(entries, ",")+"]"); err == nil {
		t.Fatal("silently truncated excessive listeners")
	}
}

func TestSharedTrafficSupportsGeneratedServerPresets(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		for _, protocol := range Protocols(engine) {
			if protocol.Key == ProtocolTailscale {
				// DERP relay traffic has no port-level counter; it is validated
				// through the endpoint status API instead of shared port stats.
				continue
			}
			input, err := nativeTLSFixturePlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatalf("%s %s generate: %v", engine, protocol.Key, err)
			}
			if _, err := SharedTrafficEndpoints(engine, content); err != nil {
				t.Fatalf("%s %s shared: %v", engine, protocol.Key, err)
			}
			plan, err := PlanPresetAccounting(engine, content)
			if err != nil {
				t.Fatalf("%s %s accounting: %v", engine, protocol.Key, err)
			}
			if _, err := SharedTrafficEndpoints(engine, plan.Content); err != nil {
				t.Fatalf("%s %s shared prepared: %v", engine, protocol.Key, err)
			}
		}
	}
}

func TestSharedTrafficFinalInboundDeletion(t *testing.T) {
	for engine, content := range map[core.Engine]string{
		core.EngineMihomo:          "listeners: []\nproxies: []\nrules: ['MATCH,DIRECT']\n",
		core.EngineXray:            `{"inbounds":[],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`,
		core.EngineSingBox:         `{"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}],"experimental":{"v2ray_api":{"listen":"127.0.0.1:10086"}}}`,
		core.EngineShadowsocksRust: `{"servers":[],"mode":"tcp_and_udp","timeout":300}`,
	} {
		t.Run(string(engine), func(t *testing.T) {
			endpoints, err := SharedTrafficEndpoints(engine, content)
			if err != nil || len(endpoints) != 0 {
				t.Fatalf("explicit empty configuration: %+v %v", endpoints, err)
			}
			runtime, err := SharedInstanceRuntimeContent(engine, content)
			if err != nil || strings.Contains(runtime, "10086") {
				t.Fatalf("empty shared runtime retained statistics listener: %s %v", runtime, err)
			}
		})
	}
	for _, test := range []struct {
		engine  core.Engine
		content string
	}{
		{core.EngineSingBox, `{}`},
		{core.EngineSingBox, `{"inbounds":null}`},
		{core.EngineSingBox, `{"inbounds":[],"Inbounds":[{"type":"http","listen_port":22}]}`},
		{core.EngineSingBox, `{"inbounds":[],"endpoints":[{"type":"tun"}]}`},
		{core.EngineSingBox, `{"inbounds":[],"experimental":{"clash_api":{"external_controller":"127.0.0.1:9090"}}}`},
		{core.EngineSingBox, `{"inbounds":[],"services":[{"type":"ssm-api"}]}`},
		{core.EngineXray, `{"inbounds":[],"api":{"listen":"127.0.0.1:10085","services":["HandlerService"]}}`},
		{core.EngineMihomo, "listeners: []\nmixed-port: 21001\n"},
		{core.EngineMihomo, "listeners: []\ntun: {enable: true}\n"},
		{core.EngineShadowsocksRust, `{"servers":[],"server_port":21001}`},
		{core.EngineShadowsocksRust, `{"servers":[],"manager_address":"127.0.0.1:9001"}`},
	} {
		if endpoints, err := SharedTrafficEndpoints(test.engine, test.content); err == nil {
			t.Fatalf("accepted hidden/malformed ingress: %s %s: %+v", test.engine, test.content, endpoints)
		}
	}
}
