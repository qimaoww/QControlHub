package serverconfig

import (
	"encoding/json"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"strings"
	"testing"
)

func TestDiscoverConnectionListeners(t *testing.T) {
	tests := []struct {
		engine            core.Engine
		content, protocol string
		port              int
	}{
		{core.EngineMihomo, "listeners:\n  - name: entry\n    type: trojan\n    port: 443\n    users:\n      secret: secret-password\n", "trojan", 443},
		{core.EngineXray, `{"inbounds":[{"tag":"entry","protocol":"vless","port":443,"settings":{"clients":[{"id":"secret-password"}]}}]}`, "vless", 443},
		{core.EngineSingBox, `{"inbounds":[{"tag":"entry","type":"hysteria2","listen_port":443,"users":[{"password":"secret-password"}]}]}`, "hysteria2", 443},
		{core.EngineShadowsocksRust, `{"server":"::","server_port":8388,"method":"aes-128-gcm","password":"secret-password"}`, "shadowsocks", 8388},
	}
	for _, test := range tests {
		t.Run(string(test.engine), func(t *testing.T) {
			listeners, err := DiscoverConnectionListeners(test.engine, test.content)
			if err != nil || len(listeners) != 1 || listeners[0].Protocol != test.protocol || listeners[0].Port != test.port {
				t.Fatalf("listeners=%+v err=%v", listeners, err)
			}
			encoded, _ := json.Marshal(listeners)
			if strings.Contains(string(encoded), "secret-password") {
				t.Fatal("listener metadata leaked credentials")
			}
		})
	}
	if _, err := DiscoverConnectionListeners(core.EngineXray, "{"); err == nil {
		t.Fatal("invalid config accepted")
	}
}

func TestConnectionListenersKeepProtocolsOnSharedPort(t *testing.T) {
	listeners, err := DiscoverConnectionListeners(core.EngineSingBox, `{"inbounds":[{"tag":"tcp-entry","type":"trojan","listen_port":443},{"tag":"udp-entry","type":"hysteria2","listen_port":443}]}`)
	if err != nil || len(listeners) != 2 {
		t.Fatalf("listeners=%+v %v", listeners, err)
	}
	kinds := map[string]string{}
	for _, listener := range listeners {
		kinds[listener.Inbound] = listener.Protocol
	}
	if kinds["tcp-entry"] != "trojan" || kinds["udp-entry"] != "hysteria2" {
		t.Fatalf("protocols mixed on same port: %+v", listeners)
	}
}
