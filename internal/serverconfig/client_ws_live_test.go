package serverconfig

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// A local hostname-routing TLS edge, not a Cloudflare service. Keep this in the
// existing live matrix so CI exercises actual VLESS payloads through the edge.
func testSingBoxWebSocketTLSAddressOverride(t *testing.T) {
	binary := os.Getenv("QCH_SING_BOX_BINARY")
	protocol, _ := FindProtocol(core.EngineSingBox, "vless-ws-tls")
	input, err := nativeTLSFixturePlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	certificate, certPath, keyPath := singBoxTestCertificate(t)
	input.Listen, input.Port = "127.0.0.1", singBoxFreePort(t)
	input.CertificatePath, input.PrivateKeyPath = certPath, keyPath
	input.TLSServerName, input.TLSClientCertificate = "tls.example.test", certificate
	input.TransportPath = "/custom-ws"
	server, err := Generate(core.EngineSingBox, input)
	if err != nil {
		t.Fatal(err)
	}
	serverLog := singBoxRun(t, binary, "native-ws-origin", server)
	// Exercise the saved source and detached metadata, not just the form input.
	parsed, ok := Parse(core.EngineSingBox, server)
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
		major                 int
	}
	seen := make(chan requestInfo, 32)
	edge := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- requestInfo{r.Host, r.TLS.ServerName, r.URL.Path, r.TLS.NegotiatedProtocol, r.ProtoMajor}
		if r.Host != input.TLSServerName && r.Host != "custom.example.test" {
			http.Error(w, "unknown virtual host", http.StatusMisdirectedRequest)
			return
		}
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
			port, echo := singBoxFreePort(t), singBoxEcho(t, "tcp")
			client, _ := json.Marshal(map[string]any{"inbounds": []any{map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": port, "network": "tcp", "override_address": "127.0.0.1", "override_port": echo}}, "outbounds": []any{out}})
			clientLog := singBoxRun(t, binary, "ws-edge-client", string(client))
			err = singBoxEchoRoundTrip("tcp", port)
			if tc.removeHost && !strings.Contains(clientLog(), "unexpected HTTP response status: 421") {
				t.Fatalf("missing Host failed outside HTTP hostname routing: %v\n%s", err, clientLog())
			}
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
				}
				if r.host != wantHost || r.sni != input.TLSServerName || r.path != input.TransportPath || r.major != 1 || r.alpn != "http/1.1" {
					t.Fatalf("unexpected edge request: %+v", r)
				}
				t.Logf("TLS verified; HTTP/%d ALPN=%s SNI=%s Host=%s path=%s; payload success=%v", r.major, r.alpn, r.sni, r.host, r.path, err == nil)
			}

			if tc.success && tc.edge {
				for _, uri := range []bool{false, true} {
					name := "mihomo-yaml"
					if uri {
						name = "vless-uri-import"
					}
					t.Run(name, func(t *testing.T) {
						testWebSocketMihomoImport(t, plan, tc.address, certPath, uri)
						select {
						case r := <-seen:
							want := tc.host
							if want == "" {
								want = input.TLSServerName
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
