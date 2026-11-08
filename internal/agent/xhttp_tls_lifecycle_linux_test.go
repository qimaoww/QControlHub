//go:build linux

package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"go.yaml.in/yaml/v4"
)

// Invoked inside both existing init-system sandboxes, with official binaries.
// The cached fixture certificate is trusted by the node and by actual clients;
// issuance/renewal are exercised separately by the existing isolated CA tests.
func TestXHTTPTLSManagedLifecycleSandbox(t *testing.T) {
	if os.Getenv("QCH_TEST_XHTTP_TLS_LIFECYCLE") != "1" {
		t.Skip("requires disposable managed TLS node")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires disposable container")
	}
	kind := os.Getenv("QCH_TEST_TLS_MANAGER")
	if kind == "" {
		kind = ServiceManagerSystemd
	}
	manager, err := NewServiceManager(kind)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e := &Executor{Specs: DefaultSpecsForServiceManager(kind), Services: manager, tlsFrontendHelperPath: "/usr/local/lib/qagent/qagent"}
	m := tlsFrontendManager{tlsFrontendRoot, tlsFrontendState, manager, NewCoreUpdater()}
	certPath, keyPath := "/etc/qcontrolhub/tls/xhttp.crt", "/etc/qcontrolhub/tls/xhttp.key"
	cert, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile("/usr/local/share/ca-certificates/qch-xhttp.crt", cert, 0644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(ctx, "/usr/sbin/update-ca-certificates"); err != nil {
		t.Fatalf("fixture trust: %v %s", err, output)
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
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	storage := filepath.Join(tlsFrontendState, "caddy", "certificates", "acme-v02.api.letsencrypt.org-directory", "tls.example.test")
	if err = os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	for path := storage; path != tlsFrontendState; path = filepath.Dir(path) {
		if err = os.Chown(path, int(identity.uid), int(identity.gid)); err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(path, 0750); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []struct{ name, source string }{{"tls.example.test.crt", certPath}, {"tls.example.test.key", keyPath}} {
		data, err := os.ReadFile(entry.source)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(storage, entry.name)
		if err = os.WriteFile(path, data, 0640); err != nil {
			t.Fatal(err)
		}
		if err = os.Chown(path, int(identity.uid), int(identity.gid)); err != nil {
			t.Fatal(err)
		}
	}
	issuerPath := filepath.Join(storage, "tls.example.test.json")
	if err = os.WriteFile(issuerPath, []byte(`{"sans":["tls.example.test"],"issuer_data":{"ca":"https://acme-v02.api.letsencrypt.org/directory"}}`), 0640); err != nil {
		t.Fatal(err)
	}
	if err = os.Chown(issuerPath, int(identity.uid), int(identity.gid)); err != nil {
		t.Fatal(err)
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineMihomo} {
		base := e.Specs[engine]
		if err = os.MkdirAll(filepath.Dir(base.ConfigPath), 0750); err != nil {
			t.Fatal(err)
		}
		if err = e.prepareManagedCoreService(ctx, engine, base, false); err != nil {
			t.Fatal(err)
		}
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%t", engine, shared), func(t *testing.T) {
				task := core.Task{Engine: engine, Action: core.ActionDeploy, SharedInstance: shared, ConfigID: "cfg_xhttp_fixture", ConfigVersion: 1}
				spec := base
				if shared {
					task.SharedTrafficID = "shr_d17ec3a700000001"
					spec, err = sharedInstanceSpec(engine, base, task.SharedTrafficID, kind)
					if err != nil {
						t.Fatal(err)
					}
				}
				t.Cleanup(func() {
					cleanup, done := context.WithTimeout(context.Background(), 20*time.Second)
					defer done()
					if t.Failed() && kind == ServiceManagerSystemd {
						log, _ := run(cleanup, "/usr/bin/journalctl", "--namespace=qagent-cores", "-u", spec.Service, "-n", "40", "--no-pager")
						t.Log(coreDiagnosticRedactor(engine, task.ConfigContent)(log))
					}
					_, _ = manager.command(cleanup, spec.Service, core.ActionStop)
					if err := m.apply(cleanup, spec.Service, nil, core.ActionUninstall); err != nil {
						t.Error(err)
					}
				})
				protocol, _ := serverconfig.FindProtocol(engine, serverconfig.ProtocolXHTTPTLS)
				input, _ := serverconfig.NewPlan(protocol)
				public, backend := singBoxFreePort(t), singBoxFreePort(t)
				input.TLSServerName, input.ClientAddress = "tls.example.test", "127.0.0.1"
				input.CertificatePath, input.PrivateKeyPath = certPath, keyPath
				input.BlockMainlandDestination, input.BlockMainlandSource = false, false
				for index, mode := range []string{"native", "nginx", "caddy", "native", "nginx", "native"} {
					input.TLSMode, input.Listen, input.Port, input.TLSPublicPort = mode, "127.0.0.1", backend, public
					input.TLSStaticHTML = protocol.StaticPages[0].HTML
					if mode == "native" {
						input.Port, input.TLSPublicPort, input.TLSStaticHTML = public, 0, ""
					}
					content, err := serverconfig.Generate(engine, input)
					if err != nil {
						t.Fatal(err)
					}
					if engine == core.EngineMihomo {
						var root map[string]any
						_ = yaml.Unmarshal([]byte(content), &root)
						root["rules"] = []string{"MATCH,DIRECT"} // Permit only this fixture's local echo.
						data, _ := json.Marshal(root)
						content = string(data)
					} else {
						var root map[string]any
						_ = json.Unmarshal([]byte(content), &root)
						for _, item := range root["outbounds"].([]any) {
							outbound := item.(map[string]any)
							if outbound["protocol"] == "freedom" {
								settings, _ := outbound["settings"].(map[string]any)
								if settings == nil {
									settings = map[string]any{}
									outbound["settings"] = settings
								}
								settings["finalRules"] = []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.0/8", "::1/128"}}}
							}
						}
						data, _ := json.Marshal(root)
						content = string(data)
					}
					metadata, _ := serverconfig.MarshalClientMetadataWithPrevious(input, "")
					task.ConfigContent = content
					task.TLSFrontends, err = serverconfig.ManagedTLSFrontends(engine, content, map[string]string{input.Tag: metadata})
					if err != nil {
						t.Fatal(err)
					}
					if index == 3 {
						before, err := os.ReadFile(spec.ConfigPath)
						if err != nil {
							t.Fatal(err)
						}
						occupied, err := net.Listen("tcp", "127.0.0.1:0")
						if err != nil {
							t.Fatal(err)
						}
						extra := input
						extra.Tag, extra.Port = "occupied", occupied.Addr().(*net.TCPAddr).Port
						generated, err := serverconfig.Generate(engine, extra)
						if err != nil {
							occupied.Close()
							t.Fatal(err)
						}
						bad := task
						bad.ConfigContent, err = serverconfig.MutateGenerated(engine, content, generated, "", "add")
						if err != nil {
							occupied.Close()
							t.Fatal(err)
						}
						// Simulate interruption after release, then retry the deployment.
						if err = m.nativeHandoff(ctx, spec.Service, bad.ConfigContent, "release"); err != nil {
							occupied.Close()
							t.Fatal(err)
						}
						_, deployErr := e.Execute(ctx, bad)
						occupied.Close()
						if deployErr == nil {
							t.Fatal("occupied listener deployment reported success")
						}
						after, err := os.ReadFile(spec.ConfigPath)
						if err != nil || string(after) != string(before) {
							t.Fatal("failed native handoff lost the previous core config", err)
						}
						deployments, err := m.load()
						if err != nil || len(deployments[spec.Service].Frontends) != 1 || deployments[spec.Service].NativeHandoff != nil {
							t.Fatal("failed handoff did not restore the owned proxy", err)
						}
						if err = probeFrontendTLS(ctx, deployments[spec.Service].Frontends[0], true); err != nil {
							t.Fatal("failed handoff did not restore live XHTTP", err)
						}
					}
					if output, err := e.Execute(ctx, task); err != nil {
						t.Fatalf("%s deploy: %v\n%s", mode, err, output)
					}
					if err = verifyManagedServiceProcessPermissions(ctx, manager, spec.Service); err != nil {
						t.Fatal(err)
					}
					profile, err := serverconfig.BuildClientProfile(input, "127.0.0.1", "")
					if err != nil {
						t.Fatal(err)
					}
					xhttpAssertClient(t, e.Specs[core.EngineXray].Binary, profile.Xray, certPath)
					if mode != "native" {
						client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: input.TLSServerName}}}
						request, _ := http.NewRequest(http.MethodGet, "https://127.0.0.1:"+strconv.Itoa(public)+"/", nil)
						request.Host = input.TLSServerName
						response, err := client.Do(request)
						if err != nil {
							t.Fatal(err)
						}
						body, _ := io.ReadAll(response.Body)
						response.Body.Close()
						client.CloseIdleConnections()
						if response.StatusCode != 200 || string(body) != input.TLSStaticHTML {
							if mode == "nginx" {
								config, _ := os.ReadFile(filepath.Join(tlsFrontendRoot, "nginx.conf"))
								t.Log(string(config))
								frontend := task.TLSFrontends[0]
								frontend.StaticPageOwner = spec.Service
								info, statErr := os.Stat(filepath.Join(tlsFrontendRoot, staticPageFile(frontend), "index.html"))
								t.Logf("static file: %v %v", info, statErr)
							}
							t.Fatalf("deployed static page differs: status=%d length=%d want=%d prefix=%.200s", response.StatusCode, len(body), len(input.TLSStaticHTML), body)
						}
					}
					deployments, err := m.load()
					if err != nil || deployments[spec.Service].NativeHandoff != nil {
						t.Fatal("handoff was not committed", err)
					}
					t.Logf("%s: actual %s service, strict client and owned resources verified", mode, kind)
					task.ConfigVersion++
				}
			})
		}
	}
}
