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

func TestExistingSingBoxOfficialFileLogMigrationUsesPanelStream(t *testing.T) {
	requireAgentRoot(t)
	logRoot := t.TempDir()
	previousLogRoot := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previousLogRoot })
	fixture, _ := configureSingBoxOfficialFixture(t, newExistingCoreMigrationFixture(t, false))
	if err := os.WriteFile(fixture.existing.ConfigPath,
		[]byte(`{"inbounds":[{"tag":"primary","type":"http","listen_port":21001}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.existing.ConfigDirectory, "10-outbounds.json"),
		[]byte(`{"outbounds":[{"type":"direct","tag":"direct"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.existing.ConfigDirectory, "20-log.json"),
		[]byte(`{"log":{"level":"info","timestamp":true,"output":"runtime.log"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	content, _, err := readExistingConfigurationSources(fixture.existing)
	if err != nil {
		t.Fatal(err)
	}
	content, err = normalizeImportedSingBoxLogDestination(content)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineSingBox, ConfigContent: content,
	}); err != nil {
		t.Fatalf("official -D/-C file-log migration failed: %v", err)
	}
	fixture.assertServiceState(t, "sing-box.service", "inactive", "disabled")
	fixture.assertServiceState(t, "qagent-sing-box.service", "active", "enabled")
	restarted := &Executor{
		Specs:                   map[core.Engine]EngineSpec{core.EngineSingBox: fixture.managed},
		ExistingSpecs:           map[core.Engine]EngineSpec{core.EngineSingBox: fixture.existing},
		ExistingDiscoveryIssues: make(map[core.Engine]string),
		MigrationMarkerPrefix:   fixture.markerPrefix, Services: fixture.executor.Services,
	}
	if err := restarted.LoadCoreMigrationState(); err != nil {
		t.Fatalf("reload official sing-box migration: %v", err)
	}
	managedContent, err := os.ReadFile(fixture.managed.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	output, destination, err := singBoxLogOutput(string(managedContent))
	if err != nil || output != "stdout" || destination != singBoxLogDestinationConsole {
		t.Fatalf("managed log output = %q/%d, %v", output, destination, err)
	}
	collector := NewCoreLogCollectorForExecutor(restarted)
	collector.mu.Lock()
	fileSources := 0
	for _, source := range collector.fileSources {
		if source.engine == core.EngineSingBox && source.kind == "file" {
			fileSources++
		}
	}
	collector.mu.Unlock()
	if fileSources != 0 {
		t.Fatalf("restarted imported sing-box file sources = %d", fileSources)
	}
	if _, err := restarted.Execute(context.Background(), core.Task{
		Action: core.ActionValidate, Engine: core.EngineSingBox, ConfigContent: content,
	}); err != nil {
		t.Fatalf("validate normalized imported log configuration: %v", err)
	}
	replacementConfig := `{"log":{"level":"info","output":"replacement.log"},"inbounds":[{"tag":"primary","type":"http","listen_port":21001}],"outbounds":[{"type":"direct","tag":"direct"}]}`
	if _, err := restarted.Execute(context.Background(), core.Task{
		Action: core.ActionDeploy, Engine: core.EngineSingBox, ConfigContent: replacementConfig,
	}); err == nil {
		t.Fatal("managed sing-box file logging was accepted")
	}
	consoleConfig := `{"log":{"level":"info","timestamp":true},"inbounds":[{"tag":"primary","type":"http","listen_port":21001}],"outbounds":[{"type":"direct","tag":"direct"}]}`
	if _, err := restarted.Execute(context.Background(), core.Task{
		Action: core.ActionDeploy, Engine: core.EngineSingBox, ConfigContent: consoleConfig,
	}); err != nil {
		t.Fatalf("deploy imported file-to-console log configuration: %v", err)
	}
	if err := collector.RefreshImportedSingBoxSource(restarted); err != nil {
		t.Fatalf("switch restarted import to console source: %v", err)
	}
	collector.mu.Lock()
	preferred := collector.preferredKind[core.EngineSingBox]
	fileSources = 0
	for _, source := range collector.fileSources {
		if source.engine == core.EngineSingBox && source.kind == "file" {
			fileSources++
		}
	}
	collector.mu.Unlock()
	if preferred != "journal" || fileSources != 0 {
		t.Fatalf("restarted source switch preferred=%q file count=%d", preferred, fileSources)
	}
}

func TestAgentUpgradeNormalizesCompletedSingBoxExternalLogAndReclaimsIt(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	logRoot := t.TempDir()
	previousLegacyRoot := legacyCoreLogRoot
	legacyCoreLogRoot = logRoot
	t.Cleanup(func() { legacyCoreLogRoot = previousLegacyRoot })
	legacyLog := filepath.Join(logRoot, "sing-box", "access.log")
	if err := os.MkdirAll(filepath.Dir(legacyLog), 0o700); err != nil {
		t.Fatal(err)
	}
	legacyContents := strings.Repeat("old access log\n", 4096)
	if err := os.WriteFile(legacyLog, []byte(legacyContents), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyConfig := fmt.Sprintf(`{"log":{"level":"info","timestamp":true,"output":%q},"inbounds":[],"outbounds":[]}`, legacyLog)
	existing := fixture.existing
	existing.Service = "sing-box.service"
	managed := fixture.managed
	managed.Service = "qagent-sing-box.service"
	if err := os.WriteFile(managed.Binary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed.ConfigPath, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	writeMigrationServiceState(t, fixture.stateDirectory, existing.Service, "inactive", "disabled")
	writeMigrationServiceState(t, fixture.stateDirectory, managed.Service, "active", "enabled")
	if err := writeCoreMigrationMarker(fixture.markerPrefix, core.EngineSingBox, coreMigrationComplete,
		coreMigrationConfigDigest(legacyConfig), coreMigrationSourceDigest(existing), "enabled", "disabled"); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{
		Specs:                   map[core.Engine]EngineSpec{core.EngineSingBox: managed},
		ExistingSpecs:           map[core.Engine]EngineSpec{core.EngineSingBox: existing},
		ExistingDiscoveryIssues: make(map[core.Engine]string),
		MigrationMarkerPrefix:   fixture.markerPrefix,
	}
	if err := executor.LoadCoreMigrationState(); err != nil {
		t.Fatal(err)
	}
	if err := executor.upgradeManagedCoreLogPolicies(context.Background()); err != nil {
		t.Fatalf("upgrade completed sing-box log policy: %v", err)
	}
	contents, err := os.ReadFile(managed.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	output, destination, err := singBoxLogOutput(string(contents))
	if err != nil || output != "stdout" || destination != singBoxLogDestinationConsole {
		t.Fatalf("normalized log output = %q/%d, %v\n%s", output, destination, err, contents)
	}
	info, err := os.Stat(legacyLog)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("retired log size = %d", info.Size())
	}
	commands, err := os.ReadFile(filepath.Join(fixture.stateDirectory, "commands.log"))
	if err != nil || !strings.Contains(string(commands), "restart "+managed.Service) {
		t.Fatalf("managed service was not restarted: %v\n%s", err, commands)
	}
	record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineSingBox)
	if err != nil || record.RequestConfigDigest != coreMigrationConfigDigest(legacyConfig) {
		t.Fatalf("migration idempotency digest changed: %+v, %v", record, err)
	}
}

func TestAgentUpgradeRollsBackCompletedSingBoxLogNormalizationOnRestartFailure(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	legacyConfig := `{"log":{"output":"/var/log/sing-box/access.log"},"inbounds":[],"outbounds":[]}`
	existing := fixture.existing
	existing.Service = "sing-box.service"
	managed := fixture.managed
	managed.Service = "qagent-sing-box.service"
	if err := os.WriteFile(managed.Binary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed.ConfigPath, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	writeMigrationServiceState(t, fixture.stateDirectory, existing.Service, "inactive", "disabled")
	writeMigrationServiceState(t, fixture.stateDirectory, managed.Service, "active", "enabled")
	writeMigrationTrigger(t, fixture.stateDirectory, "fail-managed-restart")
	if err := writeCoreMigrationMarker(fixture.markerPrefix, core.EngineSingBox, coreMigrationComplete,
		coreMigrationConfigDigest(legacyConfig), coreMigrationSourceDigest(existing), "enabled", "disabled"); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{
		Specs:                   map[core.Engine]EngineSpec{core.EngineSingBox: managed},
		ExistingSpecs:           map[core.Engine]EngineSpec{core.EngineSingBox: existing},
		ExistingDiscoveryIssues: make(map[core.Engine]string),
		MigrationMarkerPrefix:   fixture.markerPrefix,
	}
	if err := executor.LoadCoreMigrationState(); err != nil {
		t.Fatal(err)
	}
	if err := executor.upgradeManagedCoreLogPolicies(context.Background()); err == nil {
		t.Fatal("failed managed restart accepted the log policy upgrade")
	}
	contents, err := os.ReadFile(managed.ConfigPath)
	if err != nil || string(contents) != legacyConfig {
		t.Fatalf("failed upgrade did not restore configuration: %v\n%s", err, contents)
	}
	fixture.assertServiceState(t, managed.Service, "active", "enabled")
}

func TestAgentUpgradeRepairsPersistentLogsForEveryAffectedCore(t *testing.T) {
	requireAgentRoot(t)
	for _, test := range []struct {
		engine  core.Engine
		content func(string) string
		logs    func(string) map[string]string
	}{
		{
			engine: core.EngineXray,
			content: func(root string) string {
				return fmt.Sprintf(`{"log":{"access":%q,"error":%q,"loglevel":"info"},"inbounds":[],"outbounds":[]}`,
					filepath.Join(root, "xray", "access.log"), filepath.Join(root, "xray", "error.log"))
			},
			logs: func(root string) map[string]string {
				return map[string]string{
					filepath.Join(root, "xray", "access.log"): "old xray access log\n",
					filepath.Join(root, "xray", "error.log"):  "old xray error log\n",
				}
			},
		},
		{
			engine: core.EngineShadowsocksRust,
			content: func(root string) string {
				return fmt.Sprintf(`{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"test-password","log":{"level":1,"writers":[{"file":{"directory":%q,"prefix":"ssserver","suffix":"log","rotation":"daily","max_files":5}},{"syslog":{}}]}}`, filepath.Join(root, "ss-rust"))
			},
			logs: func(root string) map[string]string {
				return map[string]string{
					filepath.Join(root, "ss-rust", "ssserver.2026-09-14.log"): "old SS Rust log one\n",
					filepath.Join(root, "ss-rust", "ssserver.2026-09-15.log"): "old SS Rust log two\n",
				}
			},
		},
	} {
		t.Run(string(test.engine), func(t *testing.T) {
			fixture := newExistingCoreMigrationFixture(t, false)
			logRoot := t.TempDir()
			previousLegacyRoot := legacyCoreLogRoot
			legacyCoreLogRoot = logRoot
			t.Cleanup(func() { legacyCoreLogRoot = previousLegacyRoot })
			managed := fixture.managed
			managed.Service = "qagent-" + string(test.engine) + ".service"
			if err := os.WriteFile(managed.Binary, existingDiscoveryCoreHelper, 0o700); err != nil {
				t.Fatal(err)
			}
			content := test.content(logRoot)
			if err := os.WriteFile(managed.ConfigPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			logs := test.logs(logRoot)
			for path, contents := range logs {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			unrelated := filepath.Join(logRoot, "ss-rust", "operator.txt")
			if test.engine == core.EngineShadowsocksRust {
				if err := os.WriteFile(unrelated, []byte("keep me"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			writeMigrationServiceState(t, fixture.stateDirectory, managed.Service, "active", "enabled")
			executor := &Executor{Specs: map[core.Engine]EngineSpec{test.engine: managed}}
			if err := executor.upgradeManagedCoreLogPolicies(context.Background()); err != nil {
				t.Fatalf("upgrade %s log policy: %v", test.engine, err)
			}
			normalized, err := os.ReadFile(managed.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateNoPersistentCoreLogs(test.engine, string(normalized)); err != nil {
				t.Fatalf("normalized %s configuration still persists logs: %v\n%s", test.engine, err, normalized)
			}
			for path := range logs {
				info, err := os.Stat(path)
				if err != nil || info.Size() != 0 {
					t.Fatalf("retired %s log %s was not cleared: %+v, %v", test.engine, path, info, err)
				}
			}
			if test.engine == core.EngineShadowsocksRust {
				contents, err := os.ReadFile(unrelated)
				if err != nil || string(contents) != "keep me" {
					t.Fatalf("unrelated SS Rust file changed: %q, %v", contents, err)
				}
			}
			commands, err := os.ReadFile(filepath.Join(fixture.stateDirectory, "commands.log"))
			if err != nil || !strings.Contains(string(commands), "restart "+managed.Service) {
				t.Fatalf("managed %s service was not restarted: %v\n%s", test.engine, err, commands)
			}
		})
	}
}

func TestRetiredCoreLogCleanupUsesRestrictedSystemdHelperWhenSandboxed(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	logDirectory := filepath.Join(root, "sing-box")
	if err := os.MkdirAll(logDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDirectory, "access.log")
	if err := os.WriteFile(logPath, []byte("retired log bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	argumentsPath := filepath.Join(root, "systemd-run.arguments")
	runner := filepath.Join(root, "systemd-run")
	truncate := filepath.Join(root, "truncate")
	writeExecutable(t, runner, "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$@\" > "+shellQuote(argumentsPath)+"\nwhile [ \"$1\" != -- ]; do shift; done\nshift\nexec \"$@\"\n")
	writeExecutable(t, truncate, "#!/bin/sh\nexec /usr/bin/truncate \"$@\"\n")
	previousRoot := legacyCoreLogRoot
	previousRunner := retiredLogSystemdRunPath
	previousTruncate := retiredLogTruncatePath
	previousDirect := truncateRetiredLogDirect
	legacyCoreLogRoot = root
	retiredLogSystemdRunPath = runner
	retiredLogTruncatePath = truncate
	truncateRetiredLogDirect = func(string, int64) error { return os.ErrPermission }
	t.Cleanup(func() {
		legacyCoreLogRoot = previousRoot
		retiredLogSystemdRunPath = previousRunner
		retiredLogTruncatePath = previousTruncate
		truncateRetiredLogDirect = previousDirect
	})
	reclaimed, err := truncateRetiredCoreLog(context.Background(), logPath, defaultSystemdServiceManager())
	if err != nil || reclaimed != int64(len("retired log bytes")) {
		t.Fatalf("restricted cleanup = %d bytes, %v", reclaimed, err)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("retired log size = %d", info.Size())
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil || !strings.Contains(string(arguments), "CapabilityBoundingSet=CAP_DAC_OVERRIDE") ||
		!strings.Contains(string(arguments), "ReadWritePaths="+logPath) {
		t.Fatalf("systemd cleanup was not narrowly sandboxed: %v\n%s", err, arguments)
	}
}

func TestExistingCoreMigrationNormalizesPersistentXrayLogsIntoManagedStream(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	content := `{"log":{"access":"/var/log/xray/access.log","error":"/var/log/xray/error.log","loglevel":"info"},"inbounds":[],"outbounds":[]}`
	if err := os.WriteFile(fixture.existing.ConfigPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeImportedXrayLogDestinations(content)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: normalized,
	}); err != nil {
		t.Fatalf("normalized persistent log migration: %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "inactive", "disabled")
	fixture.assertServiceState(t, "qagent-xray.service", "active", "enabled")
	managedContent, err := os.ReadFile(fixture.managed.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNoPersistentCoreLogs(core.EngineXray, string(managedContent)); err != nil {
		t.Fatalf("managed Xray snapshot retained persistent logs: %v", err)
	}
}
