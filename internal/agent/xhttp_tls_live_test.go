package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"go.yaml.in/yaml/v4"
)

// Called by the existing native matrix in CI. Both kernels are servers here;
// isolated trusted certificates replace public issuance, not TLS verification.
func testStandaloneXHTTPTLSLiveMatrix(t *testing.T, xray, mihomo, nginx string) {
	caddy := os.Getenv("QCH_CADDY_BINARY")
	if caddy == "" {
		caddy = filepath.Join(filepath.Dir(xray), "caddy")
	}
	if mihomo == "" || nginx == "" {
		t.Skip("requires both servers and Nginx")
	}
	if _, err := os.Stat(caddy); err != nil {
		t.Skip("requires actual Caddy")
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineMihomo} {
		for _, mode := range []string{"native", "caddy", "nginx"} {
			t.Run(string(engine)+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				cert, key := xhttpTestCertificate(t, dir)
				// Nginx workers drop privileges; expose fixture directories, never keys.
				for _, path := range []string{dir, filepath.Dir(dir)} {
					if err := os.Chmod(path, 0755); err != nil {
						t.Fatal(err)
					}
				}
				protocol, _ := serverconfig.FindProtocol(engine, serverconfig.ProtocolXHTTPTLS)
				plan, err := serverconfig.NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				plan.TLSMode, plan.Listen, plan.Port, plan.TLSPublicPort = mode, "127.0.0.1", singBoxFreePort(t), singBoxFreePort(t)
				plan.BlockMainlandDestination = false
				plan.TLSServerName, plan.ClientAddress = "tls.example.test", "127.0.0.1"
				plan.CertificatePath, plan.PrivateKeyPath = cert, key
				plan.TransportHost, plan.ClientHost = "tls.example.test", "tls.example.test"
				if mode != "native" {
					plan.TLSStaticHTML = "<!doctype html><title>owned static page</title><p>{http.request.host}</p>"
				}
				content, err := serverconfig.Generate(engine, plan)
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := serverconfig.MarshalClientMetadataWithPrevious(plan, "")
				if err != nil {
					t.Fatal(err)
				}
				frontends, err := serverconfig.ManagedTLSFrontends(engine, content, map[string]string{plan.Tag: metadata})
				if err != nil {
					t.Fatal(err)
				}
				parsed, ok := serverconfig.Parse(engine, content)
				if !ok {
					t.Fatal("parse")
				}
				if err = serverconfig.ApplyClientMetadata(&parsed, metadata); err != nil {
					t.Fatal(err)
				}
				if engine == core.EngineXray {
					xhttpTestCore(t, xray, content, cert)
				} else {
					// The production inbound and users are unchanged; isolate egress to the local echo.
					var root map[string]any
					if err = yaml.Unmarshal([]byte(content), &root); err != nil {
						t.Fatal(err)
					}
					root["rules"] = []string{"MATCH,DIRECT"}
					root["log-level"] = "warning"
					data, _ := json.Marshal(root)
					path := filepath.Join(dir, "mihomo-server.json")
					_ = os.WriteFile(path, data, 0600)
					check := exec.Command(mihomo, "-t", "-d", dir, "-f", path)
					check.Env = append(os.Environ(), "SAFE_PATHS="+dir)
					if output, err := check.CombinedOutput(); err != nil {
						t.Fatalf("server schema: %v %s", err, output)
					}
					xhttpTestProcess(t, mihomo, []string{"-d", dir, "-f", path}, []string{"SAFE_PATHS=" + dir})
				}
				public := plan.Port
				if mode != "native" {
					f := frontends[0]
					public = f.PublicPort
					certData, _ := os.ReadFile(cert)
					keyData, _ := os.ReadFile(key)
					f.Certificate, f.PrivateKey, f.AutomaticCertificate = string(certData), string(keyData), false
					_ = os.WriteFile(filepath.Join(dir, frontendKey(f)+".crt"), certData, 0644)
					_ = os.WriteFile(filepath.Join(dir, frontendKey(f)+".key"), keyData, 0600)
					pageDir := filepath.Join(dir, staticPageFile(f))
					_ = os.Mkdir(pageDir, 0755)
					_ = os.WriteFile(filepath.Join(pageDir, "index.html"), []byte(f.StaticHTML), 0644)
					config := renderNginxFrontends([]core.TLSFrontend{f}, dir, dir)
					proxy := nginx
					path := filepath.Join(dir, "proxy.conf")
					args := []string{"-p", dir + "/", "-c", path}
					if mode == "caddy" {
						proxy = caddy
						config, err = renderCaddyWithFallback([]core.TLSFrontend{f}, dir, nil, dir)
						if err != nil {
							t.Fatal(err)
						}
						var root map[string]any
						_ = json.Unmarshal([]byte(config), &root)
						apps := root["apps"].(map[string]any)
						relocateCaddyChallengeListener(t, root, singBoxFreePort(t))
						apps["tls"] = map[string]any{"certificates": map[string]any{"load_files": []any{map[string]any{"certificate": cert, "key": key}}}}
						for _, server := range apps["http"].(map[string]any)["servers"].(map[string]any) {
							server.(map[string]any)["automatic_https"] = map[string]any{"disable": true}
						}
						data, _ := json.Marshal(root)
						config = string(data)
						args = []string{"run", "--config", path}
					}
					_ = os.WriteFile(path, []byte(config), 0600)
					xhttpTestProcess(t, proxy, args, nil)
					roots := x509.NewCertPool()
					roots.AppendCertsFromPEM(certData)
					client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: plan.TLSServerName}}}
					defer client.CloseIdleConnections()
					address := "https://127.0.0.1:" + strconv.Itoa(public) + "/"
					var body []byte
					for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(40 * time.Millisecond) {
						request, _ := http.NewRequest(http.MethodGet, address, nil)
						request.Host = plan.TLSServerName
						response, err := client.Do(request)
						if err == nil {
							body, _ = io.ReadAll(response.Body)
							response.Body.Close()
							if response.StatusCode == 200 {
								break
							}
						}
					}
					if string(body) != f.StaticHTML {
						t.Fatalf("static page changed or unavailable: %s", body)
					}
					// All bundled pages are served literally, without template expansion.
					for _, page := range protocol.StaticPages {
						if err := os.WriteFile(filepath.Join(pageDir, "index.html"), []byte(page.HTML), 0644); err != nil {
							t.Fatal(err)
						}
						request, _ := http.NewRequest(http.MethodGet, address, nil)
						request.Host = plan.TLSServerName
						response, err := client.Do(request)
						if err != nil {
							t.Fatal(err)
						}
						data, _ := io.ReadAll(response.Body)
						response.Body.Close()
						if response.StatusCode != 200 || string(data) != page.HTML {
							t.Fatal("bundled page changed", page.Key)
						}
					}
					// The Agent's readiness probe must verify the real XHTTP route as well as TLS.
					if err := probeFrontendProtocol(context.Background(), f, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: plan.TLSServerName, NextProtos: []string{"h2"}}); err != nil {
						t.Fatal(err)
					}
					// A page directory cannot serve a sibling's certificate or configuration.
					request, _ := http.NewRequest(http.MethodGet, address+"../"+filepath.Base(cert), nil)
					request.Host = plan.TLSServerName
					response, err := client.Do(request)
					if err != nil {
						t.Fatal(err)
					}
					data, _ := io.ReadAll(response.Body)
					response.Body.Close()
					if strings.Contains(string(data), "BEGIN CERTIFICATE") {
						t.Fatal("static root escaped")
					}
				}
				// Force each native client mode against the generated server/backend.
				for _, upload := range []string{"packet-up", "stream-up", "stream-one"} {
					t.Run(upload, func(t *testing.T) {
						input := parsed
						input.XHTTPMode = upload
						profile, err := serverconfig.BuildClientProfile(input, "127.0.0.1", "")
						if err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(profile.Xray, strconv.Itoa(public)) || strings.Contains(profile.Xray, `"allowInsecure": true`) {
							t.Fatal("invalid public export")
						}
						xhttpAssertClient(t, xray, profile.Xray, cert)
						if profile.Mihomo == "" {
							t.Fatal("missing Mihomo client export")
						}
						xhttpAssertMihomo(t, mihomo, profile.Mihomo, cert)
					})
				}
				// A TLS handshake without this fixture CA must fail.
				connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(public)), &tls.Config{MinVersion: tls.VersionTLS12, ServerName: plan.TLSServerName})
				if err == nil {
					connection.Close()
					t.Fatal("untrusted certificate accepted")
				}
			})
		}
	}
}
