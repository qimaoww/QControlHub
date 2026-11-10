//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestExternalImportRejectsManagedPathsBeforeOpening(t *testing.T) {
	for _, path := range []string{"/etc/qagent/not-an-external-source.json", "/etc/qcontrolhub/tls/not-an-external-key.pem", "/etc/qcontrolhub/panel-certificates/not-an-external-certificate.pem"} {
		if _, _, err := readExternalImportSource(path, 4096); err == nil || !strings.Contains(err.Error(), "requires its existing ownership mapping") {
			t.Fatalf("external reader did not reject protected ownership before opening %s: %v", path, err)
		}
	}
}

func TestImportDependencyIdentityAndPathMapping(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	first := filepath.Join(root, "规则 甲")
	second := filepath.Join(root, "规则 乙")
	for _, directory := range []string{first, second} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{filepath.Join(first, "same.srs"): "first", filepath.Join(second, "same.srs"): "second"} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "current.srs")
	if err := os.Symlink(filepath.Join(first, "same.srs"), link); err != nil {
		t.Fatal(err)
	}
	content := `{"unknown":{"n":9007199254740993},"route":{"rule_set":[{"type":"local","tag":"a","path":"规则 甲/same.srs"},{"type":"local","tag":"b","path":"规则 乙/same.srs"},{"type":"local","tag":"c","path":"current.srs"}]},"outbounds":[{"type":"direct","tag":"first"},{"type":"direct","tag":"second"}],"transport":{"path":"/not/a/file"},"url":"https://example.test/not-a-file"}`
	existing := EngineSpec{ConfigPath: filepath.Join(root, "config.json"), WorkingDirectory: root}
	managed := EngineSpec{ConfigPath: filepath.Join(root, "managed.json")}
	plan, err := prepareJSONDependencyImport(core.EngineSingBox, existing, managed, content)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.resources) != 2 || plan.resources[0].destination == plan.resources[1].destination {
		t.Fatalf("resource deduplication/collision: %+v", plan.resources)
	}
	if !strings.Contains(plan.managedContent, "9007199254740993") || !strings.Contains(plan.managedContent, "/not/a/file") {
		t.Fatal("changed unrelated configuration")
	}
	var document map[string]any
	decoder := json.NewDecoder(strings.NewReader(plan.managedContent))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	entries := document["route"].(map[string]any)["rule_set"].([]any)
	if entries[0].(map[string]any)["path"] != entries[2].(map[string]any)["path"] {
		t.Fatal("same resource mapped differently")
	}
	if err := plan.verifySources(); err != nil {
		t.Fatal(err)
	}
	fingerprint := importPlanFingerprint(plan)
	again, err := prepareJSONDependencyImport(core.EngineSingBox, existing, managed, content)
	if err != nil || importPlanFingerprint(again) != fingerprint {
		t.Fatalf("ordinary reads changed identity: %v", err)
	}
	source := filepath.Join(first, "same.srs")
	replacement := source + ".new"
	if err := os.WriteFile(replacement, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, source); err != nil {
		t.Fatal(err)
	}
	if err := plan.verifySources(); err == nil {
		t.Fatal("equal-content inode replacement did not retire snapshot")
	}
}

func TestImportCertificatePairAndSymlinkRotation(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	pair, err := certificates.SelfSigned([]string{"import.example.test"}, "ecdsa256", 90)
	if err != nil {
		t.Fatal(err)
	}
	other, err := certificates.SelfSigned([]string{"import.example.test"}, "ecdsa256", 90)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"chain1.pem": pair.Certificate, "key1.pem": pair.PrivateKey, "key2.pem": other.PrivateKey} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"chain.pem": "chain1.pem", "key.pem": "key1.pem"} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	content := `{"inbounds":[{"type":"trojan","tls":{"certificate_path":"chain.pem","key_path":"key.pem"}}]}`
	spec := EngineSpec{ConfigPath: filepath.Join(root, "source.json"), WorkingDirectory: root}
	managed := EngineSpec{ConfigPath: filepath.Join(root, "managed.json")}
	plan, err := prepareJSONDependencyImport(core.EngineSingBox, spec, managed, content)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.resources) != 2 {
		t.Fatal("certificate pair missing")
	}
	if err := os.Remove(filepath.Join(root, "key.pem")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("key2.pem", filepath.Join(root, "key.pem")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareJSONDependencyImport(core.EngineSingBox, spec, managed, content); err == nil || !strings.Contains(err.Error(), "usable pair") {
		t.Fatalf("mismatched rotated pair: %v", err)
	}
}

func TestImportSourceSymlinksAndBoundedResources(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	file := filepath.Join(root, "data")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, b); err != nil {
		t.Fatal(err)
	}
	data, resolved, err := readImportSource(a, 4)
	if err != nil || string(data) != "data" || resolved != file {
		t.Fatalf("legal link chain: %s %s %v", data, resolved, err)
	}
	if _, _, err := readImportSource(a, 3); err == nil {
		t.Fatal("truncated resource accepted")
	}
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readImportSource(a, 4); err == nil {
		t.Fatal("link cycle accepted")
	}
}

func TestServiceWordsAndEffectiveDropInPreserveArguments(t *testing.T) {
	unit := "[Service]\nExecStart=/old/core run -c /old/config\n[Service]\nExecStart=\nExecStart=\"/opt/内核/core binary\" run --config='配置 文件.json' -D /srv/runtime\\sdir\n"
	argv, err := effectiveUnitCommand(unit)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/内核/core binary", "run", "--config=配置 文件.json", "-D", "/srv/runtime dir"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv=%q want=%q", argv, want)
	}
	spec, err := parseCoreServiceCommand(core.EngineSingBox, argv[0], argv, "/srv/service")
	if err != nil {
		t.Fatal(err)
	}
	if spec.ConfigPath != "/srv/runtime dir/配置 文件.json" || spec.WorkingDirectory != "/srv/runtime dir" {
		t.Fatalf("wrong path context: %+v", spec)
	}
	for _, text := range []string{`/core "unterminated`, `/core \`, `/core ` + "\x00"} {
		if _, err := splitServiceWords(text); err == nil {
			t.Fatalf("malformed command accepted: %q", text)
		}
	}
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	if _, err := parseServiceAssignments("PATH=$(touch "+marker+")", nil); err == nil {
		t.Fatal("shell substitution accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("discovery executed shell text")
	}
}

func TestImportedCertificateRenewalKeepsSourceAndRestoresInterruptedPair(t *testing.T) {
	requireAgentRoot(t)
	if _, err := managedCoreServiceIdentity(); err != nil {
		t.Skipf("managed identity unavailable: %v", err)
	}
	fixture := newExistingCoreMigrationFixture(t, false)
	root := filepath.Dir(fixture.existing.ConfigPath)
	first, err := certificates.SelfSigned([]string{"renew.example.test"}, "ecdsa256", 90)
	if err != nil {
		t.Fatal(err)
	}
	second, err := certificates.SelfSigned([]string{"renew.example.test"}, "ecdsa256", 90)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"chain1.pem": first.Certificate, "key1.pem": first.PrivateKey, "chain2.pem": second.Certificate, "key2.pem": second.PrivateKey} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"chain.pem": "chain1.pem", "key.pem": "key1.pem"} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	fixture.existing.Service = "sing-box.service"
	fixture.managed.Service = "qagent-sing-box.service"
	writeMigrationServiceState(t, fixture.stateDirectory, fixture.existing.Service, "active", "enabled")
	writeMigrationServiceState(t, fixture.stateDirectory, fixture.managed.Service, "inactive", "disabled")
	writeMigrationExecStart(t, fixture.stateDirectory, fixture.existing.Service, systemdExecStart(fixture.existing.Binary, fixture.existing.Binary+" run -c "+fixture.existing.ConfigPath))
	// A valid unchanged input larger than the refresh before-image limit must
	// not prevent a small certificate renewal in the same import transaction.
	rules := `{"version":3,"rules":[]}` + strings.Repeat(" ", 5<<20)
	if err := os.WriteFile(filepath.Join(root, "rules.json"), []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	source := `{"inbounds":[{"type":"trojan","tls":{"certificate_path":"chain.pem","key_path":"key.pem"}}],"outbounds":[{"type":"direct"}],"route":{"rule_set":[{"type":"local","tag":"fixture","format":"source","path":"rules.json"}]}}`
	if err := os.WriteFile(fixture.existing.ConfigPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.executor.Specs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.managed}
	fixture.executor.ExistingSpecs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.existing}
	previousConfig, previousCertificates := ouSBConfigPath, ouSBCertificateRoot
	ouSBConfigPath, ouSBCertificateRoot = fixture.existing.ConfigPath, root
	t.Cleanup(func() { ouSBConfigPath, ouSBCertificateRoot = previousConfig, previousCertificates })
	helper := "ou-sb-firewall.service"
	writeMigrationServiceState(t, fixture.stateDirectory, helper, "active", "enabled")
	plan, err := fixture.executor.readExistingConfigPlan(context.Background(), core.EngineSingBox, fixture.managed, fixture.existing)
	if err != nil {
		t.Fatal(err)
	}
	if plan.auxiliary.name != helper {
		t.Fatal("generic certificate import omitted the existing OU-SB helper relationship")
	}
	writeMigrationServiceState(t, fixture.stateDirectory, helper, "inactive", "enabled")
	request := core.Task{Action: core.ActionImportExisting, Engine: core.EngineSingBox, ConfigContent: plan.managedContent, ImportSourceDigest: plan.snapshotDigest}
	if _, err := fixture.executor.Execute(context.Background(), request); err == nil {
		t.Fatal("changed helper state reused the earlier import snapshot")
	}
	writeMigrationServiceState(t, fixture.stateDirectory, helper, "active", "enabled")
	if _, err := fixture.executor.Execute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	for suffix, expected := range map[string]string{".active": "inactive", ".persistent": "0", ".runtime": "0"} {
		value, err := os.ReadFile(filepath.Join(fixture.stateDirectory, helper+suffix))
		if err != nil || strings.TrimSpace(string(value)) != expected {
			t.Fatalf("helper %s did not migrate: %s %v", suffix, value, err)
		}
	}
	state, err := readImportResourceState(fixture.markerPrefix, core.EngineSingBox, fixture.managed)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Dependencies) != 3 {
		t.Fatal("renewal source mapping was not retained")
	}
	// An edited configuration can retire a resource whose original and managed
	// files have already disappeared. It must not block another resource update.
	retired := filepath.Join(state.Root, "retired-rule.json")
	state.Resources = append(state.Resources, durableImportResource{Path: retired, Digest: "retired", Limit: 4096})
	state.Dependencies = append(state.Dependencies, core.ImportDependency{Field: "retired", Source: filepath.Join(root, "removed-source.json"), Destination: retired})
	if err := writeImportResourceState(fixture.markerPrefix, core.EngineSingBox, state); err != nil {
		t.Fatal(err)
	}
	rotate := func(name, target string) {
		t.Helper()
		temporary := filepath.Join(root, name+".next")
		if err := os.Symlink(target, temporary); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temporary, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	rotate("chain.pem", "chain2.pem")
	before, _ := os.ReadFile(filepath.Join(fixture.stateDirectory, "commands.log"))
	if err := fixture.executor.RefreshImportedResources(context.Background()); err == nil {
		t.Fatal("mismatched renewal pair accepted")
	}
	after, _ := os.ReadFile(filepath.Join(fixture.stateDirectory, "commands.log"))
	if strings.Count(string(before), "stop ") != strings.Count(string(after), "stop ") {
		t.Fatal("invalid renewal stopped service")
	}
	rotate("key.pem", "key2.pem")
	if err := fixture.executor.RefreshImportedResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, err := readImportResourceState(fixture.markerPrefix, core.EngineSingBox, fixture.managed)
	if err != nil {
		t.Fatal(err)
	}
	for _, dependency := range updated.Dependencies {
		if dependency.Field == "retired" {
			continue
		}
		content, err := os.ReadFile(dependency.Destination)
		if err != nil {
			t.Fatal(err)
		}
		expected := second.Certificate
		if strings.HasSuffix(dependency.Field, "key_path") {
			expected = second.PrivateKey
		}
		if dependency.Field == "route.rule_set[0].path" {
			expected = rules
		}
		if string(content) != expected {
			t.Fatal("renewed source did not reach managed resource")
		}
	}
	// Simulate a crash after one file changed: the durable before-image restores
	// the complete preceding generation before the service starts again.
	resource := updated.Resources[0]
	previous, err := os.ReadFile(resource.Path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := []byte("interrupted replacement")
	updated.Pending = &importResourceRefresh{Active: true, Files: []importResourceReplacement{{Path: resource.Path, Before: resource.Digest, After: coreMigrationConfigDigest(string(replacement)), Previous: previous}}}
	if err := writeImportResourceState(fixture.markerPrefix, core.EngineSingBox, updated); err != nil {
		t.Fatal(err)
	}
	identity, _ := managedCoreServiceIdentity()
	if err := replaceImportedResource(resource.Path, replacement, identity); err != nil {
		t.Fatal(err)
	}
	if err := fixture.executor.RefreshImportedResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(resource.Path)
	if err != nil || string(restored) != string(previous) {
		t.Fatalf("interrupted renewal not restored: %v", err)
	}
	if value, _ := os.ReadFile(filepath.Join(fixture.stateDirectory, fixture.managed.Service+".active")); strings.TrimSpace(string(value)) != "active" {
		t.Fatal("service was not restored")
	}
}
