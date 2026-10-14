//go:build linux

package agent

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"io/fs"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"gopkg.in/yaml.v3"
)

// Exercise installed paths and production Execute branches on a disposable
// systemd runner. Shared-only mode also supports isolated tests on an existing
// node, refusing to replace any existing shared instance.
func TestMihomoRealityAndTLSManagedLifecycle(t *testing.T) {
	if os.Getenv("QCH_LIVE_REALITY_SYSTEMD_TEST") != "1" {
		t.Skip("opt-in disposable systemd lifecycle fixture")
	}
	if os.Geteuid() != 0 {
		t.Fatal("requires root and systemd")
	}
	base := DefaultSpecs()[core.EngineMihomo]
	const sharedID = "shr_f17ec3a700000001"
	sharedOnly := os.Getenv("QCH_LIVE_REALITY_SHARED_ONLY") == "1"
	sharedSpec, err := sharedInstanceSpec(core.EngineMihomo, base, sharedID, ServiceManagerSystemd)
	if err != nil {
		t.Fatal(err)
	}
	sharedState := coreCertificateWorkingDirectory(core.EngineMihomo, sharedSpec, ServiceManagerSystemd)
	owned := []string{filepath.Dir(sharedSpec.ConfigPath), sharedState}
	if !sharedOnly {
		owned = append(owned, base.Binary, filepath.Dir(base.ConfigPath), "/etc/systemd/system/"+base.Service, "/etc/systemd/system/qagent-mihomo@.service", "/var/lib/qcontrolhub-mihomo")
	}
	for _, path := range owned {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("refusing to replace existing path %s", path)
		}
	}
	manager, err := NewServiceManager(ServiceManagerSystemd)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	// Register cleanup before the first write. The shared-only mode is safe
	// on an existing node: the owner's binary, unit, config and PID are retained.
	t.Cleanup(func() {
		services := []string{sharedSpec.Service}
		if !sharedOnly {
			services = append(services, base.Service)
		}
		for _, service := range services {
			_, _ = run(context.Background(), systemctlPath, "stop", service)
		}
		for _, path := range owned {
			_ = os.RemoveAll(path)
		}
		_, _ = run(context.Background(), systemctlPath, "daemon-reload")
	})
	if !sharedOnly {
		for _, path := range []string{filepath.Dir(base.Binary), filepath.Dir(base.ConfigPath)} {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		binary, err := os.ReadFile(filepath.Join(os.Getenv("QCH_PERMISSION_BIN"), "mihomo"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base.Binary, binary, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{base.Service, "qagent-mihomo@.service"} {
		path := "/etc/systemd/system/" + name
		if sharedOnly {
			if name == base.Service {
				continue
			}
			if _, err := os.Lstat(path); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			owned = append(owned, path)
		}
		unit, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/systemd/"+name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, unit, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if output, err := run(context.Background(), systemctlPath, "daemon-reload"); err != nil {
		t.Fatalf("%v %s", err, output)
	}
	certPEM, keyPEM := realityFixtureCertificate(t)
	keyPair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	cover := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "TLS cover") }))
	cover.EnableHTTP2 = true
	cover.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{keyPair}}
	cover.StartTLS()
	defer cover.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "reality-and-tls-ok") }))
	defer backend.Close()
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineMihomo: base}, Services: manager}
	for _, shared := range []bool{false, true} {
		if sharedOnly && !shared {
			continue
		}
		name := "ordinary"
		spec := base
		if shared {
			name = "shared"
			spec, err = sharedInstanceSpec(core.EngineMihomo, base, sharedID, ServiceManagerSystemd)
			if err != nil {
				t.Fatal(err)
			}
			if err := prepareSharedInstanceDirectory(core.EngineMihomo, spec, ServiceManagerSystemd); err != nil {
				t.Fatal(err)
			}
		}
		t.Run(name, func(t *testing.T) {
			state := coreCertificateWorkingDirectory(core.EngineMihomo, spec, ServiceManagerSystemd)
			if err := os.MkdirAll(state, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(state, int(identity.uid), int(identity.gid)); err != nil {
				t.Fatal(err)
			}
			for file, data := range map[string]string{"cert.pem": certPEM, "key.pem": keyPEM} {
				path := filepath.Join(state, file)
				if err := os.WriteFile(path, []byte(data), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(path, 0, int(identity.gid)); err != nil {
					t.Fatal(err)
				}
			}
			protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolVLESS)
			reality, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			reality.Listen, reality.Port, reality.RealityServerName = "127.0.0.1", availableTCPPort(t), "www.getty.edu"
			generated, err := serverconfig.Generate(core.EngineMihomo, reality)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := yaml.Unmarshal([]byte(generated), &root); err != nil {
				t.Fatal(err)
			}
			listeners := root["listeners"].([]any)
			listeners[0].(map[string]any)["reality-config"].(map[string]any)["dest"] = cover.Listener.Addr().String()
			if target := os.Getenv("QCH_LIVE_REALITY_TARGET"); target != "" {
				// Optional node-local public target check retains production SSRF,
				// trust and TLS rules. CI uses only the controlled local TLS cover.
				probe, err := serverconfig.ProbeRealityTarget(context.Background(), target)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("target=%s address=%s ALPN=%s verified TLS1.3", target, probe.Address, probe.ALPN)
				listeners[0].(map[string]any)["reality-config"].(map[string]any)["dest"] = target + ":443"
			}
			tlsPort, inlinePort := availableTCPPort(t), availableTCPPort(t)
			listeners = append(listeners,
				map[string]any{"name": "tls-files", "type": "trojan", "listen": "127.0.0.1", "port": tlsPort, "users": []any{map[string]any{"username": "fixture", "password": "fixture-tls-password"}}, "certificate": "cert.pem", "private-key": "key.pem"},
				map[string]any{"name": "tls-inline", "type": "trojan", "listen": "127.0.0.1", "port": inlinePort, "users": []any{map[string]any{"username": "fixture", "password": "fixture-tls-password"}}, "certificate": certPEM, "private-key": keyPEM})
			root["listeners"] = listeners
			data, err := yaml.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			redact := coreDiagnosticRedactor(core.EngineMihomo, content)
			if sharedOnly {
				if output, err := executor.Execute(context.Background(), core.Task{Action: core.ActionValidate, Engine: core.EngineMihomo, ConfigContent: generated}); err != nil {
					t.Fatalf("owner Reality save validation: %v %s", err, output)
				}
			}
			// These negative controls execute the actual systemd identity and
			// sandbox without changing any deployed configuration.
			if err := os.Chmod(filepath.Join(state, "key.pem"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := executor.verifyCoreCertificateAccess(context.Background(), core.EngineMihomo, spec, content, identity); err == nil || !strings.Contains(err.Error(), "file unreadable") {
				t.Fatalf("unreadable TLS key was accepted: %v", err)
			}
			if err := os.Chmod(filepath.Join(state, "key.pem"), 0o640); err != nil {
				t.Fatal(err)
			}
			missing := strings.Replace(content, "certificate: cert.pem", "certificate: missing.pem", 1)
			if err := executor.verifyCoreCertificateAccess(context.Background(), core.EngineMihomo, spec, missing, identity); err == nil || !strings.Contains(err.Error(), "file not found") {
				t.Fatalf("missing TLS certificate was accepted: %v", err)
			}
			// Save validation checks the exact snapshot under the real service
			// identity. Shared deploy itself also takes the public task route.
			if output, err := executor.validate(context.Background(), core.EngineMihomo, spec, content); err != nil {
				t.Fatalf("save validation: %v %s", err, redact(output))
			}
			task := core.Task{Action: core.ActionDeploy, Engine: core.EngineMihomo, ConfigContent: content, SharedInstance: shared}
			if shared {
				task.SharedTrafficID = sharedID
			}
			if output, err := executor.Execute(context.Background(), task); err != nil {
				t.Fatalf("deploy: %v %s", err, output)
			}
			if err := verifyManagedServiceProcessPermissions(context.Background(), manager, spec.Service); err != nil {
				t.Fatal(err)
			}
			profile, err := serverconfig.BuildClientProfileNamed(reality, "127.0.0.1", "", "REALITY")
			if err != nil {
				t.Fatal(err)
			}
			var proxy map[string]any
			if err := yaml.Unmarshal([]byte(profile.Mihomo), &proxy); err != nil {
				t.Fatal(err)
			}
			opts := proxy["reality-opts"].(map[string]any)
			if proxy["servername"] != reality.RealityServerName || opts["public-key"] != reality.RealityPublicKey || opts["short-id"] != reality.RealityShortID || strings.Contains(profile.Mihomo, reality.RealityPrivateKey) {
				t.Fatal("client export differs from server Reality parameters")
			}
			for _, client := range []map[string]any{proxy,
				{"name": "TLS-file", "type": "trojan", "server": "127.0.0.1", "port": tlsPort, "password": "fixture-tls-password", "sni": "www.getty.edu"},
				{"name": "TLS-inline", "type": "trojan", "server": "127.0.0.1", "port": inlinePort, "password": "fixture-tls-password", "sni": "www.getty.edu"},
			} {
				clientPort := availableTCPPort(t)
				clientData, err := yaml.Marshal(map[string]any{"mixed-port": clientPort, "proxies": []any{client}, "rules": []string{"MATCH," + client["name"].(string)}, "tls": map[string]any{"custom-certifactes": []string{certPEM}}})
				if err != nil {
					t.Fatal(err)
				}
				process := startMihomoForTrafficTest(t, base.Binary, t.TempDir(), "config.yaml", string(clientData), clientPort)
				assertProxyTraffic(t, clientPort, backend.URL, "reality-and-tls-ok", func() string { return "managed server " + spec.Service }, func() string { return redact(process.log(t)) })
				process.stop(t)
			}
			t.Log("save validation -> deploy -> service identity/sandbox -> exported Reality, file TLS and inline TLS traffic passed")
		})
	}
}

func realityFixtureCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "www.getty.edu"}, DNSNames: []string{"www.getty.edu"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}
