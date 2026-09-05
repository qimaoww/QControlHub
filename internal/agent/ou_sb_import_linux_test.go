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

func TestRewriteOUSBSingBoxResourcesCopiesOnlySupportedCertificates(t *testing.T) {
	sources := map[string]string{
		"/etc/ou-sb/certs/fullchain.pem": "/var/lib/qcontrolhub-sing-box/ou-sb/digest/fullchain.pem",
		"/etc/ou-sb/certs/privkey.pem":   "/var/lib/qcontrolhub-sing-box/ou-sb/digest/privkey.pem",
	}
	content := `{"inbounds":[{"tls":{"certificate_path":"/etc/ou-sb/certs/fullchain.pem","key_path":"/etc/ou-sb/certs/privkey.pem"}}]}`
	rewritten, used, err := rewriteOUSBSingBoxResources(content, sources)
	if err != nil {
		t.Fatal(err)
	}
	for source, destination := range sources {
		if !used[source] || !strings.Contains(rewritten, destination) || strings.Contains(rewritten, source) {
			t.Fatalf("OU-SB resource was not rewritten exactly: %s -> %s\n%s", source, destination, rewritten)
		}
	}
	if _, _, err := rewriteOUSBSingBoxResources(`{"path":"/etc/ou-sb/custom.pem"}`, sources); err == nil {
		t.Fatal("expected an unsupported OU-SB resource to fail closed")
	}
}

func TestOUSBImportPlanStagesProtectedServiceReadableResources(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("resource ownership staging requires the root agent identity")
	}
	root := t.TempDir()
	certificateRoot := filepath.Join(root, "etc", "ou-sb", "certs")
	stateRoot := filepath.Join(root, "state")
	for _, directory := range []string{certificateRoot, stateRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fullchain := filepath.Join(certificateRoot, "fullchain.pem")
	privateKey := filepath.Join(certificateRoot, "privkey.pem")
	if err := os.WriteFile(fullchain, []byte("certificate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKey, []byte("private-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	previousStateRoot := ouSBManagedStateRoot
	ouSBManagedStateRoot = stateRoot
	t.Cleanup(func() { ouSBManagedStateRoot = previousStateRoot })
	resourceRoot := filepath.Join(stateRoot, "ou-sb", strings.Repeat("a", 64))
	plan := coreImportPlan{
		managedContent: "{}\n",
		resourceRoot:   resourceRoot,
		resources: []coreImportResource{
			{source: fullchain, destination: filepath.Join(resourceRoot, "fullchain.pem")},
			{source: privateKey, destination: filepath.Join(resourceRoot, "privkey.pem")},
		},
	}
	identity := commandIdentity{uid: 12345, gid: 12345, groups: []uint32{12345}}
	if err := plan.stage(identity); err != nil {
		t.Fatal(err)
	}
	for _, resource := range plan.resources {
		contents, err := os.ReadFile(resource.destination)
		if err != nil || len(contents) == 0 {
			t.Fatalf("read staged OU-SB resource %s: %v", resource.destination, err)
		}
		info, err := os.Lstat(resource.destination)
		if err != nil {
			t.Fatal(err)
		}
		uid, gid, known := fileOwnership(info)
		if info.Mode().Perm() != 0o640 || !known || uid != 0 || gid != int(identity.gid) {
			t.Fatalf("unexpected staged metadata: mode=%#o uid=%d gid=%d", info.Mode().Perm(), uid, gid)
		}
	}
	if err := plan.cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(resourceRoot); !os.IsNotExist(err) {
		t.Fatalf("OU-SB resource directory survived rollback cleanup: %v", err)
	}
}

func TestCoreMigrationMarkerKeepsOriginalOU_SBRequestDigest(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "core-migration")
	managedDigest := coreMigrationConfigDigest("managed")
	requestDigest := coreMigrationConfigDigest("original")
	sourceDigest := coreMigrationConfigDigest("source")
	record := coreMigrationRecord{
		State: coreMigrationInProgress, ConfigDigest: managedDigest, RequestConfigDigest: requestDigest,
		SourceDigest: sourceDigest, ExistingEnableState: "enabled", ManagedEnableState: "disabled",
		ManagedInitialState: "inactive", HasFileRollback: true, HasAssetRollback: true,
		BinaryBackupDigest: coreMigrationMissingBackup, ConfigBackupDigest: coreMigrationMissingBackup,
		StagedBinaryDigest: managedDigest,
		AssetBackupDigests: [2]string{coreMigrationMissingBackup, coreMigrationMissingBackup},
		StagedAssetDigests: [2]string{coreMigrationMissingBackup, coreMigrationMissingBackup},
		AuxiliaryService:   "ou-sb-firewall.service", AuxiliaryEnableState: "enabled", AuxiliaryInitialState: "active",
	}
	if err := writePreparedCoreMigrationMarker(prefix, core.EngineSingBox, record); err != nil {
		t.Fatal(err)
	}
	loaded, err := readCoreMigrationRecord(prefix, core.EngineSingBox)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RequestConfigDigest != requestDigest || loaded.AuxiliaryService != record.AuxiliaryService {
		t.Fatalf("prepared marker lost OU-SB state: %#v", loaded)
	}
	if err := writeCoreMigrationMarkerWithRequestDigest(prefix, core.EngineSingBox, coreMigrationComplete,
		managedDigest, requestDigest, sourceDigest, "enabled", "disabled"); err != nil {
		t.Fatal(err)
	}
	matched, err := completedCoreMigrationMatches(prefix, core.EngineSingBox, "original")
	if err != nil || !matched {
		t.Fatalf("completed import did not match the original OU-SB request: matched=%v err=%v", matched, err)
	}
}

func TestOUSBSingBoxImportRewritesCertificatesAndKeepsRetryIdempotent(t *testing.T) {
	requireAgentRoot(t)
	if _, err := managedCoreServiceIdentity(); err != nil {
		t.Skipf("managed core service identity is unavailable: %v", err)
	}
	fixture := newExistingCoreMigrationFixture(t, false)
	certificateRoot := filepath.Join(filepath.Dir(fixture.existing.ConfigPath), "ou-sb", "certs")
	stateRoot := filepath.Join(filepath.Dir(fixture.existing.ConfigPath), "sing-box-state")
	if err := os.MkdirAll(certificateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"fullchain.pem": "certificate\n", "privkey.pem": "private-key\n"} {
		if err := os.WriteFile(filepath.Join(certificateRoot, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	content := `{"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":1080,"tls":{"enabled":true,"certificate_path":"` + filepath.Join(certificateRoot, "fullchain.pem") + `","key_path":"` + filepath.Join(certificateRoot, "privkey.pem") + `"}}],"outbounds":[{"type":"direct"}]}`
	if err := os.WriteFile(fixture.existing.ConfigPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	writeMigrationServiceState(t, fixture.stateDirectory, "sing-box.service", "active", "enabled")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-sing-box.service", "inactive", "disabled")
	fixture.existing.Service = "sing-box.service"
	fixture.managed.Service = "qagent-sing-box.service"
	writeMigrationExecStart(t, fixture.stateDirectory, fixture.existing.Service,
		systemdExecStart(fixture.existing.Binary, fixture.existing.Binary+" run -c "+fixture.existing.ConfigPath))
	fixture.executor.Specs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.managed}
	fixture.executor.ExistingSpecs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.existing}

	previousConfigPath, previousCertificateRoot, previousStateRoot := ouSBConfigPath, ouSBCertificateRoot, ouSBManagedStateRoot
	ouSBConfigPath, ouSBCertificateRoot, ouSBManagedStateRoot = fixture.existing.ConfigPath, certificateRoot, stateRoot
	t.Cleanup(func() {
		ouSBConfigPath, ouSBCertificateRoot, ouSBManagedStateRoot = previousConfigPath, previousCertificateRoot, previousStateRoot
	})

	output, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineSingBox, ConfigContent: content,
	})
	if err != nil {
		t.Fatalf("import OU-SB sing-box configuration: %v\n%s", err, output)
	}
	managedContent, err := os.ReadFile(fixture.managed.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(managedContent), certificateRoot) || !strings.Contains(string(managedContent), stateRoot) {
		t.Fatalf("managed OU-SB configuration was not rewritten:\n%s", managedContent)
	}
	output, err = fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineSingBox, ConfigContent: content,
	})
	if err != nil || !strings.Contains(output, "already completed") {
		t.Fatalf("idempotent OU-SB import retry = %q, %v", output, err)
	}
}
