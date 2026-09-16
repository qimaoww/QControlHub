package serverconfig

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Optional, real-core regression. It creates only user-space WireGuard
// interfaces and local HTTP/SOCKS peers, never OS routes or firewall rules.
func TestWireGuardNativeCoreRoundTrip(t *testing.T) {
	clientBinary := os.Getenv("QCH_TEST_SINGBOX_BIN")
	if clientBinary == "" {
		t.Skip("requires QCH_TEST_SINGBOX_BIN")
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			serverBinary := clientBinary
			if engine == core.EngineXray {
				serverBinary = os.Getenv("QCH_TEST_XRAY_BIN")
				if serverBinary == "" {
					t.Skip("requires QCH_TEST_XRAY_BIN")
				}
			}
			protocol, _ := FindProtocol(engine, ProtocolWireGuard)
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			socket, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			input.Port = socket.LocalAddr().(*net.UDPAddr).Port
			_ = socket.Close()
			if engine == core.EngineXray {
				input.Listen = "127.0.0.1"
			}
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PrepareIndependentEgress(engine, content)
			if err != nil {
				t.Fatal(err)
			}
			startWireGuardTestCore(t, engine, serverBinary, plan.Content)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			socksPort := listener.Addr().(*net.TCPAddr).Port
			_ = listener.Close()
			client := map[string]any{
				"log":      map[string]any{"level": "error"},
				"inbounds": []any{map[string]any{"type": "socks", "tag": "test-client", "listen": "127.0.0.1", "listen_port": socksPort}},
				"endpoints": []any{map[string]any{
					"type": "wireguard", "tag": "wg-client", "system": false, "mtu": input.WireGuardMTU,
					"address": splitWireGuardList(input.WireGuardClientAddress), "private_key": input.WireGuardClientPrivateKey,
					"peers": []any{map[string]any{
						"address": "127.0.0.1", "port": input.Port, "public_key": input.WireGuardServerPublicKey,
						"pre_shared_key": input.WireGuardPresharedKey, "allowed_ips": []string{"0.0.0.0/0"},
					}},
				}},
				"route": map[string]any{"final": "wg-client"},
			}
			raw, _ := json.Marshal(client)
			startWireGuardTestCore(t, core.EngineSingBox, clientBinary, string(raw))
			socksAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort))
			deadline := time.Now().Add(5 * time.Second)
			for {
				conn, err := net.DialTimeout("tcp", socksAddress, 100*time.Millisecond)
				if err == nil {
					_ = conn.Close()
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("native SOCKS client did not start: %v", err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			payload := strings.Repeat("WireGuard native loopback regression\n", 2048)
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if string(body) != payload {
					http.Error(w, "request payload mismatch", http.StatusBadRequest)
					return
				}
				_, _ = io.WriteString(w, payload)
			}))
			// gVisor deliberately drops tunneled 127/8 destinations as martian
			// packets. Bind to an address owned by this host instead; no packet
			// leaves the host and no system interface/route is changed.
			_ = target.Listener.Close()
			target.Listener = wireGuardLocalHTTPListener(t)
			target.Start()
			defer target.Close()
			proxyURL, _ := url.Parse("socks5://" + socksAddress)
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			defer transport.CloseIdleConnections()
			httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second}
			response, err := httpClient.Post(target.URL, "text/plain", strings.NewReader(payload))
			if err != nil {
				t.Fatalf("traffic through the generated WireGuard server failed: %v", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != http.StatusOK || string(body) != payload {
				t.Fatalf("native WireGuard transfer corrupted bytes: status=%d error=%v", response.StatusCode, err)
			}
			if engine == core.EngineXray && plan.Source == "core-api" {
				assertWireGuardXrayCounters(t, serverBinary, plan, input.Tag, len(payload))
			}
		})
	}
}

func assertWireGuardXrayCounters(t *testing.T, binary string, plan AccountingPlan, inbound string, payloadBytes int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "api", "statsquery", "--server="+plan.API).CombinedOutput()
	if err != nil {
		t.Fatalf("read native WireGuard counters: %v: %s", err, output)
	}
	var response struct {
		Stats []struct {
			Name  string      `json:"name"`
			Value json.Number `json:"value"`
		} `json:"stat"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("decode native WireGuard counters: %v: %s", err, output)
	}
	counters := make(map[string]int64)
	for _, stat := range response.Stats {
		counters[stat.Name], _ = stat.Value.Int64()
	}
	for _, side := range []string{"inbound>>>" + inbound, "outbound>>>" + plan.Ports[0].Outbounds[0]} {
		for _, direction := range []string{"uplink", "downlink"} {
			name := side + ">>>traffic>>>" + direction
			if counters[name] < int64(payloadBytes) {
				t.Fatalf("native WireGuard counter %s = %d, below transferred payload %d", name, counters[name], payloadBytes)
			}
		}
	}
}

func wireGuardLocalHTTPListener(t *testing.T) net.Listener {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() != nil && ip.IsGlobalUnicast() {
			listener, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
			if err == nil {
				return listener
			}
		}
	}
	t.Skip("native WireGuard regression needs a host-owned non-loopback IPv4 address")
	return nil
}

func startWireGuardTestCore(t *testing.T, engine core.Engine, binary, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	checkArgs, runArgs := []string{"check", "-c", path}, []string{"run", "-c", path}
	if engine == core.EngineXray {
		checkArgs, runArgs = []string{"run", "-test", "-config", path}, []string{"run", "-config", path}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, binary, checkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("%s rejected native WireGuard config: %v: %s", engine, err, output)
	}
	log, err := os.CreateTemp(t.TempDir(), "core-log-")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, runArgs...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = log.Close()
		if t.Failed() {
			output, _ := os.ReadFile(log.Name())
			t.Logf("%s runtime output: %s", engine, output)
		}
	})
}
