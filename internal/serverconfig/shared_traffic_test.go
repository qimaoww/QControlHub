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
			input, err := NewPlan(protocol)
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
