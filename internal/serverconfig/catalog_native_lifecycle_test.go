package serverconfig

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Optional native coverage complements the transactional API catalog matrix:
// every published protocol actually opens and releases its own sockets while
// a neighboring listener survives edits, disable/re-enable and deletion.
func TestCatalogNativeListenerLifecycle(t *testing.T) {
	directory := os.Getenv("QCH_TEST_MANAGED_CORE_DIR")
	if directory == "" {
		t.Skip("requires pinned native core fixture")
	}
	_, certificate, key := singBoxTestCertificate(t)
	port := func() int {
		t.Helper()
		for {
			socket, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			p := socket.Addr().(*net.TCPAddr).Port
			_ = socket.Close()
			udp, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
			if err == nil {
				_ = udp.Close()
				return p
			}
		}
	}
	for _, engine := range core.AllEngines() {
		for _, protocol := range Protocols(engine) {
			t.Run(string(engine)+"/"+protocol.Key, func(t *testing.T) {
				input, err := NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.Tag, input.Port = "selected", port()
				if protocol.PortForward {
					input.TargetAddress, input.TargetPort = "127.0.0.1", 80
				}
				input.CertificatePath, input.PrivateKeyPath = certificate, key
				input.TLSMode, input.TLSServerName, input.RealityServerName = "native", "tls.example.test", "reality.example.test"
				input.XHTTPTLSPort, input.XHTTPBackendPort = port(), port()
				original := input.Port
				source, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				siblingProtocol, _ := FindProtocol(engine, ProtocolSS2022)
				sibling, err := NewPlan(siblingProtocol)
				if err != nil {
					t.Fatal(err)
				}
				sibling.Tag, sibling.Port = "neighbor", port()
				neighbor, err := Generate(engine, sibling)
				if err != nil {
					t.Fatal(err)
				}
				source, err = MutateGenerated(engine, source, neighbor, "", "add")
				if err != nil {
					t.Fatal(err)
				}
				root := t.TempDir()
				binary := string(engine)
				if engine == core.EngineShadowsocksRust {
					binary = "ssserver"
				}
				occupied := func(p int, udp bool) bool {
					if udp {
						socket, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
						if err != nil {
							return true
						}
						_ = socket.Close()
						return false
					}
					conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)), 50*time.Millisecond)
					if err != nil {
						return false
					}
					_ = conn.Close()
					return true
				}
				var stop = func() {}
				t.Cleanup(func() { stop() })
				check := func(content string, state ManagedState, enabled bool) {
					t.Helper()
					stop()
					compiled, err := CompileManagedConfig(engine, content, state)
					if err != nil {
						t.Fatal(err)
					}
					plan, err := PrepareIndependentEgress(engine, compiled)
					if engine == core.EngineSingBox {
						plan, err = PrepareMarkedSingBoxAccounting(compiled)
					}
					if err != nil {
						t.Fatal(err)
					}
					file := filepath.Join(root, "config.json")
					if engine == core.EngineMihomo {
						file = filepath.Join(root, "config.yaml")
					}
					if err = os.WriteFile(file, []byte(plan.Content), 0600); err != nil {
						t.Fatal(err)
					}
					args := []string{"run", "-c", file}
					switch engine {
					case core.EngineXray:
						args = []string{"run", "-config", file}
					case core.EngineMihomo:
						args = []string{"-d", root, "-f", file}
					case core.EngineShadowsocksRust:
						args = []string{"-c", file}
					}
					log, err := os.Create(filepath.Join(root, "core.log"))
					if err != nil {
						t.Fatal(err)
					}
					command := exec.Command(filepath.Join(directory, binary), args...)
					command.Env = append(os.Environ(), "SAFE_PATHS="+filepath.Dir(certificate))
					command.Stdout, command.Stderr = log, log
					if err = command.Start(); err != nil {
						t.Fatal(err)
					}
					stopped := false
					stop = func() {
						if stopped {
							return
						}
						stopped = true
						_ = command.Process.Signal(syscall.SIGTERM)
						done := make(chan struct{})
						go func() { _ = command.Wait(); close(done) }()
						select {
						case <-done:
						case <-time.After(2 * time.Second):
							_ = command.Process.Kill()
							<-done
						}
						_ = log.Close()
					}
					// QUIC and WireGuard expose UDP; all other published presets expose TCP.
					udp := protocol.Key == "hysteria2" || protocol.Key == ProtocolTUIC || protocol.Key == ProtocolWireGuard || protocol.Key == "vmess-quic"
					deadline := time.Now().Add(5 * time.Second)
					for !occupied(sibling.Port, false) || enabled && !occupied(input.Port, udp) {
						if time.Now().After(deadline) {
							output, _ := os.ReadFile(log.Name())
							t.Fatalf("native sockets unavailable: %s", output)
						}
						time.Sleep(20 * time.Millisecond)
					}
					if !enabled && occupied(input.Port, udp) {
						t.Fatal("disabled listener still owns its socket")
					}
					if input.Port != original && occupied(original, udp) {
						t.Fatal("old listening port survived edit")
					}
				}
				check(source, ManagedState{}, true)
				section := "inbounds"
				if engine == core.EngineMihomo {
					section = "listeners"
				}
				if engine == core.EngineShadowsocksRust {
					section = "servers"
				}
				if engine == core.EngineSingBox && protocol.UsesEndpoint {
					section = "endpoints"
				}
				state := ManagedState{Disabled: []ManagedResource{{section, input.Tag}}}
				check(source, state, false)
				input.Port = port()
				generated, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				if engine == core.EngineMihomo {
					generated, err = RemoveMainlandAccessPolicy(engine, generated, input.Tag)
					if err != nil {
						t.Fatal(err)
					}
				}
				source, err = MutateGenerated(engine, source, generated, input.Tag, "modify")
				if err != nil {
					t.Fatal(err)
				}
				check(source, state, false)
				check(source, ManagedState{}, true)
				source, err = RemoveMainlandAccessPolicy(engine, source, input.Tag)
				if err != nil {
					t.Fatal(err)
				}
				source, err = DeletePresetInbound(engine, source, input.Tag)
				if err != nil {
					t.Fatal(err)
				}
				check(source, ManagedState{}, false)
				source, err = RemoveMainlandAccessPolicy(engine, source, sibling.Tag)
				if err != nil {
					t.Fatal(err)
				}
				source, err = DeletePresetInbound(engine, source, sibling.Tag)
				if err != nil {
					t.Fatal(err)
				}
				stop()
				plan, err := PrepareIndependentEgress(engine, source)
				if err != nil || plan.Source != "disabled" {
					t.Fatalf("last deletion is not inert: %v %s", err, plan.Source)
				}
				source, err = MutateGenerated(engine, source, generated, "", "add")
				if err != nil {
					t.Fatal(err)
				}
				source, err = ApplyMainlandAccessPolicyWithPrefixes(engine, source, MainlandAccessPolicy{Tag: input.Tag, Port: input.Port, Engine: engine, BlockMainlandDestination: input.BlockMainlandDestination, BlockMainlandSource: input.BlockMainlandSource}, nil)
				if err != nil {
					t.Fatal(err)
				}
				// A final add with its neighbor proves empty state is reusable.
				source, err = MutateGenerated(engine, source, neighbor, "", "add")
				if err != nil {
					t.Fatal(err)
				}
				check(source, ManagedState{}, true)
				t.Logf("native lifecycle %s/%s", engine, protocol.Key)
			})
		}
	}
}
