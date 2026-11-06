package serverconfig

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// A local hostname-routing TLS edge, not a Cloudflare service. Keep this in the
// existing live matrix so CI exercises actual VLESS payloads through the edge.
func testSingBoxWebSocketTLSAddressOverride(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineSingBox, core.EngineXray, core.EngineMihomo} {
		for _, protocol := range nativeWebSocketTLSProtocols(engine) {
			t.Run(string(engine)+"/"+protocol.Key, func(t *testing.T) { testNativeWebSocketTLSAddressOverride(t, engine, protocol) })
		}
	}
}

func nativeWebSocketTLSProtocols(engine core.Engine) []Protocol {
	var result []Protocol
	for _, p := range Protocols(engine) {
		if !p.SupportsTLS || p.UsesReality {
			continue
		}
		for _, transport := range p.Transports {
			if transport == "websocket" {
				result = append(result, p)
				break
			}
		}
	}
	return result
}

func nativeWSTestBinary(t *testing.T, engine core.Engine) string {
	t.Helper()
	variable := map[core.Engine]string{core.EngineSingBox: "QCH_SING_BOX_BINARY", core.EngineXray: "QCH_XRAY_BINARY", core.EngineMihomo: "QCH_MIHOMO_BINARY"}[engine]
	binary := os.Getenv(variable)
	if binary == "" {
		binary = filepath.Join(filepath.Dir(os.Getenv("QCH_SING_BOX_BINARY")), string(engine))
	}
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("%s is unavailable", variable)
	}
	return binary
}

func testNativeWebSocketTLSAddressOverride(t *testing.T, engine core.Engine, protocol Protocol) {
	binary := nativeWSTestBinary(t, core.EngineSingBox)
	input, err := nativeTLSFixturePlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	certificate, certPath, keyPath := singBoxTestCertificate(t)
	input.Listen, input.Port = "127.0.0.1", singBoxFreePort(t)
	input.CertificatePath, input.PrivateKeyPath = certPath, keyPath
	input.TLSServerName, input.TLSClientCertificate = "tls.example.test", certificate
	input.Transport, input.TransportPath = "websocket", "/custom-ws"
	server, err := Generate(engine, input)
	if err != nil {
		t.Fatal(err)
	}
	serverLog := nativeWSOriginRun(t, engine, nativeWSTestBinary(t, engine), server, input.Port, certPath)
	// Exercise the saved source and detached metadata, not just the form input.
	parsed, ok := Parse(engine, server)
	if !ok {
		t.Fatal("cannot read native TLS source")
	}
	metadata, err := MarshalClientMetadata(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyClientMetadata(&parsed, metadata); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(certificate))
	target, _ := url.Parse("https://127.0.0.1:" + strconv.Itoa(input.Port))
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: input.TLSServerName}}
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	type requestInfo struct {
		host, sni, path, alpn string
		major, rejection      int
	}
	seen := make(chan requestInfo, 32)
	edge := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := requestInfo{host: r.Host, sni: r.TLS.ServerName, path: r.URL.Path, alpn: r.TLS.NegotiatedProtocol, major: r.ProtoMajor}
		if r.Host != input.TLSServerName && r.Host != "custom.example.test" {
			http.Error(w, "unknown virtual host", http.StatusMisdirectedRequest)
			info.rejection = http.StatusMisdirectedRequest
			seen <- info
			return
		}
		seen <- info
		proxy.ServeHTTP(w, r)
	}))
	// Prefer 443 (also exercised in an isolated network namespace locally).
	// Unprivileged CI still exercises the full route on an allocated port.
	listener, err := net.Listen("tcp", "127.0.0.2:443")
	if err != nil {
		listener, err = net.Listen("tcp", "127.0.0.2:0")
		if err != nil {
			edge.Close()
			t.Fatal(err)
		}
	}
	edgePort := listener.Addr().(*net.TCPAddr).Port
	t.Logf("local TLS edge port: %d", edgePort)
	_ = edge.Listener.Close()
	edge.Listener = listener
	edge.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, NextProtos: []string{"http/1.1"}}
	edge.StartTLS()
	t.Cleanup(edge.Close)
	for _, tc := range []struct {
		name, host, address       string
		edge, removeHost, success bool
	}{
		{name: "direct-domain-control", address: "localhost", success: true},
		{name: "preferred-ip-empty-host", address: "127.0.0.2", edge: true, success: true},
		{name: "preferred-ip-explicit-host", address: "127.0.0.2", host: "custom.example.test", edge: true, success: true},
		{name: "reproduce-missing-host", address: "127.0.0.2", edge: true, removeHost: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := parsed
			plan.TransportHost = tc.host
			if tc.edge {
				plan.Port = edgePort
			}
			raw, err := BuildClientOutbound(core.EngineSingBox, plan, tc.address, "")
			if err != nil {
				t.Fatal(err)
			}
			var out map[string]any
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			profile, err := BuildClientProfile(plan, tc.address, "")
			if err != nil {
				t.Fatal(err)
			}
			var exported map[string]any
			if err := json.Unmarshal([]byte(profile.URI), &exported); err != nil {
				t.Fatal(err)
			}
			imported := firstMap(exported["outbounds"])
			if !reflect.DeepEqual(out, imported) {
				t.Fatal("JSON profile and outbound differ")
			}
			out = imported
			if tc.removeHost {
				delete(mapValue(out["transport"]), "headers")
			}
			echo := singBoxEcho(t, "tcp")
			port := singBoxFreePort(t)
			client, _ := json.Marshal(map[string]any{"inbounds": []any{map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": port, "network": "tcp", "override_address": "127.0.0.1", "override_port": echo}}, "outbounds": []any{out}})
			clientLog := singBoxRun(t, binary, "ws-edge-client", string(client))
			err = singBoxEchoRoundTrip("tcp", port)
			if tc.edge {
				var r requestInfo
				select {
				case r = <-seen:
				case <-time.After(3 * time.Second):
					t.Fatalf("edge received no request: %v\n%s", err, clientLog())
				}
				wantHost := tc.host
				if wantHost == "" {
					wantHost = input.TLSServerName
				}
				if tc.removeHost {
					wantHost = net.JoinHostPort(tc.address, strconv.Itoa(edgePort))
					// Observe the actual response at the edge; core log writes can
					// arrive after the failed socket read has already returned.
					if r.rejection != http.StatusMisdirectedRequest {
						t.Fatalf("missing Host failed outside HTTP hostname routing: %+v", r)
					}
				}
				if r.host != wantHost || r.sni != input.TLSServerName || r.path != input.TransportPath || r.major != 1 || r.alpn != "http/1.1" {
					t.Fatalf("unexpected edge request: %+v", r)
				}
				t.Logf("TLS verified; HTTP/%d ALPN=%s SNI=%s Host=%s path=%s; payload success=%v", r.major, r.alpn, r.sni, r.host, r.path, err == nil)
			}

			if tc.success && tc.edge {
				t.Run("xray-json", func(t *testing.T) {
					testWebSocketXrayImport(t, plan, tc.address, certPath)
					select {
					case r := <-seen:
						want := tc.host
						if want == "" {
							want = input.TLSServerName
						}
						if r.host != want || r.sni != input.TLSServerName || r.path != input.TransportPath {
							t.Fatalf("Xray changed routing: %+v", r)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("Xray did not reach edge")
					}
				})

				for _, uri := range []bool{false, true} {
					name := "mihomo-yaml"
					if uri {
						name = "share-uri-import"
					}
					t.Run(name, func(t *testing.T) {
						testWebSocketMihomoImport(t, plan, tc.address, certPath, uri)
						select {
						case r := <-seen:
							want := tc.host
							if want == "" {
								want = input.TLSServerName
							}
							if uri && nativeProtocol(plan.Protocol) == ProtocolTrojan && tc.host != "" {
								// Mihomo 1.19.32's Trojan URI parser ignores host. The
								// subscription path must use native YAML for this case.
								want = input.TLSServerName
								t.Log("Mihomo Trojan URI ignored custom Host; use native YAML")
							}
							if r.host != want || r.sni != input.TLSServerName || r.path != input.TransportPath {
								t.Fatalf("import changed edge routing: %+v", r)
							}
						case <-time.After(3 * time.Second):
							t.Fatal("import did not reach edge")
						}
					})
				}
			}

			if (err == nil) != tc.success {
				t.Fatalf("success=%v want=%v: %v\nserver: %s\nclient: %s", err == nil, tc.success, err, serverLog(), clientLog())
			}
		})
	}
}

func testPlainWebSocketClientRoutingLive(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineSingBox, core.EngineXray, core.EngineMihomo} {
		t.Run(string(engine), func(t *testing.T) {
			input := Input{Protocol: ProtocolVMess, Username: "default", Tag: "plain-ws", Listen: "127.0.0.1", Port: singBoxFreePort(t),
				Credential: "123e4567-e89b-42d3-a456-426614174000", Transport: "websocket", TransportPath: "/relay", TransportHost: "host.example.test"}
			source, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			originLogs := nativeWSOriginRun(t, engine, nativeWSTestBinary(t, engine), source, input.Port, "")
			metadata, _ := MarshalClientMetadata(input)
			parsed, ok := Parse(engine, source)
			if !ok {
				t.Fatal("parse failed")
			}
			if err := ApplyClientMetadata(&parsed, metadata); err != nil {
				t.Fatal(err)
			}
			target, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(input.Port))
			proxy := httputil.NewSingleHostReverseProxy(target)
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != input.TransportHost || r.URL.Path != input.TransportPath {
					http.Error(w, "wrong WS route", http.StatusMisdirectedRequest)
					return
				}
				proxy.ServeHTTP(w, r)
			}))
			t.Cleanup(edge.Close)
			_, port, _ := net.SplitHostPort(edge.Listener.Addr().String())
			parsed.Port, _ = strconv.Atoi(port)
			out, err := BuildClientOutbound(core.EngineSingBox, parsed, "127.0.0.1", "")
			if err != nil {
				t.Fatal(err)
			}
			var outbound map[string]any
			_ = json.Unmarshal(out, &outbound)
			echo, clientPort := singBoxEcho(t, "tcp"), singBoxFreePort(t)
			client, _ := json.Marshal(map[string]any{"inbounds": []any{map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": clientPort, "network": "tcp", "override_address": "127.0.0.1", "override_port": echo}}, "outbounds": []any{outbound}})
			logs := singBoxRun(t, nativeWSTestBinary(t, core.EngineSingBox), "plain-ws-client", string(client))
			if engine == core.EngineMihomo {
				// Mihomo binds its listener before tunnel.OnRunning. Warm up
				// only this controlled echo path; keep the strict payload and
				// exported-client assertions below after forwarding is ready.
				deadline := time.Now().Add(3 * time.Second)
				for {
					err := singBoxEchoRoundTrip("tcp", clientPort)
					if err == nil {
						break
					}
					if !errors.Is(err, io.EOF) || time.Now().After(deadline) {
						t.Fatalf("plaintext WS forwarding not ready: %v\n%s\n%s", err, originLogs(), logs())
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			if err := singBoxEchoRoundTrip("tcp", clientPort); err != nil {
				t.Fatalf("plaintext WS payload: %v\n%s", err, logs())
			}
			t.Run("xray", func(t *testing.T) { testWebSocketXrayImport(t, parsed, "127.0.0.1", "") })
			t.Run("mihomo", func(t *testing.T) { testWebSocketMihomoImport(t, parsed, "127.0.0.1", "", false) })
			t.Run("share-uri", func(t *testing.T) { testWebSocketMihomoImport(t, parsed, "127.0.0.1", "", true) })
		})
	}
}
