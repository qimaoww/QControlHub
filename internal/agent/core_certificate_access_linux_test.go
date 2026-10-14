//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCertificateAccessChecksActualIdentityExistenceAndTraversal(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to exercise another service identity")
	}
	root, err := os.MkdirTemp("", "qch-certificate-access-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "cert.pem")
	if err := os.WriteFile(path, []byte("certificate fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{}
	configDirectory := filepath.Join(root, "configs")
	if err := os.Mkdir(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := EngineSpec{ConfigPath: filepath.Join(configDirectory, "config.json"), Service: "qagent-sing-box.service"}
	content := fmt.Sprintf(`{"inbounds":[{"tls":{"certificate_path":%q}}]}`, path)
	identity := commandIdentity{uid: 65534, gid: 65534, groups: []uint32{65534}}
	check := func(want string) {
		t.Helper()
		err := executor.verifyCoreCertificateAccess(context.Background(), core.EngineSingBox, spec, content, identity)
		if want == "" {
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "inbounds[0].tls.certificate_path") {
			t.Fatalf("want actionable %q at field position, got %v", want, err)
		}
	}
	check("")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	check("file unreadable")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	check("file unreadable")
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check("file not found")
}

func TestMihomoCertificateAccessRetainsSafePathPolicy(t *testing.T) {
	executor := &Executor{}
	content := "listeners:\n- type: trojan\n  certificate: /root/cert.pem\n  private-key: /root/key.pem\n"
	err := executor.verifyCoreCertificateAccess(context.Background(), core.EngineMihomo, DefaultSpecs()[core.EngineMihomo], content, commandIdentity{})
	if err == nil || !strings.Contains(err.Error(), "SAFE_PATHS") || !strings.Contains(err.Error(), "listeners[0].certificate") {
		t.Fatalf("path policy was bypassed: %v", err)
	}
}
