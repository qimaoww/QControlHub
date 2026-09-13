package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientOutboundsFromDeployedPresets(t *testing.T) {
	for _, source := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		for _, protocol := range Protocols(source) {
			for _, target := range []core.Engine{core.EngineXray, core.EngineSingBox} {
				t.Run(string(source)+"/"+protocol.Key+"/"+string(target), func(t *testing.T) {
					input, err := NewPlan(protocol)
					if err != nil {
						t.Fatal(err)
					}
					input.CertificatePath, input.PrivateKeyPath = "/private/server.crt", "/private/server.key"
					outbound, err := BuildClientOutbound(target, input, "2001:db8::12", "tls.example.test")
					unsupported := isSnellProtocol(input.Protocol) || input.Protocol == ProtocolSudoku || input.Protocol == ProtocolPortForward ||
						target == core.EngineXray && (input.Protocol == ProtocolHy2 || input.Protocol == ProtocolTUIC || input.Protocol == ProtocolAnyTLS) ||
						target == core.EngineSingBox && (input.Transport == "xhttp" || isVLESSEncryptionProtocol(input.Protocol) || input.RealityMLDSA65Verify != "")
					if unsupported {
						if err == nil {
							t.Fatal("unsupported peer security/protocol silently downgraded")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					for _, secret := range []string{input.RealityPrivateKey, input.RealityMLDSA65Seed, input.VLESSDecryption, input.CertificatePath, input.PrivateKeyPath} {
						if secret != "" && secret != "none" && strings.Contains(string(outbound), secret) {
							t.Fatal("server private material leaked to an outbound")
						}
					}
					if !strings.Contains(string(outbound), "2001:db8::12") || !strings.Contains(string(outbound), input.Credential) {
						t.Fatal("peer address or client credential lost")
					}
					if input.RealityEnabled && !strings.Contains(string(outbound), input.RealityPublicKey) {
						t.Fatal("Reality client public key missing")
					}
					var out map[string]any
					if err := json.Unmarshal(outbound, &out); err != nil {
						t.Fatal(err)
					}
					root := map[string]any{"inbounds": []any{map[string]any{"tag": "local", "port": 2080, "listen_port": 2080}}, "outbounds": []any{out}}
					if target == core.EngineXray {
						root["routing"] = map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"local"}, "outboundTag": "peer"}}}
					} else {
						root["route"] = map[string]any{"rules": []any{map[string]any{"inbound": []string{"local"}, "outbound": "peer"}}}
					}
					content, _ := json.Marshal(root)
					plan, err := PrepareAccounting(target, string(content))
					if err != nil || plan.Source != "nft-dual" || plan.Ports[0].Mark != 0x51430820 {
						t.Fatalf("peer outbound cannot retain independent accounting: %v", err)
					}
				})
			}
		}
	}
}

func TestClientOutboundSecurityAndTransport(t *testing.T) {
	input := Input{Protocol: ProtocolVLESS, Credential: "123e4567-e89b-42d3-a456-426614174000", Port: 443, TLSEnabled: true, Transport: "websocket", TransportPath: "/peer"}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		out, err := BuildClientOutbound(engine, input, "node.example", "tls.example")
		if err != nil || !strings.Contains(string(out), "tls.example") || !strings.Contains(string(out), "/peer") || strings.Contains(string(out), "insecure") {
			t.Fatalf("TLS/transport changed: %v", err)
		}
		unknown := input
		unknown.Transport = "unknown"
		if _, err := BuildClientOutbound(engine, unknown, "node.example", "tls.example"); err == nil {
			t.Fatal("unknown transport downgraded to TCP")
		}
		if _, err := BuildClientOutbound(engine, input, "0.0.0.0", "tls.example"); err == nil {
			t.Fatal("listener wildcard used as a remote destination")
		}
	}
}
