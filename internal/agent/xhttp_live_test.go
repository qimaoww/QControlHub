package agent

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
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
	"golang.org/x/net/http2"
	"gopkg.in/yaml.v3"
)

// No Internet endpoint is contacted. TLS and CDN-style endpoints terminate in
// this test namespace, with a private CA trusted only by the spawned clients.
func TestXHTTPLiveMatrix(t *testing.T) {
	xray := os.Getenv("QCH_XRAY_BINARY")
	if xray == "" {
		t.Skip("set QCH_XRAY_BINARY for actual Xray validation and connections")
	}
	nginx := os.Getenv("QCH_NGINX_BINARY")
	dir := t.TempDir()
	certPath, keyPath := xhttpTestCertificate(t, dir)
	type deployment struct {
		key    string
		shared bool
	}
	var deployments []deployment
	for _, key := range []string{serverconfig.ProtocolVLESSXHTTP, serverconfig.ProtocolXHTTPTLS, serverconfig.ProtocolXHTTPTLSReality, serverconfig.ProtocolXHTTPRealityTLS, serverconfig.ProtocolXHTTPFive} {
		deployments = append(deployments, deployment{key: key}, deployment{key: key, shared: true})
	}
	for _, deployment := range deployments {
		key, name := deployment.key, deployment.key
		if deployment.shared {
			name += "/shared-instance"
		}
		t.Run(name, func(t *testing.T) {
			protocol, _ := serverconfig.FindProtocol(core.EngineXray, key)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			publicListener := xhttpReservePort(t)
			serverListeners := []net.Listener{publicListener}
			var apiListener, tlsListener net.Listener
			if !deployment.shared {
				apiListener = xhttpReservePort(t)
				serverListeners = append(serverListeners, apiListener)
			}
			input.Listen, input.Port, input.ClientPort, input.ClientAddress = "::", publicListener.Addr().(*net.TCPAddr).Port, 0, "127.0.0.1"
			input.BlockMainlandDestination, input.BlockMainlandSource = false, false
			input.CertificatePath, input.PrivateKeyPath = certPath, keyPath
			input.TLSServerName, input.RealityServerName, input.XHTTPTLSAddress = "tls.example.test", "reality.example.test", "127.0.0.1"
			group := key != serverconfig.ProtocolVLESSXHTTP && key != serverconfig.ProtocolXHTTPTLS
			if group {
				if nginx == "" {
					t.Skip("set QCH_NGINX_BINARY to verify managed combination")
				}
				backendListener := xhttpReservePort(t)
				tlsListener = xhttpReservePort(t)
				serverListeners = append(serverListeners, backendListener)
				input.XHTTPBackendPort, input.XHTTPTLSPort = backendListener.Addr().(*net.TCPAddr).Port, tlsListener.Addr().(*net.TCPAddr).Port
			}
			content, err := serverconfig.Generate(core.EngineXray, input)
			if err != nil {
				t.Fatal(err)
			}
			if !group && input.RealityEnabled {
				// Public target checks are tested separately; substitute a local TLS1.3/H2
				// target to keep runtime traffic entirely inside the isolated fixture.
				target := xhttpTestTarget(t, certPath, keyPath)
				var root map[string]any
				_ = json.Unmarshal([]byte(content), &root)
				in := root["inbounds"].([]any)[0].(map[string]any)
				in["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)["target"] = target
				data, _ := json.Marshal(root)
				content = string(data)
			}
			if group {
				metadata, err := serverconfig.MarshalClientMetadata(input)
				if err != nil {
					t.Fatal(err)
				}
				frontends, err := serverconfig.ManagedTLSFrontends(core.EngineXray, content, map[string]string{input.Tag: metadata})
				if err != nil {
					t.Fatal(err)
				}
				proxyDir := t.TempDir()
				config := renderNginxFrontends(frontends, proxyDir, proxyDir)
				path := filepath.Join(proxyDir, "nginx.conf")
				_ = os.WriteFile(path, []byte(config), 0600)
				args := []string{"-p", proxyDir + "/", "-c", path}
				if output, err := exec.Command(nginx, append(args, "-t")...).CombinedOutput(); err != nil {
					t.Fatalf("Nginx rejected generated config: %v %s", err, output)
				}
				if err := tlsListener.Close(); err != nil {
					t.Fatal(err)
				}
				xhttpTestProcess(t, nginx, args, nil)
				xhttpWaitPort(t, input.XHTTPTLSPort)
			}
			var accountingSource map[string]any
			_ = json.Unmarshal([]byte(content), &accountingSource)
			if !deployment.shared {
				accountingSource["api"] = map[string]any{"tag": "qch-stat-api", "listen": apiListener.Addr().String()}
			}
			accountingJSON, _ := json.Marshal(accountingSource)
			accounting, err := serverconfig.PlanPresetAccounting(core.EngineXray, string(accountingJSON))
			if err != nil {
				t.Fatal(err)
			}
			if group && accounting.Ports[0].User != input.Username {
				t.Fatal("group lacks independent user statistics")
			}
			runtimeContent := accounting.Content
			if deployment.shared {
				runtimeContent, err = serverconfig.SharedInstanceRuntimeContent(core.EngineXray, runtimeContent)
				if err != nil {
					t.Fatal("shared runtime preparation", err)
				}
				endpoints, err := serverconfig.SharedTrafficEndpoints(core.EngineXray, runtimeContent)
				if err != nil || len(endpoints) != 1 || endpoints[0].Port != input.Port {
					t.Fatalf("shared quota endpoint: %+v %v", endpoints, err)
				}
			}
			xhttpTestCore(t, xray, runtimeContent, certPath, serverListeners...)
			profile, err := serverconfig.BuildClientProfile(input, "127.0.0.1", "")
			if err != nil {
				t.Fatal(err)
			}
			variants := profile.Variants
			if len(variants) == 0 {
				variants = []serverconfig.ClientVariant{{Name: "direct", Profile: profile}}
			}
			for _, variant := range variants {
				t.Run(variant.Name, func(t *testing.T) {
					xhttpAssertClient(t, xray, variant.Profile.Xray, certPath)
					if group && !deployment.shared {
						counters, err := queryNativeTrafficAt(context.Background(), core.EngineXray, accounting.API)
						if err != nil || counters["user>>>"+input.Username+">>>traffic>>>uplink"] == 0 || counters["user>>>"+input.Username+">>>traffic>>>downlink"] == 0 {
							t.Fatalf("group user counters unavailable: %v %v", counters, err)
						}
					}
					if mihomo := os.Getenv("QCH_MIHOMO_BINARY"); mihomo != "" && variant.Profile.Mihomo != "" {
						xhttpAssertMihomo(t, mihomo, variant.Profile.Mihomo, certPath)
					}
				})
			}
			if key == serverconfig.ProtocolXHTTPTLS {
				for _, alpn := range []string{"h2", "http/1.1"} {
					for _, mode := range []string{"packet-up", "stream-up", "stream-one"} {
						t.Run("mode/"+alpn+"/"+mode, func(t *testing.T) {
							modeInput := input
							modeInput.TLSClientALPN, modeInput.XHTTPMode = alpn, mode
							if _, err := serverconfig.Generate(core.EngineXray, modeInput); err != nil {
								t.Fatal(err)
							}
							modeProfile, err := serverconfig.BuildClientProfile(modeInput, "127.0.0.1", "")
							if err != nil {
								t.Fatal(err)
							}
							xhttpAssertClient(t, xray, modeProfile.Xray, certPath)
							if mihomo := os.Getenv("QCH_MIHOMO_BINARY"); mihomo != "" && modeProfile.Mihomo != "" {
								xhttpAssertMihomo(t, mihomo, modeProfile.Mihomo, certPath)
							}
						})
					}
				}
			}
			if !group {
				for _, addresses := range [][2]string{{"127.0.0.1", "127.0.0.1"}, {"::1", "127.0.0.1"}, {"127.0.0.1", "::1"}} {
					t.Run("split/"+strings.Join(addresses[:], "-"), func(t *testing.T) {
						if strings.Contains(strings.Join(addresses[:], ""), ":") {
							listener, err := net.Listen("tcp6", "[::1]:0")
							if err != nil {
								t.Skip("host lacks IPv6 loopback")
							}
							listener.Close()
						}
						var client map[string]any
						_ = json.Unmarshal([]byte(profile.Xray), &client)
						out := client["outbounds"].([]any)[0].(map[string]any)
						out["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["address"] = addresses[0]
						stream := out["streamSettings"].(map[string]any)
						copyBytes, _ := json.Marshal(stream)
						var down map[string]any
						_ = json.Unmarshal(copyBytes, &down)
						down["address"], down["port"] = addresses[1], input.Port
						stream["xhttpSettings"].(map[string]any)["extra"] = map[string]any{"downloadSettings": down}
						data, _ := json.Marshal(client)
						xhttpAssertClient(t, xray, string(data), certPath)
					})
				}
			}
		})
	}
}

// Keep every fixture port bound until its process is ready to start. Releasing
// ports as they are selected lets later listeners or client connections reuse them.
func xhttpReservePort(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func xhttpWaitPort(t *testing.T, port int) {
	t.Helper()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	var last error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		last = err
	}
	t.Fatalf("fixture listener %s did not start: %v", address, last)
}

func xhttpAssertClient(t *testing.T, binary, content, certPath string) {
	t.Helper()
	echo := singBoxEcho(t, "tcp")
	listener := xhttpReservePort(t)
	port := listener.Addr().(*net.TCPAddr).Port
	var root map[string]any
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		t.Fatal(err)
	}
	root["inbounds"] = []any{map[string]any{"tag": "local", "listen": "127.0.0.1", "port": port, "protocol": "tunnel", "settings": map[string]any{"address": "127.0.0.1", "port": echo, "network": "tcp"}}}
	data, _ := json.Marshal(root)
	xhttpTestCore(t, binary, string(data), certPath, listener)
	var last error
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(40 * time.Millisecond) {
		last = singBoxEchoRoundTrip("tcp", port)
		if last == nil {
			return
		}
	}
	t.Fatal("exported client failed TCP round trip", last)
}

func xhttpTestCore(t *testing.T, binary, content, certPath string, listeners ...net.Listener) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	check := exec.Command(binary, "run", "-test", "-config", path)
	check.Env = append(os.Environ(), "SSL_CERT_FILE="+certPath)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("Xray rejected generated config: %v %s", err, output)
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	xhttpTestProcess(t, binary, []string{"run", "-config", path}, []string{"SSL_CERT_FILE=" + certPath})
	for _, listener := range listeners {
		xhttpWaitPort(t, listener.Addr().(*net.TCPAddr).Port)
	}
}

func xhttpTestProcess(t *testing.T, binary string, args, env []string) {
	t.Helper()
	log, err := os.CreateTemp(t.TempDir(), "process-")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = log.Close()
		if t.Failed() {
			data, _ := os.ReadFile(log.Name())
			t.Logf("%s: %s", binary, data)
		}
	})
}

func xhttpTestCertificate(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "XHTTP isolated test"}, DNSNames: []string{"tls.example.test", "reality.example.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pk, _ := x509.MarshalPKCS8PrivateKey(key)
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0640); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func xhttpTestTarget(t *testing.T, certPath, keyPath string) string {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }), TLSConfig: &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2"}, CurvePreferences: []tls.CurveID{tls.X25519}}}
	if err := http2.ConfigureServer(s, &http2.Server{}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(tls.NewListener(l, s.TLSConfig)) }()
	t.Cleanup(func() { _ = s.Close() })
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(l.Addr().(*net.TCPAddr).Port))
}

func TestXHTTPOldKernelAndListenerConflict(t *testing.T) {
	for _, version := range []struct {
		value string
		valid bool
	}{{"24.12.14", false}, {"24.12.15", true}, {"26.3.27", true}, {"unrecognized", false}} {
		binary := filepath.Join(t.TempDir(), "xray")
		writeExecutable(t, binary, "#!/bin/sh\nprintf 'Xray "+version.value+"\\n'\n")
		err := validateXrayXHTTPCapabilities(context.Background(), binary, `{"inbounds":[{"streamSettings":{"network":"xhttp"}}]}`)
		if (err == nil) != version.valid {
			t.Fatalf("version %s: %v", version.value, err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	content := fmt.Sprintf(`{"inbounds":[{"listen":"127.0.0.1","port":%d,"streamSettings":{"network":"xhttp"}}]}`, listener.Addr().(*net.TCPAddr).Port)
	if err := checkXHTTPListenerPorts(EngineSpec{ConfigPath: filepath.Join(t.TempDir(), "new.json")}, content); err == nil {
		t.Fatal("listener conflict accepted")
	}
}

// Basic Mihomo maps are verified against the pinned official client as well.
// Arbitrary Xray extra objects intentionally do not produce partial YAML.
func xhttpAssertMihomo(t *testing.T, binary, proxyJSON, certPath string) {
	t.Helper()
	var proxy map[string]any
	if err := yaml.Unmarshal([]byte(proxyJSON), &proxy); err != nil {
		t.Fatal(err)
	}
	echo := singBoxEcho(t, "tcp")
	listener := xhttpReservePort(t)
	port := listener.Addr().(*net.TCPAddr).Port
	content, _ := json.Marshal(map[string]any{"mixed-port": port, "bind-address": "127.0.0.1", "mode": "rule", "log-level": "silent", "proxies": []any{proxy}, "rules": []string{"MATCH," + proxy["name"].(string)}})
	dir := t.TempDir()
	path := filepath.Join(dir, "mihomo.json")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	check := exec.Command(binary, "-t", "-d", dir, "-f", path)
	check.Env = append(os.Environ(), "SSL_CERT_FILE="+certPath)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("Mihomo rejected export: %v %s", err, output)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	xhttpTestProcess(t, binary, []string{"-d", dir, "-f", path}, []string{"SSL_CERT_FILE=" + certPath})
	xhttpWaitPort(t, port)
	var last error
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(40 * time.Millisecond) {
		last = func() error {
			conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
			if err != nil {
				return err
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			target := net.JoinHostPort("127.0.0.1", strconv.Itoa(echo))
			if _, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
				return err
			}
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
			if err != nil {
				return err
			}
			if response.StatusCode != 200 {
				return fmt.Errorf("CONNECT: %s", response.Status)
			}
			payload := "qch-xhttp-mihomo-verified"
			if _, err = io.WriteString(conn, payload); err != nil {
				return err
			}
			data := make([]byte, len(payload))
			if _, err = io.ReadFull(reader, data); err != nil {
				return err
			}
			if string(data) != payload {
				return fmt.Errorf("Mihomo payload mismatch")
			}
			return nil
		}()
		if last == nil {
			return
		}
	}
	t.Fatal("Mihomo exported client failed TCP echo", last)
}
