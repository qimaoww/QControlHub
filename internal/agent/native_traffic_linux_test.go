package agent

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"golang.org/x/net/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Explicit opt-in, isolated loopback and local fixture binaries: never starts
// a proxy service on the developer/production host network.
func TestNativeTrafficCores(t *testing.T) {
	if os.Getenv("QCH_TEST_NATIVE_TRAFFIC") != "1" {
		t.Skip("QCH_TEST_NATIVE_TRAFFIC is not enabled")
	}
	if os.Getenv("QCH_NATIVE_TEST_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "unshare", "--net", os.Args[0], "-test.run="+flag.Lookup("test.run").Value.String(), "-test.v")
		command.Env = append(os.Environ(), "QCH_NATIVE_TEST_CHILD=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated core accounting: %v\n%s", err, output)
		}
		t.Logf("isolated core measurements:\n%s", output)
		return
	}
	if output, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Fatalf("loopback: %v %s", err, output)
	}
	if output, err := exec.Command("ip", "addr", "add", "192.0.2.1/32", "dev", "lo").CombinedOutput(); err != nil {
		t.Fatalf("isolated target address: %v %s", err, output)
	}
	engines := []core.Engine{core.EngineXray, core.EngineSingBox}
	if os.Getenv("QCH_TEST_MIHOMO_BIN") != "" {
		engines = append(engines, core.EngineMihomo)
	}
	if os.Getenv("QCH_TEST_SSRUST_DIR") != "" {
		engines = append(engines, core.EngineShadowsocksRust)
	}
	for _, engine := range engines {
		t.Run(string(engine), func(t *testing.T) {
			binary := os.Getenv("QCH_TEST_XRAY_BIN")
			if engine == core.EngineSingBox {
				binary = os.Getenv("QCH_TEST_SINGBOX_BIN")
			}
			if engine == core.EngineMihomo {
				binary = os.Getenv("QCH_TEST_MIHOMO_BIN")
			}
			if engine == core.EngineShadowsocksRust {
				binary = filepath.Join(os.Getenv("QCH_TEST_SSRUST_DIR"), "ssserver")
			}
			if !filepath.IsAbs(binary) {
				t.Fatal("absolute fixture binary path is required")
			}
			content := `{"inbounds":[{"tag":"a","port":1080,"listen":"127.0.0.1","protocol":"socks","settings":{"auth":"noauth","udp":true}},{"tag":"b","port":1081,"listen":"127.0.0.1","protocol":"socks","settings":{"auth":"noauth","udp":true}}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`
			if engine == core.EngineSingBox {
				content = `{"inbounds":[{"tag":"a","listen_port":1080,"listen":"127.0.0.1","type":"socks"},{"tag":"b","listen_port":1081,"listen":"127.0.0.1","type":"socks"}],"outbounds":[{"tag":"direct","type":"direct"}]}`
			}
			plan, err := serverconfig.PlanPresetAccounting(engine, content)
			if engine == core.EngineSingBox && os.Getenv("QCH_TEST_SINGBOX_MARKS") == "1" {
				if err != nil {
					t.Fatal(err)
				}
				plan, err = serverconfig.PrepareMarkedSingBoxAccounting(plan.Content)
			}
			if engine == core.EngineMihomo {
				plan, err = serverconfig.PrepareAccounting(engine, "listeners: [{name: a, type: socks, listen: 127.0.0.1, port: 1080, udp: true}, {name: b, type: socks, listen: 127.0.0.1, port: 1081, udp: true}]\nrules: ['MATCH,DIRECT']\n")
			}
			if engine == core.EngineShadowsocksRust {
				plan, err = serverconfig.PrepareAccounting(engine, `{"mode":"tcp_and_udp","servers":[{"server":"127.0.0.1","server_port":1080,"method":"aes-256-gcm","password":"qch-test-password"},{"server":"127.0.0.1","server_port":1081,"method":"aes-256-gcm","password":"qch-test-password"}]}`)
			}
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(plan.Content), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "run", "-c", path)
			if engine == core.EngineMihomo {
				command = exec.CommandContext(ctx, binary, "-d", t.TempDir(), "-f", path)
			}
			if engine == core.EngineShadowsocksRust {
				command = exec.CommandContext(ctx, binary, "-c", path)
			}
			log, err := os.CreateTemp(t.TempDir(), "core-log-")
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			command.Stdout, command.Stderr = log, log
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				cancel()
				_ = command.Wait()
				if t.Failed() {
					value, _ := os.ReadFile(log.Name())
					t.Log(string(value))
				}
			}()
			var counters map[string]uint64
			var manager *TrafficManager
			if plan.Source == "nft-dual" {
				var policy core.PortTrafficPolicy
				manager, _, _, policy = newAccuracyTrafficManager(t)
				manager.backend = &nftBackend{nftPath: "/usr/sbin/nft", direct: true}
				manager.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
					return nativeAccountingSnapshot{Plan: plan}, nil
				}
				policy.Engine, policy.Port = engine, 1080
				second := policy
				second.ID = "trf_fedcba9876543210"
				second.Port = 1081
				if err := manager.SetPolicies(ctx, []core.PortTrafficPolicy{policy, second}, policy.AgentID); err != nil {
					t.Fatal(err)
				}
				defer manager.SetPolicies(context.Background(), nil, policy.AgentID)
			}
			for attempt := 0; attempt < 40; attempt++ {
				if manager == nil {
					counters, err = queryNativeTraffic(ctx, engine)
				} else {
					var connection net.Conn
					connection, err = net.DialTimeout("tcp", "127.0.0.1:1080", time.Second)
					if err == nil {
						_ = connection.Close()
					}
				}
				if err == nil {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if err != nil {
				t.Fatal(err)
			}
			if engine == core.EngineShadowsocksRust {
				for i := 0; i < 2; i++ {
					local := exec.CommandContext(ctx, filepath.Join(os.Getenv("QCH_TEST_SSRUST_DIR"), "sslocal"), "-U", "-s", fmt.Sprintf("127.0.0.1:%d", 1080+i), "-b", fmt.Sprintf("127.0.0.1:%d", 2080+i), "-m", "aes-256-gcm", "-k", "qch-test-password")
					local.Stdout, local.Stderr = log, log
					if err := local.Start(); err != nil {
						t.Fatal(err)
					}
					defer func() { _ = local.Process.Kill(); _ = local.Wait() }()
					for attempt := 0; attempt < 40; attempt++ {
						probe, probeErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", 2080+i), time.Second)
						if probeErr == nil {
							_ = probe.Close()
							break
						}
						time.Sleep(25 * time.Millisecond)
					}
				}
			}
			// Mihomo binds listeners before publishing its initial routing
			// configuration. A TCP accept alone is not proxy readiness.
			if engine == core.EngineMihomo {
				time.Sleep(150 * time.Millisecond)
			}
			listener, err := net.Listen("tcp", "192.0.2.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serveErrors := make(chan error, 2)
			go func() {
				for i := 0; i < 2; i++ {
					connection, err := listener.Accept()
					if err != nil {
						serveErrors <- err
						return
					}
					_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
					_, err = io.ReadFull(connection, make([]byte, 16))
					if err == nil {
						_, err = io.WriteString(connection, strings.Repeat("d", 4096*(i+1)))
					}
					_ = connection.Close()
					serveErrors <- err
				}
			}()
			for i := 0; i < 2; i++ {
				port := 1080 + i
				if engine == core.EngineShadowsocksRust {
					port += 1000
				}
				dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", port), nil, &net.Dialer{Timeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				connection, err := dialer.Dial("tcp", listener.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
				if _, err := io.WriteString(connection, strings.Repeat("u", 16)); err != nil {
					t.Fatal(err)
				}
				if _, err := io.ReadFull(connection, make([]byte, 4096*(i+1))); err != nil {
					t.Fatal(err)
				}
				_ = connection.Close()
				if err := <-serveErrors; err != nil {
					t.Fatal(err)
				}
			}
			if manager != nil {
				manager.collect(ctx, false)
				for i, usage := range manager.Snapshot() {
					want := uint64(4096 * (i + 1))
					a := usage.Accounting
					t.Logf("TCP engine=%s port=%d payload_up=16 payload_down=%d accounting=%+v", engine, 1080+i, want, a)
					if !usage.EnforcementAvailable || a == nil || a.TargetReceived < want || a.TargetReceived > want+2000 || a.TargetSent < 16 || a.ClientSent < want {
						t.Fatalf("marked core %s port %d: %+v / %+v", engine, 1080+i, usage, a)
					}
				}
				testCoreUDPAccounting(t, ctx, engine, plan, manager)
				return
			}
			for attempt := 0; attempt < 40; attempt++ {
				counters, err = queryNativeTraffic(ctx, engine)
				if err != nil {
					t.Fatal(err)
				}
				if nativeTrafficLegs(counters, plan.Ports[1]).ClientSent >= 8192 {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			for i, port := range plan.Ports {
				got := nativeTrafficLegs(counters, port)
				t.Logf("TCP engine=%s port=%d payload_up=16 payload_down=%d legs=%+v", engine, port.Port, 4096*(i+1), got)
				want := trafficLegs{ClientReceived: 16, TargetSent: 16, ClientSent: uint64(4096 * (i + 1)), TargetReceived: uint64(4096 * (i + 1))}
				// Xray wraps inbound transport before the SOCKS handshake;
				// sing-box counts the decoded stream. Do not erase this overhead.
				if engine == core.EngineXray {
					want.ClientReceived += 13
					want.ClientSent += 12
				}
				if got != want {
					t.Fatalf("port %d: %+v, want %+v", port.Port, got, want)
				}
			}
			repeated, err := queryNativeTraffic(ctx, engine)
			if err != nil {
				t.Fatal(err)
			}
			for _, port := range plan.Ports {
				if nativeTrafficLegs(counters, port) != nativeTrafficLegs(repeated, port) {
					t.Fatal("query reset counters")
				}
			}
			testCoreUDPAccounting(t, ctx, engine, plan, nil)
			testStatsAPIProxyIsolation(t, ctx, engine, plan)
		})
	}
}

type resetStatsTestCodec struct{ nativeStatsCodec }

func (resetStatsTestCodec) Marshal(any) ([]byte, error) { return []byte{0x10, 1}, nil }

func testStatsAPIProxyIsolation(t *testing.T, ctx context.Context, engine core.Engine, plan serverconfig.AccountingPlan) {
	t.Helper()
	before, err := queryNativeTraffic(ctx, engine)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(plan.API)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1", "localhost", "::ffff:127.0.0.1"} {
		dialer, err := proxy.SOCKS5("tcp", "127.0.0.1:1080", nil, proxy.Direct)
		if err != nil {
			t.Fatal(err)
		}
		connection, err := grpc.NewClient(plan.API, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return dialer.(proxy.ContextDialer).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
		}))
		if err != nil {
			t.Fatal(err)
		}
		probe, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
		service := "xray.app.stats.command.StatsService"
		if engine == core.EngineSingBox {
			service = "v2ray.core.app.stats.command.StatsService"
		}
		var response nativeStatsResponse
		err = connection.Invoke(probe, "/"+service+"/QueryStats", &nativeStatsRequest{}, &response, grpc.ForceCodec(resetStatsTestCodec{}))
		cancel()
		_ = connection.Close()
		if err == nil {
			t.Fatalf("proxy accessed reset-capable API through %s", host)
		}
	}
	after, err := queryNativeTraffic(ctx, engine)
	if err != nil {
		t.Fatalf("Agent lost local API access: %v", err)
	}
	for key, value := range before {
		if after[key] < value {
			t.Fatalf("API attack reset %s: %d -> %d", key, value, after[key])
		}
	}
}

func testCoreUDPAccounting(t *testing.T, ctx context.Context, engine core.Engine, plan serverconfig.AccountingPlan, manager *TrafficManager) {
	t.Helper()
	read := func() []trafficLegs {
		t.Helper()
		var result []trafficLegs
		if manager != nil {
			manager.collect(ctx, false)
			for _, usage := range manager.Snapshot() {
				a := usage.Accounting
				result = append(result, trafficLegs{ClientReceived: a.ClientReceived, ClientSent: a.ClientSent, TargetReceived: a.TargetReceived, TargetSent: a.TargetSent})
			}
			return result
		}
		counters, err := queryNativeTraffic(ctx, engine)
		if err != nil {
			t.Fatal(err)
		}
		for _, port := range plan.Ports {
			result = append(result, nativeTrafficLegs(counters, port))
		}
		return result
	}
	before := read()
	port := 1080
	if engine == core.EngineShadowsocksRust {
		port = 2080
	}
	control, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := control.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	hello := make([]byte, 2)
	if _, err := io.ReadFull(control, hello); err != nil || hello[1] != 0 {
		t.Fatalf("SOCKS greeting: %v %v", hello, err)
	}
	if _, err := control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(control, header); err != nil || header[1] != 0 {
		t.Fatalf("UDP associate: %v %v", header, err)
	}
	length := 4
	if header[3] == 4 {
		length = 16
	} else if header[3] != 1 {
		t.Fatal("unexpected relay address type")
	}
	address := make([]byte, length+2)
	if _, err := io.ReadFull(control, address); err != nil {
		t.Fatal(err)
	}
	ip := net.IP(address[:length])
	if ip.IsUnspecified() {
		ip = net.ParseIP("127.0.0.1")
	}
	relay, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: ip, Port: int(binary.BigEndian.Uint16(address[length:]))})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	_ = relay.SetDeadline(time.Now().Add(3 * time.Second))
	target, err := net.ListenPacket("udp4", "192.0.2.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	_ = target.SetDeadline(time.Now().Add(3 * time.Second))
	targetPort := target.LocalAddr().(*net.UDPAddr).Port
	packet := append([]byte{0, 0, 0, 1, 192, 0, 2, 1, byte(targetPort >> 8), byte(targetPort)}, []byte("udp-request")...)
	if _, err := relay.Write(packet); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1024)
	n, peer, err := target.ReadFrom(buffer)
	if err != nil || string(buffer[:n]) != "udp-request" {
		t.Fatalf("UDP target: %d %v", n, err)
	}
	if _, err := target.WriteTo([]byte("udp-response-data"), peer); err != nil {
		t.Fatal(err)
	}
	if n, err := relay.Read(buffer); err != nil || n < 17 || string(buffer[n-17:n]) != "udp-response-data" {
		t.Fatalf("UDP relay: %d %v", n, err)
	}
	var after []trafficLegs
	for attempt := 0; attempt < 40; attempt++ {
		after = read()
		if after[0].TargetReceived >= before[0].TargetReceived+17 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if after[0].TargetReceived < before[0].TargetReceived+17 || after[0].TargetSent < before[0].TargetSent+11 || after[0].ClientReceived <= before[0].ClientReceived || after[0].ClientSent <= before[0].ClientSent {
		t.Fatalf("UDP four-leg accounting: before=%+v after=%+v", before, after)
	}
	t.Logf("UDP engine=%s payload_up=11 payload_down=17 before=%+v after=%+v", engine, before, after)
	if after[1].TargetReceived != before[1].TargetReceived || after[1].TargetSent != before[1].TargetSent {
		t.Fatalf("UDP attributed to another port: before=%+v after=%+v", before, after)
	}
}
