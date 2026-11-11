package serverconfig

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

func TestMihomoUDP443RealityLive(t *testing.T) {
	binary := os.Getenv("QCH_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("QCH_MIHOMO_BINARY is not configured")
	}
	_, cert, key := singBoxTestCertificate(t)
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	cover := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("cover")) }))
	cover.EnableHTTP2 = true
	cover.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13, CurvePreferences: []tls.CurveID{tls.X25519}, NextProtos: []string{"h2"}}
	cover.StartTLS()
	defer cover.Close()
	for _, address := range []string{"127.0.0.2", "::1"} {
		t.Run(address, func(t *testing.T) {
			// Bind a real, controlled receiver before testing policy; an absent UDP
			// target must never be mistaken for a successful server rejection.
			udp, err := net.ListenPacket("udp", net.JoinHostPort(address, "443"))
			if err != nil {
				t.Fatal(err)
			}
			defer udp.Close()
			var received atomic.Int64
			go func() {
				buffer := make([]byte, 2048)
				for {
					n, peer, err := udp.ReadFrom(buffer)
					if err != nil {
						return
					}
					received.Add(1)
					_, _ = udp.WriteTo(buffer[:n], peer)
				}
			}()
			tcp, err := net.Listen("tcp", net.JoinHostPort(address, "443"))
			if err != nil {
				t.Fatal(err)
			}
			defer tcp.Close()
			go func() {
				for {
					c, err := tcp.Accept()
					if err != nil {
						return
					}
					go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
				}
			}()
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprint(enabled), func(t *testing.T) {
					input := udp443TestPlan(t)
					input.Tag = "blocked"
					input.Listen = "127.0.0.1"
					input.Port = singBoxFreePort(t)
					input.RealityTarget = ""
					input.BlockUDP443 = udp443Bool(enabled)
					source, err := Generate(core.EngineMihomo, input)
					if err != nil {
						t.Fatal(err)
					}
					sibling := input
					sibling.Tag = "unaffected"
					sibling.Port = singBoxFreePort(t)
					sibling.BlockUDP443 = udp443Bool(false)
					other, err := Generate(core.EngineMihomo, sibling)
					if err != nil {
						t.Fatal(err)
					}
					source, err = MutateGenerated(core.EngineMihomo, source, other, "", "add")
					if err != nil {
						t.Fatal(err)
					}
					var root map[string]any
					_ = yaml.Unmarshal([]byte(source), &root)
					for _, raw := range root["listeners"].([]any) {
						mapValue(mapValue(raw)["reality-config"])["dest"] = cover.Listener.Addr().String()
					}
					encodedServer, _ := yaml.Marshal(root)
					source = string(encodedServer)
					plan, err := PlanPresetAccounting(core.EngineMihomo, source)
					if err != nil {
						t.Fatal(err)
					}
					log := nativeWSOriginRun(t, core.EngineMihomo, binary, plan.Content, input.Port, cert)
					for _, test := range []struct {
						name, network string
						target        int
						sibling       bool
						blocked       bool
					}{
						{"udp443", "udp", 443, false, enabled}, {"tcp443", "tcp", 443, false, false}, {"udp-other", "udp", singBoxEcho(t, "udp"), false, false}, {"other-inbound", "udp", 443, true, false},
					} {
						t.Run(test.name, func(t *testing.T) {
							selected := input
							if test.sibling {
								selected = sibling
							}
							selected.ClientAddress = "127.0.0.1"
							selected.ClientPort = selected.Port
							outbound, err := BuildClientOutbound(core.EngineMihomo, selected, "127.0.0.1", "")
							if err != nil {
								t.Fatal(err)
							}
							var out map[string]any
							_ = json.Unmarshal(outbound, &out)
							port := singBoxFreePort(t)
							host := address
							if test.name == "udp-other" {
								host = "127.0.0.1"
							}
							config := map[string]any{"log-level": "debug", "listeners": []any{map[string]any{"type": "tunnel", "name": "payload", "listen": "127.0.0.1", "port": port, "network": []string{"tcp", "udp"}, "target": net.JoinHostPort(host, strconv.Itoa(test.target))}}, "proxies": []any{out}, "rules": []string{"MATCH,peer"}}
							encoded, _ := yaml.Marshal(config)
							clientLog := nativeWSOriginRun(t, core.EngineMihomo, binary, string(encoded), port, cert)
							count := received.Load()
							if test.blocked {
								if err := singBoxEchoRoundTripTimeout("udp", port, 600*time.Millisecond); err == nil {
									t.Fatal("blocked UDP reached receiver")
								}
								if received.Load() != count || !strings.Contains(log(), "REJECT") {
									t.Fatalf("rejection lacks server evidence: %s\nclient: %s", log(), clientLog())
								}
							} else if err := catalogEchoRoundTrip(test.network, port); err != nil {
								t.Fatalf("allowed traffic failed: %v\nserver: %s\nclient: %s", err, log(), clientLog())
							}
							t.Logf("enabled=%v inbound=%s network=%s target=%s:%d blocked=%v received_delta=%d", enabled, selected.Tag, test.network, host, test.target, test.blocked, received.Load()-count)
						})
					}
				})
			}
		})
	}
}
