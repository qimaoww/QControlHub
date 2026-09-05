//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
	testSSRustBootstrapPreservesUnit(t, script)
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

func testSSRustBootstrapPreservesUnit(t *testing.T, script string) {
	t.Helper()
	unitDir := "/etc/systemd/system"
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(unitDir, "qagent-shadowsocks-rust.service")
	content, err := os.ReadFile(filepath.Join(filepath.Dir(script), "systemd", "qagent-shadowsocks-rust.service"))
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.ReplaceAll(string(content), " --acl "+shadowsocksRustACLPath, "")
	legacy = strings.ReplaceAll(legacy, "ConditionPathExists="+shadowsocksRustACLPath+"\n", "")
	if err := os.WriteFile(unit, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	const configPath = "/etc/qagent/shadowsocks-rust/config.json"
	const original = `{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"original-managed"}`
	if err := os.WriteFile(configPath, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	fakeBin := t.TempDir()
	fake := filepath.Join(fakeBin, "systemctl")
	contents := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
  daemon-reload) exit 0 ;;
  is-active) exit 0 ;;
  show)
    case "$3" in
      --property=LoadState) echo loaded ;;
      --property=ActiveState) echo active ;;
      --property=FragmentPath) echo %s ;;
      --property=ExecStart) echo '{ path=/usr/local/lib/qagent/cores/ssserver ; argv[]=/usr/local/lib/qagent/cores/ssserver -c /etc/qagent/shadowsocks-rust/config.json ; ignore_errors=no ; }' ;;
      --property=Description) echo 'Shadowsocks Rust core managed by QAgent' ;;
      --property=User|--property=Group) echo qcontrolhub-core ;;
      --property=Type) echo simple ;;
      --property=WorkingDirectory) echo /var/lib/qcontrolhub-shadowsocks-rust ;;
      --property=Environment) echo RUST_LOG=info ;;
      --property=*) echo '' ;;
      *) exit 99 ;;
    esac ;;
  *) echo "unexpected service mutation: $*" >&2; exit 99 ;;
esac
`, unit)
	if err := os.WriteFile(fake, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(script, "shadowsocks-rust")
	command.Env = append(os.Environ(), "QCH_SERVICE_MANAGER=systemd", "QCH_SKIP_CORE_SERVICES=shadowsocks-rust", "QCH_PRESERVE_CORE_UNIT=1", "PATH="+fakeBin+":/usr/sbin:/usr/bin:/sbin:/bin")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("preserved-unit bootstrap: %v\n%s", err, output)
	}
	after, err := os.ReadFile(unit)
	if err != nil || string(after) != legacy {
		t.Fatal("bootstrap changed unit before migration transaction")
	}
	after, err = os.ReadFile(configPath)
	if err != nil || string(after) != original {
		t.Fatal("bootstrap changed existing configuration")
	}
	if !strings.Contains(string(output), "preserved existing managed shadowsocks-rust unit and enablement") {
		t.Fatal("bootstrap did not report preserved service")
	}
	if _, err := os.Stat(shadowsocksRustACLPath); err != nil {
		t.Fatal("bootstrap did not repair missing ACL prerequisite")
	}
}
