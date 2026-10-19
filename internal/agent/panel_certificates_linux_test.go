//go:build linux

package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Installed paths are exercised only in the disposable certificate sandbox.
func TestCertificatePairAtomicSwitchIsolationAndRollback(t *testing.T) {
	if os.Getenv("QCH_TEST_CERTIFICATE_FILES") != "1" {
		t.Skip("requires disposable installed-path certificate sandbox")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("refusing installed paths outside Docker")
	}
	if err := os.MkdirAll(managedCoreConfigurationRoot, 0750); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(managedCoreConfigurationRoot, "certificate-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	makeTask := func(version int) core.Task {
		pair, err := certificates.SelfSigned([]string{"edge.example.test"}, "ecdsa256", 90)
		if err != nil {
			t.Fatal(err)
		}
		return core.Task{Action: core.ActionDeploy, Engine: core.EngineMihomo, ConfigContent: "tls:\n  certificate: " + core.PanelCertificatePrefix + "crt_0000000000000001/cert.pem\n  private-key: " + core.PanelCertificatePrefix + "crt_0000000000000001/key.pem\n", Certificates: []core.DeploymentCertificate{{ID: "crt_0000000000000001", Version: version, Certificate: pair.Certificate, PrivateKey: pair.PrivateKey}}}
	}
	spec := EngineSpec{ConfigPath: filepath.Join(root, "alice", "config.yaml")}
	if err = os.MkdirAll(filepath.Dir(spec.ConfigPath), 0750); err != nil {
		t.Fatal(err)
	}
	v1 := makeTask(1)
	if err = preparePanelCertificates(spec, &v1); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v1.ConfigContent, core.PanelCertificatePrefix) {
		t.Fatal("panel marker escaped into remote config")
	}
	certRoot := filepath.Join(filepath.Dir(spec.ConfigPath), "panel-certificates", "crt_0000000000000001")
	for _, name := range []string{"cert.pem", "key.pem"} {
		path := filepath.Join(certRoot, "v1", name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		m := metadataFromFileInfo(info)
		if m.mode != 0640 || m.uid != 0 || m.gid != int(identity.gid) {
			t.Fatalf("unsafe pair metadata: %+v", m)
		}
		cmd := exec.Command("/usr/bin/test", "-r", path)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: identity.uid, Gid: identity.gid}}
		if err = cmd.Run(); err != nil {
			t.Fatalf("non-root kernel cannot read %s: %v", name, err)
		}
	}
	if _, err = atomicDeploy(spec.ConfigPath, v1.ConfigContent); err != nil {
		t.Fatal(err)
	}
	v2 := makeTask(2)
	if err = preparePanelCertificates(spec, &v2); err != nil {
		t.Fatal(err)
	}
	backup, err := atomicDeploy(spec.ConfigPath, v2.ConfigContent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rollbackDeploy(spec.ConfigPath, backup); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(spec.ConfigPath)
	if string(got) != v1.ConfigContent {
		t.Fatal("rollback did not restore both previous pair paths")
	}
	conflict := makeTask(1)
	if err = preparePanelCertificates(spec, &conflict); err == nil {
		t.Fatal("overwrote immutable certificate version")
	}
	other := EngineSpec{ConfigPath: filepath.Join(root, "bob", "config.yaml")}
	_ = os.MkdirAll(filepath.Dir(other.ConfigPath), 0750)
	task := makeTask(1)
	if err = preparePanelCertificates(other, &task); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(task.ConfigContent, "/alice/") {
		t.Fatal("instance path leakage")
	}
	bad := makeTask(3)
	bad.Certificates[0].PrivateKey = v1.Certificates[0].PrivateKey
	if err = preparePanelCertificates(spec, &bad); err == nil {
		t.Fatal("accepted mismatched pair")
	}
	if _, err = os.Stat(filepath.Join(certRoot, "v3")); !os.IsNotExist(err) {
		t.Fatal("published partial pair")
	}
	_ = os.Symlink(filepath.Join(certRoot, "v1"), filepath.Join(certRoot, "v4"))
	bad = makeTask(4)
	if err = preparePanelCertificates(spec, &bad); err == nil {
		t.Fatal("accepted version symlink")
	}
	t.Log("Real protected paths: root:service 0750/0640, non-root read, independent instance copies, immutable versions, paired config rollback, mismatch/symlink rejection")
}
