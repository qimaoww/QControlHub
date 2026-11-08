//go:build linux

package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	// Both engines include real shared proxy rotation and per-inbound removal.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	// The sandbox's original fixture is a CA. The certificate center accepts
	// server leaves, so issue a real leaf and retain that CA for strict clients.
	authority, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(authority.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	issueLeaf := func() []byte {
		leaf := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "TLS fixture"}, DNSNames: []string{"tls.example.test", "reality.example.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
		der, err := x509.CreateCertificate(rand.Reader, leaf, ca, ca.PublicKey, authority.PrivateKey)
		if err != nil {
			t.Fatal(err)
		}
		return append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: authority.Certificate[0]})...)
	}
	cert = issueLeaf()
	if err = os.WriteFile(certPath, cert, 0640); err != nil {
		t.Fatal(err)
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
	if err = m.prepareDirectories(); err != nil {
		t.Fatal(err)
	}
	if err = m.ensureDependency(ctx, "caddy"); err != nil {
		t.Fatal(err)
	}
	fixtureCaddy, err := os.ReadFile(managedCaddyBinary)
	if err != nil {
		t.Fatal(err)
	}
	seedCaddy := func() {
		if _, err := os.Stat(managedCaddyBinary); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(managedCaddyBinary, fixtureCaddy, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(managedCaddyBinary, 0755); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := atomicDeploy(managedCaddyBinary+".qch-owned", fmt.Sprintf("%x", sha256.Sum256(fixtureCaddy))); err != nil {
			t.Fatal(err)
		}

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
				key, err := os.ReadFile(keyPath)
				if err != nil {
					t.Fatal(err)
				}
				certificateID := "crt_d17ec3a700000001"
				task := core.Task{Engine: engine, Action: core.ActionDeploy, SharedInstance: shared, ConfigID: "cfg_xhttp_fixture", ConfigVersion: 1, Certificates: []core.DeploymentCertificate{{ID: certificateID, Version: 1, Certificate: string(cert), PrivateKey: string(key)}}}
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
				input.PanelCertificateID = certificateID
				input.CertificatePath, input.PrivateKeyPath = core.PanelCertificatePrefix+certificateID+"/cert.pem", core.PanelCertificatePrefix+certificateID+"/key.pem"
				input.BlockMainlandDestination, input.BlockMainlandSource = false, false
				otherService := "qagent-xray@shr_d17ec3a700000002.service"
				if kind == ServiceManagerOpenRC {
					otherService = "qagent-xray-shr_d17ec3a700000002"
				}
				var reserved core.TLSFrontend
				for index, mode := range []string{"native", "nginx", "caddy", "native", "nginx", "native"} {
					if mode != "native" {
						seedCaddy()
					}
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
						if err = preparePanelCertificates(spec, &bad); err != nil {
							occupied.Close()
							t.Fatal(err)
						}
						candidate, warning := e.prepareNativeAccountingContent(ctx, engine, spec, bad.ConfigContent)
						if warning != "" {
							t.Fatal(warning)
						}
						if shared {
							candidate, err = serverconfig.SharedInstanceRuntimeContent(engine, candidate)
							if err != nil {
								t.Fatal(err)
							}
						}
						if err = m.nativeHandoff(ctx, spec.Service, candidate, "release"); err != nil {
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
							t.Fatal("failed handoff did not restore the owned proxy", err, deployErr)
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
					// Each mode owns its client/echo processes. Release them before
					// a later mode reclaims the currently unused backend port.
					if !t.Run(mode+"-client", func(t *testing.T) {
						xhttpAssertClient(t, e.Specs[core.EngineXray].Binary, profile.Xray, certPath)
					}) {
						t.FailNow()
					}
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
					if (index == 1 || index == 2) && !shared {
						// Different protocols and instances share the existing
						// proxy process and HTTPS port; each keeps its path.
						secondary := &Executor{Specs: DefaultSpecsForServiceManager(kind), Services: manager, tlsFrontendHelperPath: e.tlsFrontendHelperPath}
						sbSpec := secondary.Specs[core.EngineSingBox]
						t.Cleanup(func() {
							cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
							defer cancel()
							_, _ = manager.command(cleanup, sbSpec.Service, core.ActionStop)
							if err := m.apply(cleanup, sbSpec.Service, nil, core.ActionUninstall); err != nil {
								t.Error("cross-protocol cleanup", err)
							}
						})
						if err := os.MkdirAll(filepath.Dir(sbSpec.ConfigPath), 0750); err != nil {
							t.Fatal(err)
						}
						if err := secondary.prepareManagedCoreService(ctx, core.EngineSingBox, sbSpec, false); err != nil {
							t.Fatal(err)
						}
						other := core.Task{Engine: core.EngineSingBox, Action: core.ActionDeploy, ConfigID: "cfg_multi_protocol", ConfigVersion: 1, Certificates: task.Certificates}
						var plans []serverconfig.Input
						metadata := map[string]string{}
						for _, key := range []string{"vless-ws-tls", "vmess-httpupgrade-tls"} {
							protocol, ok := serverconfig.FindProtocol(core.EngineSingBox, key)
							if !ok {
								t.Fatal("missing cross-protocol fixture", key)
							}
							plan, err := serverconfig.NewPlan(protocol)
							if err != nil {
								t.Fatal(err)
							}
							plan.Port, plan.TLSPublicPort, plan.TLSMode = singBoxFreePort(t), public, mode
							plan.TLSServerName, plan.ClientAddress, plan.PanelCertificateID = input.TLSServerName, "127.0.0.1", certificateID
							plan.Listen, plan.BlockMainlandDestination, plan.BlockMainlandSource = "127.0.0.1", false, false
							plan.CertificatePath, plan.PrivateKeyPath = input.CertificatePath, input.PrivateKeyPath
							generated, err := serverconfig.Generate(core.EngineSingBox, plan)
							if err != nil {
								t.Fatal(err)
							}
							if other.ConfigContent == "" {
								other.ConfigContent = generated
							} else {
								other.ConfigContent, err = serverconfig.MutateGenerated(core.EngineSingBox, other.ConfigContent, generated, "", "add")
								if err != nil {
									t.Fatal(err)
								}
							}
							metadata[plan.Tag], err = serverconfig.MarshalClientMetadataWithPrevious(plan, "")
							if err != nil {
								t.Fatal(err)
							}
							plans = append(plans, plan)
						}
						other.TLSFrontends, err = serverconfig.ManagedTLSFrontends(core.EngineSingBox, other.ConfigContent, metadata)
						if err != nil {
							t.Fatal(err)
						}
						// Only the local test echo is exempted from private routing.
						var sbRoot map[string]any
						if err = json.Unmarshal([]byte(other.ConfigContent), &sbRoot); err != nil {
							t.Fatal(err)
						}
						route, _ := sbRoot["route"].(map[string]any)
						if route == nil {
							route = map[string]any{}
							sbRoot["route"] = route
						}
						rules, _ := route["rules"].([]any)
						route["rules"] = append([]any{map[string]any{"ip_cidr": []string{"127.0.0.0/8"}, "outbound": "direct"}}, rules...)
						encoded, _ := json.Marshal(sbRoot)
						other.ConfigContent = string(encoded)
						if output, err := secondary.Execute(ctx, other); err != nil {
							t.Fatalf("shared proxy deployment: %v\n%s", err, output)
						}
						proxy, _, _ := m.serviceDefinition(mode, false)
						beforePID, err := managedServiceProcessPID(ctx, manager, proxy)
						if err != nil {
							t.Fatal(err)
						}
						assertAll := func() {
							for _, plan := range append([]serverconfig.Input{input}, plans...) {
								profile, err := serverconfig.BuildClientProfile(plan, "127.0.0.1", "")
								if err != nil {
									t.Fatal(err)
								}
								if !t.Run("shared-"+plan.Protocol, func(t *testing.T) {
									if plan.Transport == "xhttp" {
										xhttpAssertClient(t, e.Specs[core.EngineXray].Binary, profile.Xray, certPath)
										return
									}
									// These presets export their native sing-box client JSON.
									outbound, err := serverconfig.BuildClientOutbound(core.EngineSingBox, plan, "127.0.0.1", "")
									if err != nil {
										t.Fatal(err)
									}
									client := map[string]any{"outbounds": []any{json.RawMessage(outbound)}}
									echo, port := singBoxEcho(t, "tcp"), singBoxFreePort(t)
									client["inbounds"] = []any{map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": port, "network": "tcp", "override_address": "127.0.0.1", "override_port": echo}}
									data, _ := json.Marshal(client)
									logs := singBoxRun(t, sbSpec.Binary, plan.Protocol, string(data))
									var last error
									for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(40 * time.Millisecond) {
										last = singBoxEchoRoundTrip("tcp", port)
										if last == nil {
											return
										}
									}
									t.Fatal("exported shared-protocol client failed", last, logs())
								}) {
									t.FailNow()
								}
							}
						}
						assertAll()
						// Certificate rotation uses the same pinned execution channel.
						other.Certificates = append([]core.DeploymentCertificate{}, other.Certificates...)
						other.Certificates[0].Version = index + 1
						other.Certificates[0].Certificate = string(issueLeaf())
						if output, err := secondary.Execute(ctx, other); err != nil {
							t.Fatalf("shared proxy renewal: %v\n%s", err, output)
						}
						assertAll()
						// Remove just HTTPUpgrade and retain WS plus XHTTP.
						other.ConfigContent, err = serverconfig.MutateGenerated(core.EngineSingBox, other.ConfigContent, "", plans[1].Tag, "delete")
						if err != nil {
							t.Fatal(err)
						}
						delete(metadata, plans[1].Tag)
						other.TLSFrontends, err = serverconfig.ManagedTLSFrontends(core.EngineSingBox, other.ConfigContent, metadata)
						if err != nil {
							t.Fatal(err)
						}
						plans = plans[:1]
						if output, err := secondary.Execute(ctx, other); err != nil {
							t.Fatalf("shared route deletion: %v\n%s", err, output)
						}
						assertAll()
						if _, err := manager.command(ctx, sbSpec.Service, core.ActionStop); err != nil {
							t.Fatal(err)
						}
						if err := m.apply(ctx, sbSpec.Service, nil, core.ActionUninstall); err != nil {
							t.Fatal(err)
						}
						afterPID, err := managedServiceProcessPID(ctx, manager, proxy)
						if err != nil || afterPID != beforePID {
							t.Fatal("removing protocols restarted or removed the shared proxy", err)
						}
						profile, err := serverconfig.BuildClientProfile(input, "127.0.0.1", "")
						if err != nil {
							t.Fatal(err)
						}
						if !t.Run("remaining-xhttp", func(t *testing.T) { xhttpAssertClient(t, e.Specs[core.EngineXray].Binary, profile.Xray, certPath) }) {
							t.FailNow()
						}
						t.Log("XHTTP, WebSocket and HTTPUpgrade shared one real proxy process; rotation and per-inbound deletion retained the other connections")
					}

					if index == 2 {
						deployed, err := m.load()
						if err != nil {
							t.Fatal(err)
						}
						reserved = deployed[spec.Service].Frontends[0]
						reserved.PublicPort = singBoxFreePort(t)
						if err := m.apply(ctx, otherService, []core.TLSFrontend{reserved}, core.ActionDeploy); err != nil {
							t.Fatal(err)
						}
						if err := m.apply(ctx, otherService, nil, core.ActionStop); err != nil {
							t.Fatal(err)
						}
					}
					if index == 3 {
						reserved.StaticPageOwner = otherService
						reservedPage := filepath.Join(m.root, staticPageFile(reserved), "index.html")
						if _, err := os.Stat(reservedPage); err != nil {
							t.Fatal("another stopped instance lost its page", err)
						}
						_, unit, _ := m.serviceDefinition("caddy", false)
						original, err := os.ReadFile(unit)
						if err != nil {
							t.Fatal("another stopped instance lost its service", err)
						}
						if err = os.WriteFile(unit, append(original, []byte("\n# operator customization\n")...), 0755); err != nil {
							t.Fatal(err)
						}
						if err = m.apply(ctx, otherService, nil, core.ActionUninstall); err == nil || !strings.Contains(err.Error(), "反代资源清理") {
							t.Fatal("customized service cleanup did not remain pending", err)
						}
						pending, err := m.load()
						if err != nil || len(pending[otherService].RetiredFrontends) == 0 {
							t.Fatal("cleanup failure lost its durable references", err)
						}
						if err = os.WriteFile(unit, original, 0755); err != nil {
							t.Fatal(err)
						}
						if err = e.repairTLSRuntime(ctx); err != nil {
							t.Fatal("restart cleanup retry", err)
						}
						for _, path := range []string{unit, filepath.Join(m.root, "caddy.conf"), reservedPage, filepath.Join(m.state, "caddy"), managedCaddyBinary, managedCaddyBinary + ".qch-owned"} {
							if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
								t.Fatalf("last reference retained %s: %v", path, err)
							}
						}
						if _, err = os.Stat(certPath); err != nil {
							t.Fatal("cleanup removed an external certificate", err)
						}
						t.Log("stopped shared references preserved; final release uninstalled resources; durable cleanup failure recovered through Agent repair")
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
