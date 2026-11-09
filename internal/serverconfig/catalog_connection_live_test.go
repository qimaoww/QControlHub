package serverconfig

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// Complement the sing-box server matrix with actual other-core servers.
// Only the external Reality handshake service is replaced by a local fixture.
func TestCrossCoreCatalogConnectionsLive(t *testing.T) {
	directory := os.Getenv("QCH_TEST_MANAGED_CORE_DIR")
	if directory == "" {
		t.Skip("requires pinned native core fixture")
	}
	certificate, certPath, keyPath := singBoxTestCertificate(t)
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	cover := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	cover.EnableHTTP2 = true
	cover.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13, CurvePreferences: []tls.CurveID{tls.X25519}, NextProtos: []string{"h2"}}
	cover.StartTLS()
	t.Cleanup(cover.Close)
	for _, engine := range []core.Engine{core.EngineXray, core.EngineMihomo, core.EngineShadowsocksRust} {
		for _, protocol := range Protocols(engine) {
			if protocol.PortForward || protocol.UsesWireGuard || protocol.Key == ProtocolHy2 && engine != core.EngineXray || protocol.Key == ProtocolTUIC || isSnellProtocol(protocol.Key) || protocol.Key == ProtocolMieru || protocol.Key == ProtocolSudoku || protocol.Transports[0] == "xhttp" && protocol.Key != ProtocolVLESSXHTTP && protocol.Key != ProtocolVLESSEncXHTTP {
				continue
			}
			methods := protocol.Methods
			if len(methods) == 0 {
				methods = []string{""}
			}
			for _, transport := range protocol.Transports {
				for _, method := range methods {
					t.Run(string(engine)+"/"+protocol.Key+"/"+transport+"/"+method, func(t *testing.T) {
						input, err := nativeTLSFixturePlan(protocol)
						if err != nil {
							t.Fatal(err)
						}
						input.BlockMainlandDestination, input.BlockMainlandSource = false, false
						input.Listen, input.Port, input.Transport = "127.0.0.1", singBoxFreePort(t), transport
						input.TransportPath = "/custom-transport"
						if transport == "raw" {
							input.TransportPath = ""
						}
						if transport == "xhttp" {
							input.XHTTPMode = "stream-one"
						}
						if transport == "grpc" {
							input.TransportPath = "custom-service"
						}
						input.CertificatePath, input.PrivateKeyPath, input.TLSServerName = certPath, keyPath, "tls.example.test"
						if input.TLSEnabled {
							input.TLSClientCertificate = certificate
						}
						if method != "" {
							input.Method = method
							input.Credential, err = NewCredential(input.Protocol, method)
							if err != nil {
								t.Fatal(err)
							}
						}
						if input.RealityEnabled {
							input.RealityServerName = "tls.example.test"
							if transport != "xhttp" {
								input.RealityServerNames = "first.example.test,tls.example.test"
							}
						}
						input.ClientAddress, input.ClientPort = "127.0.0.1", input.Port
						source, err := Generate(engine, input)
						if err != nil {
							t.Fatal(err)
						}
						parsed, ok := Parse(engine, source)
						if !ok {
							t.Fatal("generated server unreadable")
						}
						metadata, err := MarshalClientMetadata(input)
						if err != nil {
							t.Fatal(err)
						}
						if err := ApplyClientMetadata(&parsed, metadata); err != nil {
							t.Fatal(err)
						}
						if parsed.ClientAddress != input.ClientAddress {
							t.Fatal("saved client endpoint lost")
						}
						if input.RealityEnabled {
							var root map[string]any
							if engine == core.EngineMihomo {
								_ = yaml.Unmarshal([]byte(source), &root)
								mapValue(firstMap(root["listeners"])["reality-config"])["dest"] = cover.Listener.Addr().String()
								encoded, _ := yaml.Marshal(root)
								source = string(encoded)
							} else {
								_ = json.Unmarshal([]byte(source), &root)
								settings := mapValue(mapValue(firstMap(root["inbounds"])["streamSettings"])["realitySettings"])
								settings["target"] = cover.Listener.Addr().String()
								encoded, _ := json.Marshal(root)
								source = string(encoded)
							}
						}
						binary := string(engine)
						if engine == core.EngineShadowsocksRust {
							binary = "ssserver"
						}
						serverLog := nativeWSOriginRun(t, engine, filepath.Join(directory, binary), source, input.Port, certPath)
						clientEngine := core.EngineSingBox
						if input.Transport == "xhttp" || protocol.UsesVLESSEncryption || engine == core.EngineXray && input.RealityEnabled {
							clientEngine = core.EngineXray
						}
						if engine == core.EngineMihomo && input.Transport == "xhttp" {
							clientEngine = core.EngineMihomo
						}
						for _, network := range []string{"tcp", "udp"} {
							t.Run(network, func(t *testing.T) {
								outbound, err := BuildClientOutbound(clientEngine, parsed, "unused.example.test", "")
								if err != nil {
									t.Fatal(err)
								}
								var out map[string]any
								_ = json.Unmarshal(outbound, &out)
								echo, port := singBoxEcho(t, network), singBoxFreePort(t)
								client := map[string]any{"inbounds": []any{map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": port, "network": network, "override_address": "127.0.0.1", "override_port": echo}}, "outbounds": []any{out}}
								if clientEngine == core.EngineXray {
									client["inbounds"] = []any{map[string]any{"tag": "local", "protocol": "tunnel", "listen": "127.0.0.1", "port": port, "settings": map[string]any{"allowedNetwork": "tcp,udp", "rewriteAddress": "127.0.0.1", "rewritePort": echo}}}
								}
								if clientEngine == core.EngineMihomo {
									client = map[string]any{"listeners": []any{map[string]any{"type": "tunnel", "name": "payload", "listen": "127.0.0.1", "port": port, "network": []string{"tcp", "udp"}, "target": fmt.Sprintf("127.0.0.1:%d", echo)}}, "proxies": []any{out}, "rules": []string{"MATCH,peer"}}
								}
								encoded, _ := json.Marshal(client)
								var clientLog func() string
								if clientEngine != core.EngineSingBox {
									clientLog = nativeWSOriginRun(t, clientEngine, filepath.Join(directory, string(clientEngine)), string(encoded), port, certPath)
								} else {
									clientLog = singBoxRun(t, filepath.Join(directory, "sing-box"), "catalog-client", string(encoded))
								}
								if err := catalogEchoRoundTrip(network, port); err != nil {
									t.Fatalf("%v\nserver: %s\nclient: %s", err, serverLog(), clientLog())
								}
							})
						}
					})
				}
			}
		}
	}
}

func TestPortForwardCatalogConnectionsLive(t *testing.T) {
	directory := os.Getenv("QCH_TEST_MANAGED_CORE_DIR")
	if directory == "" {
		t.Skip("requires pinned native core fixture")
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineMihomo, core.EngineSingBox} {
		for _, network := range []string{"tcp", "udp"} {
			t.Run(string(engine)+"/"+network, func(t *testing.T) {
				protocol, _ := FindProtocol(engine, ProtocolPortForward)
				input, err := NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := Generate(engine, input); err == nil {
					t.Fatal("missing target accepted")
				}
				input.Listen, input.Port = "127.0.0.1", singBoxFreePort(t)
				input.TargetAddress, input.TargetPort, input.Network = "127.0.0.1", singBoxEcho(t, network), "tcp,udp"
				source, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				parsed, ok := Parse(engine, source)
				if !ok || parsed.TargetAddress != input.TargetAddress || parsed.TargetPort != input.TargetPort || parsed.Network != input.Network {
					t.Fatalf("forward target lost: %+v", parsed)
				}
				logs := nativeWSOriginRun(t, engine, filepath.Join(directory, string(engine)), source, input.Port, "")
				if err := catalogEchoRoundTrip(network, input.Port); err != nil {
					t.Fatalf("%v: %s", err, logs())
				}
			})
		}
	}
}

// A bound Mihomo UDP socket can precede routing readiness. UDP has no delivery
// guarantee; require an actual reply within the bounded startup window.
func catalogEchoRoundTrip(network string, port int) error {
	timeout := 500 * time.Millisecond
	if network == "tcp" {
		timeout = 12 * time.Second
	}
	var err error
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); {
		if err = singBoxEchoRoundTripTimeout(network, port, timeout); err == nil {
			return nil
		}
	}
	return err
}
