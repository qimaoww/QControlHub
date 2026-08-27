package serverconfig

import (
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDiscoverTrafficPortsIncludesExistingUnsupportedListeners(t *testing.T) {
	t.Parallel()
	fixtures := []struct {
		engine  core.Engine
		content string
		want    map[int]core.TrafficProtocol
	}{
		{
			engine:  core.EngineMihomo,
			content: "mixed-port: 7890\nlisteners:\n  - name: custom-tunnel\n    type: future-protocol\n    port: 24443\n    network: udp\n",
			want:    map[int]core.TrafficProtocol{7890: core.TrafficProtocolBoth, 24443: core.TrafficProtocolUDP},
		},
		{
			engine:  core.EngineXray,
			content: `{"inbounds":[{"tag":"reality-in","protocol":"vless","port":443},{"tag":"custom","protocol":"future","port":"8443","settings":{"network":"udp"}}]}`,
			want:    map[int]core.TrafficProtocol{443: core.TrafficProtocolTCP, 8443: core.TrafficProtocolUDP},
		},
		{
			engine:  core.EngineSingBox,
			content: `{"inbounds":[{"tag":"hy2-in","type":"hysteria2","listen_port":2096},{"tag":"tun","type":"tun"}]}`,
			want:    map[int]core.TrafficProtocol{2096: core.TrafficProtocolUDP},
		},
		{
			engine:  core.EngineShadowsocksRust,
			content: `{"mode":"tcp_and_udp","servers":[{"server_port":8388,"method":"aes-256-gcm"},{"name":"udp-only","server_port":8389,"mode":"udp_only"}]}`,
			want:    map[int]core.TrafficProtocol{8388: core.TrafficProtocolBoth, 8389: core.TrafficProtocolUDP},
		},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(string(fixture.engine), func(t *testing.T) {
			t.Parallel()
			got := DiscoverTrafficPorts(fixture.engine, fixture.content)
			if len(got) != len(fixture.want) {
				t.Fatalf("DiscoverTrafficPorts(%s) = %+v", fixture.engine, got)
			}
			for _, endpoint := range got {
				if want := fixture.want[endpoint.Port]; endpoint.Protocol != want {
					t.Errorf("port %d protocol = %q, want %q", endpoint.Port, endpoint.Protocol, want)
				}
			}
		})
	}
}

func TestDiscoverTrafficPortsDoesNotReturnConfigurationSecrets(t *testing.T) {
	t.Parallel()
	ports := DiscoverTrafficPorts(core.EngineXray, `{"inbounds":[{"tag":"public-name","protocol":"vless","port":443,"settings":{"clients":[{"id":"secret-uuid"}]}}]}`)
	if len(ports) != 1 || ports[0].Name != "public-name" || ports[0].Port != 443 {
		t.Fatalf("discovered ports = %+v", ports)
	}
}
