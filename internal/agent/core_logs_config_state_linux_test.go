//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestLostSingBoxConfigurationSurvivesCollectorRestart(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	content := `{"log":{"level":"info"},"inbounds":[],"outbounds":[]}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{MigrationMarkerPrefix: filepath.Join(directory, "migration"), Specs: map[core.Engine]EngineSpec{
		core.EngineSingBox: {Binary: "/bin/sh", ConfigPath: configPath, Service: "qagent-sing-box.service"},
	}}
	first := NewCoreLogCollectorForExecutor(executor)
	first.markCoreLogDelivery(core.EngineSingBox, core.CoreLogSourceJournalHost)
	if _, err := readCoreLogConfigurationHistory(coreLogConfigurationHistoryPath(executor)); err != nil {
		t.Fatalf("successful configuration was not remembered: %v", err)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	restarted := NewCoreLogCollectorForExecutor(executor)
	for _, kind := range []string{core.CoreLogSourceJournal, core.CoreLogSourceJournalHost} {
		restarted.markCoreLogDelivery(core.EngineSingBox, kind)
		if status := restarted.Status()[core.EngineSingBox]; status.Status != core.CoreLogStatusFailed || status.Error != core.CoreLogErrorConfigMissing {
			t.Fatalf("late %s delivery hid a lost configuration after restart: %+v", kind, status)
		}
	}
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RefreshImportedSingBoxSource(executor); err != nil {
		t.Fatal(err)
	}
	restarted.markCoreLogDelivery(core.EngineSingBox, core.CoreLogSourceJournalHost)
	if status := restarted.Status()[core.EngineSingBox]; status.Status != core.CoreLogStatusActive || status.Error != "" {
		t.Fatalf("restored configuration did not clear the old failure: %+v", status)
	}
}

func TestSingBoxConfigurationBackupIdentifiesPreHistoryDeployment(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath+".bak-previous", []byte(`{"log":{"level":"info"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: {Binary: "/bin/sh", ConfigPath: configPath, Service: "qagent-sing-box.service"}}}
	collector := NewCoreLogCollectorForExecutor(executor)
	if status := collector.Status()[core.EngineSingBox]; status.Status != core.CoreLogStatusFailed || status.Error != core.CoreLogErrorConfigMissing {
		t.Fatalf("pre-history deployment loss became never-configured: %+v", status)
	}
}

func TestSingBoxHistoryWriteFailureDoesNotStopWorkingTransport(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"log":{"level":"info"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{MigrationMarkerPrefix: filepath.Join(blocker, "migration"), Specs: map[core.Engine]EngineSpec{
		core.EngineSingBox: {Binary: "/bin/sh", ConfigPath: configPath, Service: "qagent-sing-box.service"},
	}}
	collector := NewCoreLogCollectorForExecutor(executor)
	collector.markCoreLogDelivery(core.EngineSingBox, core.CoreLogSourceJournalHost)
	if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
		t.Fatal(err)
	}
	if status := collector.Status()[core.EngineSingBox]; status.Status != core.CoreLogStatusActive || status.Source != core.CoreLogSourceJournalHost {
		t.Fatalf("history write failure stopped a proven transport: %+v", status)
	}
}

func TestRunningSingBoxWithoutHistoryStillReportsLostConfiguration(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: {Binary: "/bin/sh", ConfigPath: configPath, Service: "qagent-sing-box.service"}}}
	collector := NewCoreLogCollectorForExecutor(executor)
	collector.serviceState = staticCoreLogServiceState("active")
	if err := collector.RefreshImportedSingBoxSource(executor); err == nil {
		t.Fatal("an active process without its config was treated as never configured")
	}
	if status := collector.Status()[core.EngineSingBox]; status.Status != core.CoreLogStatusFailed || status.Error != core.CoreLogErrorConfigMissing {
		t.Fatalf("lost configuration without a history marker: %+v", status)
	}
}

func TestMissingSingBoxErrorDoesNotMaskRestoredOrRemovedConfiguration(t *testing.T) {
	for _, state := range []string{"invalid-restoration", "removed-installation", "disabled-engine"} {
		t.Run(state, func(t *testing.T) {
			directory := t.TempDir()
			configPath, binary := filepath.Join(directory, "config.json"), filepath.Join(directory, "sing-box")
			for path, content := range map[string]string{configPath: `{"log":{"level":"info"}}`, binary: "isolated-placeholder"} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: {Binary: binary, ConfigPath: configPath, Service: "qagent-sing-box.service"}}}
			collector := NewCoreLogCollectorForExecutor(executor)
			if err := os.Remove(configPath); err != nil {
				t.Fatal(err)
			}
			if err := collector.RefreshImportedSingBoxSource(executor); err == nil {
				t.Fatal("missing deployed config did not fail")
			}
			switch state {
			case "invalid-restoration":
				if err := os.WriteFile(configPath, []byte("invalid json"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "removed-installation":
				if err := os.Remove(binary); err != nil {
					t.Fatal(err)
				}
			case "disabled-engine":
				delete(executor.Specs, core.EngineSingBox)
			}
			err := collector.RefreshImportedSingBoxSource(executor)
			status, exists := collector.Status()[core.EngineSingBox]
			if state == "invalid-restoration" {
				if err == nil || status.Status != core.CoreLogStatusFailed || status.Error != "config-invalid" {
					t.Fatalf("old missing-config error masked the restored file error: %+v %v", status, err)
				}
			} else if err != nil || exists {
				t.Fatalf("old missing-config error survived removal: %+v %v", status, err)
			}
		})
	}
}
