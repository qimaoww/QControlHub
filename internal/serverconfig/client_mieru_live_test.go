package serverconfig

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// Mieru's transport network and the traffic carried inside it are independent.
func TestMieruTransportAndPayloadNetworksLive(t *testing.T) {
	binary := nativeWSTestBinary(t, core.EngineMihomo)
	for _, transport := range []string{"TCP", "UDP"} {
		for _, payload := range []string{"tcp", "udp"} {
			t.Run(transport+"/"+payload, func(t *testing.T) {
				protocol, _ := FindProtocol(core.EngineMihomo, ProtocolMieru)
				input, err := NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.Listen, input.Port, input.MieruTransport = "127.0.0.1", singBoxFreePort(t), transport
				input.ClientAddress, input.ClientPort = "127.0.0.1", input.Port
				source, err := Generate(core.EngineMihomo, input)
				if err != nil {
					t.Fatal(err)
				}
				parsed, ok := Parse(core.EngineMihomo, source)
				if !ok || parsed.MieruTransport != transport {
					t.Fatal("Mieru transport lost during readback")
				}
				metadata, err := MarshalClientMetadata(input)
				if err != nil {
					t.Fatal(err)
				}
				if err := ApplyClientMetadata(&parsed, metadata); err != nil {
					t.Fatal(err)
				}
				ready := singBoxFreePort(t)
				// Probe the fixture controller because a Mieru UDP server has no
				// TCP protocol listener. Payload traffic remains on Mieru's port.
				source += fmt.Sprintf("external-controller: 127.0.0.1:%d\n", ready)
				serverLog := nativeWSOriginRun(t, core.EngineMihomo, binary, source, ready, "")
				outbound, err := BuildClientOutbound(core.EngineMihomo, parsed, "unused.example.test", "")
				if err != nil {
					t.Fatal(err)
				}
				var proxy map[string]any
				if err := json.Unmarshal(outbound, &proxy); err != nil {
					t.Fatal(err)
				}
				echo, port := singBoxEcho(t, payload), singBoxFreePort(t)
				client, err := yaml.Marshal(map[string]any{
					"listeners": []any{map[string]any{"type": "tunnel", "name": "payload", "listen": "127.0.0.1", "port": port, "network": []string{"tcp", "udp"}, "target": fmt.Sprintf("127.0.0.1:%d", echo)}},
					"proxies":   []any{proxy}, "rules": []string{"MATCH,peer"},
				})
				if err != nil {
					t.Fatal(err)
				}
				clientLog := nativeWSOriginRun(t, core.EngineMihomo, binary, string(client), port, "")
				if err := catalogEchoRoundTrip(payload, port); err != nil {
					t.Fatalf("Mieru transport=%s payload=%s: %v\nserver: %s\nclient: %s", transport, payload, err, serverLog(), clientLog())
				}
			})
		}
	}
}
