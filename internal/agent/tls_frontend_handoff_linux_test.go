//go:build linux

package agent

import (
	"context"
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
)

// Uses the existing disposable managed-TLS node and official sing-box binary.
// It deliberately refuses to run on an operator's host.
func TestNativeTLSFrontendHandoffLifecycle(t *testing.T) {
	if os.Getenv("QCH_TEST_NATIVE_TLS_HANDOFF") != "1" {
		t.Skip("requires disposable managed-TLS node")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires disposable container")
	}
	manager, err := NewServiceManager(os.Getenv("QCH_TEST_TLS_MANAGER"))
	if err != nil {
		t.Fatal(err)
	}
	e := &Executor{Specs: DefaultSpecsForServiceManager(manager.Kind()), Services: manager, tlsFrontendHelperPath: "/usr/local/lib/qagent/qagent"}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	cert, key := xhttpTestCertificate(t, t.TempDir())
	// The core service identity needs certificate access; keep the fixture
	// beneath its existing managed certificate root.
	for _, pair := range [][2]string{{cert, "/etc/qcontrolhub/tls/handoff.crt"}, {key, "/etc/qcontrolhub/tls/handoff.key"}} {
		data, err := os.ReadFile(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(pair[1]), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pair[1], data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Caddy reuses a valid cached certificate signed by this container's test
	// CA. Public issuance is deliberately left to the existing ACME sandbox.
	ca, err := os.ReadFile(cert)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/usr/local/share/ca-certificates/qch-handoff.crt", ca, 0644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(ctx, "/usr/sbin/update-ca-certificates"); err != nil {
		t.Fatalf("test CA: %v %s", err, output)
	}
	hosts, err := os.OpenFile("/etc/hosts", os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hosts.WriteString("\n93.184.215.14 tls.example.test\n")
	hosts.Close()
	if err != nil {
		t.Fatal(err)
	}
	m := tlsFrontendManager{tlsFrontendRoot, tlsFrontendState, manager, NewCoreUpdater()}
	if err := m.prepareDirectories(); err != nil {
		t.Fatal(err)
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	storage := filepath.Join(tlsFrontendState, "caddy", "certificates", "acme-v02.api.letsencrypt.org-directory", "tls.example.test")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(storage, int(identity.uid), int(identity.gid)); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ name, source string }{{"tls.example.test.crt", cert}, {"tls.example.test.key", key}} {
		data, err := os.ReadFile(entry.source)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(storage, entry.name)
		if err := os.WriteFile(path, data, 0640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, int(identity.uid), int(identity.gid)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(storage, "tls.example.test.json"), []byte(`{"sans":["tls.example.test"],"issuer_data":{"ca":"https://acme-v02.api.letsencrypt.org/directory"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"nginx", "caddy"} {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%t", mode, shared), func(t *testing.T) {
				base := e.Specs[core.EngineSingBox]
				spec := base
				task := core.Task{Engine: core.EngineSingBox, Action: core.ActionDeploy, SharedInstance: shared}
				if shared {
					task.SharedTrafficID = "shr_a123456789abcdef"
					spec, err = sharedInstanceSpec(task.Engine, base, task.SharedTrafficID, manager.Kind())
					if err != nil {
						t.Fatal(err)
					}
				}
				t.Cleanup(func() {
					cleanup, done := context.WithTimeout(context.Background(), 20*time.Second)
					defer done()
					_, _ = manager.command(cleanup, spec.Service, core.ActionStop)
					if err := e.executeTLSFrontend(cleanup, spec, core.Task{Engine: task.Engine, Action: core.ActionStop}, false); err != nil {
						t.Errorf("stop TLS fixture: %v", err)
					}
				})
				protocol, _ := serverconfig.FindProtocol(task.Engine, "vmess-ws-tls")
				input, err := serverconfig.NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.Tag, input.Listen, input.Port = "native", "0.0.0.0", 443
				if shared {
					input.Listen = "::"
				}
				input.TLSMode, input.TLSServerName = "native", "tls.example.test"
				input.CertificatePath, input.PrivateKeyPath = "/etc/qcontrolhub/tls/handoff.crt", "/etc/qcontrolhub/tls/handoff.key"
				input.BlockMainlandDestination, input.BlockMainlandSource = false, false
				generate := func() string {
					t.Helper()
					content, err := serverconfig.Generate(task.Engine, input)
					if err != nil {
						t.Fatal(err)
					}
					return content
				}
				task.ConfigContent = generate()
				if output, err := e.Execute(ctx, task); err != nil {
					t.Fatalf("native deploy: %v\n%s", err, output)
				}
				pid, err := managedServiceProcessPID(ctx, manager, spec.Service)
				if err != nil {
					t.Fatal(err)
				}
				input.Tag, input.Listen, input.Port, input.TLSMode, input.TLSPublicPort = "renamed", "127.0.0.1", 21001, mode, 443
				generated := generate()
				content, err := serverconfig.MutateGenerated(task.Engine, task.ConfigContent, generated, "native", "modify")
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := serverconfig.MarshalClientMetadataWithPrevious(input, "")
				if err != nil {
					t.Fatal(err)
				}
				task.ConfigContent = content
				task.TLSFrontends, err = serverconfig.ManagedTLSFrontends(task.Engine, content, map[string]string{input.Tag: metadata})
				if err != nil {
					t.Fatal(err)
				}
				m := tlsFrontendManager{tlsFrontendRoot, tlsFrontendState, manager, NewCoreUpdater()}
				if err := m.checkPorts(nil, task.TLSFrontends); err == nil {
					t.Fatal("baseline did not reproduce native listener conflict")
				} else {
					t.Logf("reproduced before core restart: %v", err)
				}
				// Both providers share this same port check; Caddy's ACME issuance
				// remains covered by the separate certificate sandbox.
				for _, mode := range []string{"caddy", "nginx"} {
					frontends := append([]core.TLSFrontend(nil), task.TLSFrontends...)
					frontends[0].Mode = mode
					if err := m.checkPorts(nil, frontends, func(port int) error { return m.checkNativeTLSHandoff(ctx, spec.Service, content, frontends, port) }); err != nil {
						t.Fatalf("%s handoff: %v", mode, err)
					}
				}
				retained := strings.ReplaceAll(content, `"listen_port": 21001`, `"listen_port": 443`)
				if err := m.checkNativeTLSHandoff(ctx, spec.Service, retained, task.TLSFrontends, 443); err == nil {
					t.Fatal("candidate retaining the public port was accepted")
				}
				validation := task
				validation.Action = core.ActionValidate
				if output, err := e.Execute(ctx, validation); err != nil {
					t.Fatalf("validate: %v\n%s", err, output)
				}
				after, err := managedServiceProcessPID(ctx, manager, spec.Service)
				if err != nil || after != pid {
					t.Fatal("validation changed running core")
				}
				// A stale file is not evidence that an external listener belongs to
				// this service. The normal validate request must still reject it.
				if _, err := manager.command(ctx, spec.Service, core.ActionStop); err != nil {
					t.Fatal(err)
				}
				external, err := net.Listen("tcp", ":443")
				if err != nil {
					t.Fatal(err)
				}
				_, validationErr := e.Execute(ctx, validation)
				external.Close()
				if validationErr == nil || !strings.Contains(validationErr.Error(), "不可用") {
					t.Fatalf("stale config authorized external listener: %v", validationErr)
				}
				if _, err := serviceCommandAndVerifyWithManager(ctx, manager, spec.Service, core.ActionStart); err != nil {
					t.Fatal(err)
				}
				// Inject a real TLS hostname failure after the core has switched to
				// its backend. Both the proxy and original native core must recover.
				original, err := os.ReadFile(spec.ConfigPath)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "nginx" {
					bad := task
					bad.TLSFrontends = append([]core.TLSFrontend(nil), task.TLSFrontends...)
					bad.TLSFrontends[0].Domain = "wrong-certificate.example.test"
					failure, stop := context.WithTimeout(ctx, 8*time.Second)
					_, deployErr := e.Execute(failure, bad)
					stop()
					if deployErr == nil || !strings.Contains(deployErr.Error(), "previous configuration was restored") {
						t.Fatalf("handoff failure did not roll back: %v", deployErr)
					}
					restored, err := os.ReadFile(spec.ConfigPath)
					if err != nil || string(restored) != string(original) {
						t.Fatalf("native config was lost: %v", err)
					}
					if _, err := managedServiceProcessPID(ctx, manager, spec.Service); err != nil {
						t.Fatal(err)
					}
					t.Log("external process rejected despite retained file; TLS failure restored native configuration and running service")
				}
				if output, err := e.Execute(ctx, task); err != nil {
					t.Fatalf("handoff deploy: %v\n%s", err, output)
				} else {
					t.Log(output)
				}
				if err := probeFrontendTLS(ctx, task.TLSFrontends[0], true); err != nil {
					t.Fatal(err)
				}
				applied, err := os.ReadFile(spec.ConfigPath)
				if err != nil || !strings.Contains(string(applied), "renamed") || strings.Contains(string(applied), `"tag": "native"`) {
					t.Fatalf("replacement not applied: %v", err)
				}
				// The old public port is now owned by Nginx; the core must own only
				// the new backend. A bind confirms it was not silently changed.
				if socket, err := net.Listen("tcp", ":443"); err == nil {
					socket.Close()
					t.Fatal("public listener absent")
				}
				t.Logf("native :443 -> %s :443 completed, renamed backend applied and TLS/WebSocket handshake passed", mode)
			})
		}
	}
}

func TestRetainedListenerWithLiveCores(t *testing.T) {
	if os.Getenv("QCH_TEST_NATIVE_TLS_HANDOFF") != "1" {
		t.Skip("requires disposable node with official cores")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires disposable container")
	}
	manager, err := NewServiceManager(os.Getenv("QCH_TEST_TLS_MANAGER"))
	if err != nil {
		t.Fatal(err)
	}
	e := &Executor{Specs: DefaultSpecsForServiceManager(manager.Kind()), Services: manager, tlsFrontendHelperPath: "/usr/local/lib/qagent/qagent"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for _, engine := range []core.Engine{core.EngineSingBox, core.EngineXray, core.EngineMihomo, core.EngineShadowsocksRust} {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%t", engine, shared), func(t *testing.T) {
				base := e.Specs[engine]
				if _, err := os.Stat(base.Binary); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(base.ConfigPath), 0750); err != nil {
					t.Fatal(err)
				}
				if err := e.prepareManagedCoreService(ctx, engine, base, false); err != nil {
					t.Fatal(err)
				}
				spec := base
				task := core.Task{ID: "tsk_initial", ConfigID: "cfg_live_identity", ConfigVersion: 1, Engine: engine, Action: core.ActionDeploy, SharedInstance: shared}
				if shared {
					task.SharedTrafficID = "shr_b123456789abcdef"
					spec, err = sharedInstanceSpec(engine, base, task.SharedTrafficID, manager.Kind())
					if err != nil {
						t.Fatal(err)
					}
				}
				t.Cleanup(func() {
					cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
					defer done()
					if _, err := manager.command(cleanup, spec.Service, core.ActionStop); err != nil {
						t.Error(err)
					}
				})
				protocol, ok := serverconfig.FindProtocol(engine, serverconfig.ProtocolSS2022)
				if !ok {
					t.Fatal("missing SS2022 preset")
				}
				input, err := serverconfig.NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.Tag, input.Listen, input.Port = "original", "127.0.0.1", 24443
				input.BlockMainlandDestination, input.BlockMainlandSource = false, false
				initial, err := serverconfig.Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				other := input
				other.Tag, other.Port = "unrelated", 24445
				additional, err := serverconfig.Generate(engine, other)
				if err != nil {
					t.Fatal(err)
				}
				initial, err = serverconfig.MutateGenerated(engine, initial, additional, "", "add")
				if err != nil {
					t.Fatal(err)
				}
				task.ConfigContent = initial
				if output, err := e.Execute(ctx, task); err != nil {
					t.Fatalf("initial: %v\n%s", err, output)
				}
				// Existing deployments predate the main-instance sidecar. A
				// refresh must reconstruct the retained identity without restart.
				if err := os.Remove(sharedStatePath(spec)); err != nil {
					t.Fatal(err)
				}
				beforeStatus, err := managedServiceProcessPID(ctx, manager, spec.Service)
				if err != nil {
					t.Fatal(err)
				}
				refresh := task
				refresh.ID = "tsk_status"
				refresh.Action = core.ActionStatus
				if output, err := e.Execute(ctx, refresh); err != nil {
					t.Fatalf("legacy status: %v %s", err, output)
				}
				afterStatus, err := managedServiceProcessPID(ctx, manager, spec.Service)
				if err != nil || afterStatus != beforeStatus {
					t.Fatal("status restarted the service")
				}
				identity := e.deploymentStatus(ctx, engine, spec, task.SharedTrafficID)
				if identity.ConfigID != task.ConfigID || identity.ConfigVersion != 1 {
					t.Fatalf("legacy identity was not restored: %+v", identity)
				}
				failed := task
				failed.ID = "tsk_failed"
				failed.ConfigVersion = 2
				failed.ConfigContent = "invalid configuration"
				if _, err := e.Execute(ctx, failed); err == nil {
					t.Fatal("invalid candidate unexpectedly deployed")
				}
				identity = e.deploymentStatus(ctx, engine, spec, task.SharedTrafficID)
				if identity.ConfigID != task.ConfigID || identity.ConfigVersion != 1 {
					t.Fatal("failed candidate replaced retained identity")
				}
				fresh, err := serverconfig.NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.Credential = fresh.Credential
				for _, tag := range []string{"original", "renamed"} {
					input.Tag = tag
					generated, err := serverconfig.Generate(engine, input)
					if err != nil {
						t.Fatal(err)
					}
					original := "original"
					content, err := serverconfig.MutateGenerated(engine, task.ConfigContent, generated, original, "modify")
					if err != nil {
						t.Fatal(err)
					}
					task.ConfigContent = content
					task.ConfigVersion++
					validation := task
					validation.Action = core.ActionValidate
					pid, err := managedServiceProcessPID(ctx, manager, spec.Service)
					if err != nil {
						t.Fatal(err)
					}
					if output, err := e.Execute(ctx, validation); err != nil {
						t.Fatalf("validate: %v\n%s", err, output)
					}
					after, err := managedServiceProcessPID(ctx, manager, spec.Service)
					if err != nil || pid != after {
						t.Fatal("validation changed process")
					}
					if output, err := e.Execute(ctx, task); err != nil {
						t.Fatalf("update: %v\n%s", err, output)
					}
					assertRetainedSS2022RoundTrip(t, ctx, input, 24443)
					pid, err = managedServiceProcessPID(ctx, manager, spec.Service)
					if err != nil {
						t.Fatal(err)
					}
					if err := processOwnsTCPPort(pid, 24443); err != nil {
						t.Fatal(err)
					}
					if err := processOwnsTCPPort(pid, 24445); err != nil {
						t.Fatalf("unrelated listener lost: %v", err)
					}
					applied, err := os.ReadFile(spec.ConfigPath)
					if err != nil || !strings.Contains(string(applied), tag) || !strings.Contains(string(applied), input.Credential) || !strings.Contains(string(applied), "unrelated") {
						t.Fatalf("updated identity absent: %v", err)
					}
				}
				input.Port = 24444
				generated, err := serverconfig.Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				task.ConfigContent, err = serverconfig.MutateGenerated(engine, task.ConfigContent, generated, "renamed", "modify")
				if err != nil {
					t.Fatal(err)
				}
				if output, err := e.Execute(ctx, task); err != nil {
					t.Fatalf("new port: %v\n%s", err, output)
				}
				pid, err := managedServiceProcessPID(ctx, manager, spec.Service)
				if err != nil {
					t.Fatal(err)
				}
				if err := processOwnsTCPPort(pid, 24444); err != nil {
					t.Fatal(err)
				}
				old, err := net.Listen("tcp4", "127.0.0.1:24443")
				if err != nil {
					t.Fatalf("old port retained: %v", err)
				}
				old.Close()
				t.Log("same-port validation and deployment passed; rename replaced original and target process owns live listener")
			})
		}
	}
}

// Exercise authentication with the official client against the updated live
// listener; reading a changed file alone cannot prove the process applied it.
func assertRetainedSS2022RoundTrip(t *testing.T, ctx context.Context, input serverconfig.Input, serverPort int) {
	t.Helper()
	echo := singBoxEcho(t, "tcp")
	localPort := singBoxFreePort(t)
	client := exec.CommandContext(ctx, "/usr/local/lib/qagent/cores/sslocal", "-b", fmt.Sprintf("127.0.0.1:%d", localPort), "-s", fmt.Sprintf("127.0.0.1:%d", serverPort), "-m", input.Method, "-k", input.Credential)
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Process.Kill(); _ = client.Wait() }()
	var last error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), nil, &net.Dialer{Timeout: time.Second})
		if err != nil {
			last = err
			continue
		}
		connection, err := dialer.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", echo))
		if err != nil {
			last = err
			continue
		}
		_ = connection.SetDeadline(time.Now().Add(time.Second))
		expected := []byte("qch-retained-credentials")
		_, err = connection.Write(expected)
		reply := make([]byte, len(expected))
		if err == nil {
			_, err = io.ReadFull(connection, reply)
		}
		connection.Close()
		if err == nil && string(reply) == string(expected) {
			return
		}
		last = err
	}
	t.Fatalf("updated credentials did not pass a live TCP request: %v", last)
}
