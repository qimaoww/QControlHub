//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSSRustScriptExactServiceMapping(t *testing.T) {
	spec := EngineSpec{Binary: "/usr/local/bin/ssserver", ConfigPath: "/etc/shadowsocks-rust/config.json", Service: "shadowsocks-rust.service"}
	base := spec.Binary + " -c " + spec.ConfigPath
	if !supportedExistingService(core.EngineShadowsocksRust, spec.Service) || !supportedExistingExecStart(core.EngineShadowsocksRust, spec, base) {
		t.Fatal("script service mapping rejected")
	}
	spec.ACLPath = "/etc/shadowsocks-rust/block_cn.acl"
	if !supportedExistingExecStart(core.EngineShadowsocksRust, spec, base+" --acl "+spec.ACLPath) {
		t.Fatal("script ACL invocation rejected")
	}
	for _, argv := range []string{base, base + " --acl /tmp/custom.acl", base + " --acl " + spec.ACLPath + " --daemonize", base + " -c " + spec.ConfigPath} {
		if supportedExistingExecStart(core.EngineShadowsocksRust, spec, argv) {
			t.Fatalf("unsafe/drifted invocation accepted: %s", argv)
		}
	}
	withoutACL := spec
	withoutACL.ACLPath = ""
	if coreMigrationSourceDigest(spec) == coreMigrationSourceDigest(withoutACL) {
		t.Fatal("ACL argument is absent from source identity")
	}
}

func TestSSRustScriptACLPlanIsReadOnlyAndTracksDrift(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("protected import requires root-owned fixtures")
	}
	root := t.TempDir()
	previousRoot := ssRustImportStateRoot
	ssRustImportStateRoot = filepath.Join(root, "managed-state")
	t.Cleanup(func() { ssRustImportStateRoot = previousRoot })
	if err := os.Mkdir(ssRustImportStateRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	aclPath := filepath.Join(root, "block_cn.acl")
	if err := os.WriteFile(aclPath, []byte("[outbound_block_list]\n192.0.2.0/24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := EngineSpec{ConfigPath: filepath.Join(root, "config.json"), ACLPath: aclPath}
	content := `{"servers":[{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"test-password","dns":"1.1.1.1"}],"ipv6_first":true}`
	plan, err := prepareSSRustImport(spec, content)
	if err != nil || !plan.active() {
		t.Fatalf("plan ACL import: %v", err)
	}
	if _, err := os.Stat(plan.resourceRoot); !os.IsNotExist(err) {
		t.Fatalf("read-only plan staged resources: %v", err)
	}
	var doc map[string]any
	_ = json.Unmarshal([]byte(plan.managedContent), &doc)
	entry := doc["servers"].([]any)[0].(map[string]any)
	if entry["acl"] != plan.resources[0].destination || doc["acl"] != entry["acl"] || entry["dns"] != "1.1.1.1" || doc["ipv6_first"] != true {
		t.Fatalf("ACL did not retain per-port precedence and network settings: %s", plan.managedContent)
	}
	identity := commandIdentity{uid: 12345, gid: 12345, groups: []uint32{12345}}
	if err := plan.stage(identity); err != nil {
		t.Fatalf("stage protected ACL: %v", err)
	}
	info, err := os.Stat(plan.resources[0].destination)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("staged ACL metadata: %v", err)
	}
	if err := plan.cleanup(); err != nil {
		t.Fatalf("rollback resource cleanup: %v", err)
	}
	if _, err := os.Stat(aclPath); err != nil {
		t.Fatalf("source ACL was removed: %v", err)
	}
	if err := os.WriteFile(aclPath, []byte("[outbound_block_list]\n198.51.100.0/24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := prepareSSRustImport(spec, content)
	if err != nil || changed.managedContent == plan.managedContent {
		t.Fatalf("ACL drift not reflected in snapshot: %v", err)
	}
	for _, invalid := range []string{`{"servers":[{"acl":"/tmp/other.acl"}]}`, `{"servers":[{"acl":false}]}`, `{"servers":[{"plugin":"custom"}]}`, `{"servers":[null]}`, `{} {}`} {
		if _, err := prepareSSRustImport(spec, invalid); err == nil {
			t.Fatalf("unsupported import accepted: %s", invalid)
		}
	}
	if err := os.Rename(aclPath, aclPath+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(aclPath+".original", aclPath); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSSRustImport(spec, content); err == nil {
		t.Fatal("symlinked ACL accepted")
	}
}

func TestSSRustScriptMigrationAndRetry(t *testing.T) {
	requireAgentRoot(t)
	for _, failStart := range []bool{false, true} {
		t.Run(fmt.Sprint("fail-start-", failStart), func(t *testing.T) {
			testSSRustScriptMigration(t, failStart)
		})
	}
}

func testSSRustScriptMigration(t *testing.T, failStart bool) {
	t.Helper()
	fixture := newExistingCoreMigrationFixture(t, failStart)
	fixture.existing.Service = "shadowsocks-rust.service"
	fixture.managed.Service = "qagent-shadowsocks-rust.service"
	content := `{"servers":[{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"test-password","dns":"1.1.1.1","outbound_bind_addr":"192.0.2.1"}],"ipv6_first":true,"mode":"tcp_and_udp","timeout":300}`
	if err := os.WriteFile(fixture.existing.ConfigPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	writeMigrationServiceState(t, fixture.stateDirectory, fixture.existing.Service, "active", "enabled")
	writeMigrationServiceState(t, fixture.stateDirectory, fixture.managed.Service, "inactive", "disabled")
	writeMigrationExecStart(t, fixture.stateDirectory, fixture.existing.Service, systemdExecStart(fixture.existing.Binary, fixture.existing.Binary+" -c "+fixture.existing.ConfigPath))
	fixture.executor.Specs = map[core.Engine]EngineSpec{core.EngineShadowsocksRust: fixture.managed}
	fixture.executor.ExistingSpecs = map[core.Engine]EngineSpec{core.EngineShadowsocksRust: fixture.existing}
	task := core.Task{Engine: core.EngineShadowsocksRust, Action: core.ActionImportExisting, ConfigContent: content}
	output, err := fixture.executor.Execute(context.Background(), task)
	if failStart {
		if err == nil {
			t.Fatal("failed service start accepted")
		}
		fixture.assertServiceState(t, fixture.existing.Service, "active", "enabled")
		fixture.assertServiceState(t, fixture.managed.Service, "inactive", "disabled")
		restored, readErr := os.ReadFile(fixture.managed.ConfigPath)
		if readErr != nil || string(restored) != fixture.originalManagedConfig {
			t.Fatalf("rollback did not restore configuration: %v\n%s", readErr, restored)
		}
		return
	}
	if err != nil {
		t.Fatalf("import: %v\n%s", err, output)
	}
	fixture.assertServiceState(t, fixture.existing.Service, "inactive", "disabled")
	fixture.assertServiceState(t, fixture.managed.Service, "active", "enabled")
	deployed, err := os.ReadFile(fixture.managed.ConfigPath)
	if err != nil || string(deployed) != content {
		t.Fatalf("script settings changed: %v\n%s", err, deployed)
	}
	if !strings.Contains(output, "firewall") {
		t.Fatal("missing script firewall ownership warning")
	}
	if _, err := fixture.executor.Execute(context.Background(), task); err != nil {
		t.Fatalf("idempotent import retry: %v", err)
	}
}

func TestSSRustScriptDiscoveryPersistsACLMapping(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreDiscoveryFixture(t)
	engine := core.EngineShadowsocksRust
	service := "shadowsocks-rust.service"
	managed := DefaultSpecs()[engine]
	fixture.managedSpecs = map[core.Engine]EngineSpec{engine: managed}
	existingDiscoveryCandidates[engine] = existingDiscoveryCandidateSet{
		services: []string{service}, executables: []string{fixture.realBinary}, configs: []string{fixture.configPath},
	}
	acl := filepath.Join(filepath.Dir(fixture.configPath), "block_cn.acl")
	for path, content := range map[string]string{
		fixture.configPath: `{"servers":[{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"test-password"}]}`,
		acl:                "[outbound_block_list]\n192.0.2.0/24\n",
		filepath.Join(fixture.stateDirectory, service+".active"):             "active\n",
		filepath.Join(fixture.stateDirectory, managed.Service+".active"):     "inactive\n",
		filepath.Join(fixture.stateDirectory, managed.Service+".load-state"): "not-found\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture.writeExecStart(t, service, systemdExecStart(fixture.realBinary, fixture.realBinary+" -c "+fixture.configPath+" --acl "+acl))
	specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil || len(issues) != 0 || specs[engine].ACLPath != acl {
		t.Fatalf("SS Rust discovery: specs=%+v issues=%+v err=%v", specs, issues, err)
	}
	state, err := loadExistingCoreDiscoveryState(fixture.discoveryStatePath)
	if err != nil || state.Specs[engine].ACLPath != acl {
		t.Fatalf("persisted ACL mapping: %+v, %v", state, err)
	}
	fixture.writeExecStart(t, service, systemdExecStart(fixture.realBinary, fixture.realBinary+" -c "+fixture.configPath+" --daemonize"))
	specs, issues, err = RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil || len(specs) != 0 || issues[engine] == "" {
		t.Fatalf("unsafe invocation not disabled: %+v, %+v, %v", specs, issues, err)
	}
}
