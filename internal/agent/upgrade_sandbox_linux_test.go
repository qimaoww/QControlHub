//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// This integration test is explicitly run in an isolated read-only Docker
// rootfs with only the old Agent's /etc/qagent and /usr/local/lib/qagent write
// allowances. It must never operate on the workstation's real service paths.
func TestAgentLifecycleReadOnlySandbox(t *testing.T) {
	if os.Getenv("QCH_TEST_UPGRADE_SANDBOX") != "1" {
		t.Skip("requires isolated read-only container")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("sandbox test requires Docker")
	}
	probe := "/var/lib/qch-upgrade-readonly-probe"
	if err := os.Mkdir(probe, 0o700); err == nil {
		os.Remove(probe)
		t.Fatal("container root filesystem must be read-only")
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		spec := DefaultSpecs()[engine]
		if err := os.MkdirAll(filepath.Dir(spec.ConfigPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(filepath.Dir(spec.ConfigPath), 0, int(identity.gid)); err != nil {
			t.Fatal(err)
		}
	}
	script, err := stageBundledCoreInstallAssets(coreInstallAssetRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(script, coreInstallAssetRoot+"/") {
		t.Fatal("bundle escaped writable namespace")
	}
	root := t.TempDir()
	acl := filepath.Join(root, "block_cn.acl")
	if err := os.WriteFile(acl, []byte("[outbound_block_list]\n192.0.2.0/24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := prepareSSRustImport(EngineSpec{ConfigPath: filepath.Join(root, "config.json"), ACLPath: acl}, `{"servers":[{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"test-password"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.stage(identity); err != nil {
		t.Fatalf("SS Rust ACL cannot stage under old Agent sandbox: %v", err)
	}
	if err := plan.cleanup(); err != nil {
		t.Fatal(err)
	}
	cert := filepath.Join(root, "cert.pem")
	if err := os.WriteFile(cert, []byte("test certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	resourceDir := filepath.Join(ouSBManagedStateRoot, "ou-sb", strings.Repeat("a", 64))
	certPlan := coreImportPlan{stateRoot: ouSBManagedStateRoot, resourceRoot: resourceDir, resources: []coreImportResource{{source: cert, destination: filepath.Join(resourceDir, "cert.pem")}}}
	if err := certPlan.stage(identity); err != nil {
		t.Fatalf("OU-SB certificates cannot stage: %v", err)
	}
	if err := certPlan.cleanup(); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(filepath.Dir(coreInstallAssetRoot), "qagent-test")
	candidate := current + ".candidate"
	if err := os.WriteFile(current, existingDiscoveryCoreHelper, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, upgradeCandidate(t), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := prepareAgentUpgrade(context.Background(), current, candidate, "upgrade-test", ServiceManagerSystemd)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.rollback(); err != nil {
		t.Fatal(err)
	}
}
