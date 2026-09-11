package serverconfig

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

func TestMihomoClientExportProtocolMatrix(t *testing.T) {
	for _, engine := range core.AllEngines() {
		for _, protocol := range Protocols(engine) {
			if protocol.PortForward {
				continue
			}
			t.Run(string(engine)+"/"+protocol.Key, func(t *testing.T) {
				input, err := NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				content, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				parsed, ok := Parse(engine, content)
				if !ok {
					t.Fatal("generated server configuration did not parse")
				}
				metadata, err := MarshalClientMetadata(input)
				if err != nil {
					t.Fatal(err)
				}
				if err := ApplyClientMetadata(&parsed, metadata); err != nil {
					t.Fatal(err)
				}
				profile, err := BuildClientProfileNamed(parsed, "2001:db8::10", "tls.example.test", "Tokyo: [private]")
				if err != nil || profile.MihomoError != "" || profile.Mihomo == "" {
					t.Fatalf("Mihomo export unavailable: %v %s", err, profile.MihomoError)
				}
				if strings.ContainsAny(profile.Mihomo, "\r\n") || !strings.HasPrefix(profile.Mihomo, "{") {
					t.Fatalf("not a single-line proxy map: %q", profile.Mihomo)
				}
				var proxy map[string]any
				if err := yaml.Unmarshal([]byte(profile.Mihomo), &proxy); err != nil {
					t.Fatal(err)
				}
				if proxy["name"] != "Tokyo: [private]" || proxy["server"] != "2001:db8::10" || intValue(proxy["port"]) != input.Port {
					t.Fatalf("client identity changed: %#v", proxy)
				}
				wantType := protocol.Key
				switch protocol.Key {
				case ProtocolShadowsocks, ProtocolSS2022:
					wantType = "ss"
					if proxy["cipher"] != input.Method || proxy["password"] != input.Credential {
						t.Fatal("Shadowsocks credentials changed")
					}
				case ProtocolVLESS, ProtocolVLESSXHTTP, ProtocolVLESSEncTCP, ProtocolVLESSEncXHTTP:
					wantType = "vless"
					if proxy["uuid"] != input.Credential {
						t.Fatal("VLESS UUID changed")
					}
					if input.RealityEnabled {
						reality := mapValue(proxy["reality-opts"])
						if proxy["tls"] != true || reality["public-key"] != input.RealityPublicKey || reality["short-id"] != input.RealityShortID || proxy["servername"] != input.RealityServerName {
							t.Fatal("Reality client settings were lost")
						}
					}
					if isVLESSEncryptionProtocol(input.Protocol) && proxy["encryption"] != input.VLESSEncryption {
						t.Fatal("VLESS client encryption was lost")
					}
				case ProtocolSnell, ProtocolSnellShadowTLS:
					wantType = "snell"
					if proxy["psk"] != input.Credential || intValue(proxy["version"]) != 5 || proxy["tfo"] != true {
						t.Fatal("Snell v5 client settings were lost")
					}
					if protocol.Key == ProtocolSnellShadowTLS {
						opts := mapValue(proxy["obfs-opts"])
						if opts["mode"] != "shadow-tls" || opts["password"] != input.SnellShadowTLSPassword || opts["host"] != input.SnellObfsHost || intValue(opts["version"]) != 3 {
							t.Fatal("ShadowTLS client settings were lost")
						}
					}
				case ProtocolHy2:
					wantType = "hysteria2"
				case ProtocolTUIC:
					if proxy["uuid"] != input.Credential || proxy["password"] != input.SecondaryCredential || proxy["congestion-controller"] != "bbr" {
						t.Fatal("TUIC client settings were lost")
					}
				case ProtocolSudoku:
					if proxy["key"] != input.SudokuClientKey {
						t.Fatal("Sudoku client key was not hydrated")
					}
				}
				if proxy["type"] != wantType {
					t.Fatalf("proxy type = %v, want %s", proxy["type"], wantType)
				}
				for _, secret := range []string{input.RealityPrivateKey, input.RealityMLDSA65Seed, input.VLESSDecryption, input.PrivateKeyPath, input.CertificatePath} {
					if secret != "" && strings.Contains(profile.Mihomo, secret) {
						t.Fatal("server-only private key or file path leaked into subscription")
					}
				}
				if !profile.SubscriptionCompatible || profile.URI == "" {
					t.Fatal("adding Mihomo export broke URL/native export")
				}
			})
		}
	}
}

func TestMihomoClientTransportOptions(t *testing.T) {
	for _, protocolKey := range []string{ProtocolVMess, ProtocolTrojan} {
		for _, transport := range []string{"websocket", "grpc"} {
			t.Run(protocolKey+"/"+transport, func(t *testing.T) {
				protocol, _ := FindProtocol(core.EngineXray, protocolKey)
				input, err := NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.Transport, input.TransportPath, input.TLSEnabled = transport, "/private", true
				profile, err := BuildClientProfile(input, "edge.example.test", "tls.example.test")
				if err != nil || profile.MihomoError != "" {
					t.Fatalf("export transport: %v %s", err, profile.MihomoError)
				}
				var proxy map[string]any
				if err := yaml.Unmarshal([]byte(profile.Mihomo), &proxy); err != nil {
					t.Fatal(err)
				}
				if transport == "websocket" {
					opts := mapValue(proxy["ws-opts"])
					if proxy["network"] != "ws" || opts["path"] != input.TransportPath || mapValue(opts["headers"])["Host"] != "tls.example.test" {
						t.Fatalf("WebSocket options: %#v", proxy)
					}
				} else if proxy["network"] != "grpc" || mapValue(proxy["grpc-opts"])["grpc-service-name"] != input.TransportPath {
					t.Fatalf("gRPC options: %#v", proxy)
				}
				key := "servername"
				if protocolKey == ProtocolTrojan {
					key = "sni"
				}
				if proxy[key] != "tls.example.test" || proxy["skip-cert-verify"] == true {
					t.Fatal("TLS server name or certificate verification changed")
				}
			})
		}
	}
}

func TestMihomoRejectsUnsupportedRealityVerificationWithoutDowngrade(t *testing.T) {
	protocol, _ := FindProtocol(core.EngineXray, ProtocolVLESS)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.RealityMLDSA65Seed = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("q", 32)))
	profile, err := BuildClientProfile(input, "edge.example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Mihomo != "" || !strings.Contains(profile.MihomoError, "ML-DSA-65") || !strings.Contains(profile.URI, "pqv=") {
		t.Fatal("unsupported Reality verification was silently removed or URL export broke")
	}
}
