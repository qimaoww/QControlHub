package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCatalogClientConnectionRoundTrip(t *testing.T) {
	for _, engine := range core.AllEngines() {
		for _, protocol := range Protocols(engine) {
			if protocol.PortForward || protocol.Transports[0] == "xhttp" {
				continue
			}
			t.Run(string(engine)+"/"+protocol.Key, func(t *testing.T) {
				input, err := nativeTLSFixturePlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.ClientAddress, input.ClientPort = "2001:db8::42", 34443
				if input.Transport == "websocket" || input.Transport == "http" || input.Transport == "httpupgrade" {
					input.ClientHost = "edge.example.test"
				}
				if input.TLSEnabled {
					input.TLSClientALPN = "http/1.1"
				}
				if input.Protocol == ProtocolHy2 {
					input.TLSClientALPN = "h3"
				}
				if input.Protocol == ProtocolVLESSHTTP2Reality {
					input.ClientHost = "edge.example.test"
				}
				source, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := MarshalClientMetadata(input)
				if err != nil {
					t.Fatal(err)
				}
				parsed, ok := Parse(engine, source)
				if !ok {
					t.Fatal("source not recognized")
				}
				if err := ApplyClientMetadata(&parsed, encoded); err != nil {
					t.Fatal(err)
				}
				if parsed.ClientAddress != input.ClientAddress || parsed.ClientPort != input.ClientPort || parsed.ClientHost != input.ClientHost || parsed.TLSClientALPN != input.TLSClientALPN {
					t.Fatalf("lost dial options: %+v", parsed)
				}
				profile, err := BuildClientProfile(parsed, "old.example.test", "tls.example.test")
				if err != nil {
					t.Fatal(err)
				}
				fields := map[string]string{}
				for _, field := range profile.Fields {
					fields[field.Label] = field.Value
				}
				if fields["服务器"] != input.ClientAddress || fields["端口"] != "34443" {
					t.Fatalf("wrong client endpoint: %+v", fields)
				}
				if !isSnellProtocol(input.Protocol) && input.Protocol != ProtocolMieru && input.Protocol != ProtocolSudoku && !protocol.UsesWireGuard && !protocol.UsesVLESSEncryption {
					outbound, err := BuildClientOutbound(core.EngineSingBox, parsed, "old.example.test", "tls.example.test")
					if err != nil {
						t.Fatal(err)
					}
					var out map[string]any
					_ = json.Unmarshal(outbound, &out)
					if out["server"] != input.ClientAddress || out["server_port"] != float64(input.ClientPort) {
						t.Fatalf("native outbound ignored override: %s", outbound)
					}
				}
				// Export resolves copies; it must not change the server listener.
				again, err := Generate(engine, parsed)
				if err != nil || again != source {
					t.Fatalf("client options changed server: %v", err)
				}
				stale := parsed
				stale.ClientAddress, stale.ClientPort, stale.ClientHost, stale.TLSClientALPN = "", 0, "", ""
				stale.Port++
				before := stale
				if err := ApplyClientMetadata(&stale, encoded); err != nil {
					t.Fatal(err)
				}
				if stale.ClientAddress != "" || stale.ClientPort != 0 || stale.ClientHost != before.ClientHost {
					t.Fatal("stale options attached to new endpoint")
				}
				parsed.ClientAddress, parsed.ClientPort, parsed.ClientHost, parsed.TLSClientALPN = "", 0, "", ""
				cleared, err := MarshalClientMetadataWithPrevious(parsed, encoded)
				if err != nil || strings.Contains(cleared, "client_connection") {
					t.Fatal("explicit clear restored old options", err)
				}
			})
		}
	}
}

func TestMihomoQUICALPNRoundTrip(t *testing.T) {
	for _, key := range []string{ProtocolTUIC, ProtocolHy2} {
		protocol, _ := FindProtocol(core.EngineMihomo, key)
		input, _ := nativeTLSFixturePlan(protocol)
		input.TLSALPN = "h2,http/1.1"
		source, err := Generate(core.EngineMihomo, input)
		if err != nil {
			t.Fatal(err)
		}
		parsed, ok := Parse(core.EngineMihomo, source)
		if !ok || parsed.TLSALPN != input.TLSALPN {
			t.Fatal("server ALPN was replaced", parsed.TLSALPN)
		}
	}
}

func TestClientConnectionBindingUsesNativeTransportDefaults(t *testing.T) {
	for _, engine := range core.AllEngines() {
		for _, transport := range []string{"", "raw"} {
			t.Run(string(engine)+"/"+transport, func(t *testing.T) {
				protocol, _ := FindProtocol(engine, ProtocolSS2022)
				input, err := NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.Transport, input.TransportPath = transport, "/inactive-path"
				input.ClientAddress, input.ClientPort = "edge.example.test", 34443
				source, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := MarshalClientMetadata(input)
				if err != nil {
					t.Fatal(err)
				}
				parsed, ok := Parse(engine, source)
				if !ok {
					t.Fatal("source not recognized")
				}
				if err := ApplyClientMetadata(&parsed, metadata); err != nil {
					t.Fatal(err)
				}
				if parsed.ClientAddress != input.ClientAddress || parsed.ClientPort != input.ClientPort {
					t.Fatal("native transport normalization lost saved client overrides")
				}
			})
		}
	}
}

func TestNewTLSPlansRequireRealCertificateInput(t *testing.T) {
	for _, engine := range core.AllEngines() {
		for _, protocol := range Protocols(engine) {
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			if input.CertificatePath != "" || input.PrivateKeyPath != "" {
				t.Fatal("plan invents certificate paths")
			}
			if input.TLSEnabled && !proxiedTLS(input) {
				if _, err := Generate(engine, input); err == nil {
					t.Fatalf("%s/%s accepted missing TLS certificate", engine, protocol.Key)
				}
			}
		}
	}
}

func TestRealityTargetAndNamesRoundTrip(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineMihomo, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			protocol, _ := FindProtocol(engine, ProtocolVLESS)
			input, _ := NewPlan(protocol)
			input.RealityServerName = "one.example.test"
			input.RealityServerNames = "one.example.test"
			if engine != core.EngineSingBox {
				input.RealityServerNames += ",two.example.test"
			}
			input.RealityTarget = "cover.example.test:8443"
			source, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(engine, source)
			if !ok || parsed.RealityTarget != input.RealityTarget || strings.Join(xhttpRealityNames(parsed), ",") != input.RealityServerNames {
				t.Fatalf("Reality options lost: %+v", parsed)
			}
			parsed.RealityServerNames = "wrong.example.test"
			if _, err := Generate(engine, parsed); err == nil {
				t.Fatal("client SNI missing from server list accepted")
			}
		})
	}
}

func TestRealityClientSNISelectionSurvivesReadback(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineMihomo} {
		t.Run(string(engine), func(t *testing.T) {
			protocol, _ := FindProtocol(engine, ProtocolVLESS)
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.RealityServerName = "selected.example.test"
			input.RealityServerNames = "first.example.test,selected.example.test"
			source, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := MarshalClientMetadata(input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(engine, source)
			if !ok {
				t.Fatal("source not recognized")
			}
			if err := ApplyClientMetadata(&parsed, metadata); err != nil {
				t.Fatal(err)
			}
			if parsed.RealityServerName != input.RealityServerName {
				t.Fatal("server allow-list order replaced the selected client SNI")
			}
			parsed.RealityServerName, parsed.RealityServerNames = "changed.example.test", "changed.example.test"
			if err := ApplyClientMetadata(&parsed, metadata); err != nil {
				t.Fatal(err)
			}
			if parsed.RealityServerName != "changed.example.test" {
				t.Fatal("metadata restored a client SNI removed from native source")
			}
		})
	}
}

func TestMihomoRejectsIgnoredServerALPN(t *testing.T) {
	for _, key := range []string{ProtocolVMess, ProtocolTrojan, ProtocolAnyTLS, ProtocolXHTTPTLS} {
		protocol, _ := FindProtocol(core.EngineMihomo, key)
		input, _ := nativeTLSFixturePlan(protocol)
		input.TLSALPN = "qch-custom"
		if _, err := Generate(core.EngineMihomo, input); err == nil {
			t.Fatalf("%s ignored unsupported server ALPN", key)
		}
	}
}
