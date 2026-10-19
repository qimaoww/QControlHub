package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestManagedCertificateScannerScopesManifestFilesAndSecrets(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires ownership validation")
	}
	// A protected task-owned root allows the same parent-chain checks as /var/lib.
	root, err := os.MkdirTemp("/var/lib", "qch-managed-cert-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	m := tlsFrontendManager{root: filepath.Join(root, "manifest"), state: filepath.Join(root, "state")}
	certdir := filepath.Join(m.state, "caddy", "certificates", "fixture", "edge.example.test")
	for _, dir := range []string{m.root, certdir} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	pair, _ := certificates.SelfSigned([]string{"edge.example.test"}, "ecdsa256", 90)
	f := core.TLSFrontend{Tag: "a", Mode: "caddy", Domain: "edge.example.test", PublicPort: 443, BackendPort: 20001, Transport: "websocket", Path: "/a"}
	spec := DefaultSpecsForServiceManager(ServiceManagerSystemd)[core.EngineSingBox]
	manifest, _ := json.Marshal(tlsDeployments{spec.Service: {Frontends: []core.TLSFrontend{f}}})
	if err = os.WriteFile(filepath.Join(m.root, "deployments.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	configuration, _ := renderCaddyFrontends([]core.TLSFrontend{f}, m.state)
	os.WriteFile(filepath.Join(m.root, "caddy.conf"), []byte(configuration), 0600)
	metadata := []byte(`{"issuer_data":{"ca":"` + letsEncryptDirectory + `"}}`)
	os.WriteFile(filepath.Join(certdir, "edge.example.test.json"), metadata, 0600)
	certpath, keypath := filepath.Join(certdir, "edge.example.test.crt"), filepath.Join(certdir, "edge.example.test.key")
	os.WriteFile(certpath, []byte(pair.Certificate), 0600)
	os.WriteFile(keypath, []byte(pair.PrivateKey), 0600)
	source := core.ManagedCertificateSource{ID: "source", Engine: core.EngineSingBox, Domain: f.Domain}
	check := func(want bool) {
		t.Helper()
		report := m.scanManagedCertificate(source, ServiceManagerSystemd)
		if (report.Error == "") != want {
			t.Fatalf("scan success=%v expected=%v", report.Error == "", want)
		}
		if !want && (report.PrivateKey != "" || report.Certificate != "") {
			t.Fatal("failed scan retained secret")
		}
	}
	check(true)
	os.WriteFile(filepath.Join(certdir, "edge.example.test.json"), []byte(`{"issuer_data":{"ca":"https://foreign.example.test/dir"}}`), 0600)
	check(false)
	os.WriteFile(filepath.Join(certdir, "edge.example.test.json"), metadata, 0600)
	foreign := source
	foreign.Domain = "foreign.example.test"
	if report := m.scanManagedCertificate(foreign, ServiceManagerSystemd); report.PrivateKey != "" || report.Error == "" {
		t.Fatal("manifest boundary escaped")
	}
	os.Rename(keypath, keypath+".original")
	os.Symlink(keypath+".original", keypath)
	check(false)
	os.Remove(keypath)
	os.Rename(keypath+".original", keypath)
	os.Link(keypath, keypath+".hardlink")
	check(false)
	os.Remove(keypath + ".hardlink")
	os.Chmod(keypath, 0666)
	check(false)
	os.Chmod(keypath, 0600)
	os.WriteFile(keypath, []byte("not a private key"), 0600)
	check(false)
	// Even a link to an otherwise safe certificate subtree is excluded.
	os.Rename(filepath.Join(m.state, "caddy"), filepath.Join(m.state, "outside"))
	os.Symlink(filepath.Join(m.state, "outside"), filepath.Join(m.state, "caddy"))
	check(false)
}

func TestManagedCertificateReadHelperHasNoWriteAccessToCaddyOrNetwork(t *testing.T) {
	args := strings.Join(managedCertificateHelperArguments("/usr/local/bin/qagent", "qagent-cert-read-test.service", "abcdef012345abcdef012345.json"), "\n")
	for _, expected := range []string{"CapabilityBoundingSet=CAP_DAC_READ_SEARCH", "ProtectSystem=strict", "ReadWritePaths=" + tlsFrontendRequests, "RestrictAddressFamilies=AF_UNIX", "UMask=0077", "manage-tls"} {
		if !strings.Contains(args, expected) {
			t.Fatalf("missing restricted helper property %s", expected)
		}
	}
	for _, forbidden := range []string{"ReadWritePaths=/var/lib/qcontrolhub-tls", "CAP_DAC_OVERRIDE", "AF_INET", "CAP_SETUID"} {
		if strings.Contains(args, forbidden) {
			t.Fatalf("broad helper privilege: %s", forbidden)
		}
	}
}
