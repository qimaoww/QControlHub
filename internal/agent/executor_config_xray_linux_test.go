//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestReadExistingXrayConfigurationUsesSourceDump(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(root, "config.json")
	if err := os.WriteFile(primary, []byte(`{"log":{"loglevel":"info"},"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "20-log.json"), []byte(`{"log":{"loglevel":"debug"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dumped := `{"log":{"loglevel":"error","access":"/var/log/xray/access.log","error":"/var/log/xray/error.log"},"inbounds":[],"outbounds":[]}`
	binary := filepath.Join(root, "xray")
	if err := os.WriteFile(binary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "xray-dump.json"), []byte(dumped), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary+".control.json", []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	managed := EngineSpec{ConfigPath: filepath.Join(root, "managed", "config.json")}
	existing := EngineSpec{Binary: binary, ConfigPath: primary, ConfigDirectory: configDirectory}
	content, err := (&Executor{}).readExistingConfig(context.Background(), core.EngineXray, managed, existing)
	if err != nil {
		t.Fatalf("readExistingConfig() error = %v", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal([]byte(content), &normalized); err != nil {
		t.Fatal(err)
	}
	logging := normalized["log"].(map[string]any)
	if logging["access"] != "" || logging["error"] != "" || logging["loglevel"] != "error" {
		t.Fatalf("normalized Xray log policy = %+v", logging)
	}
	if _, err := os.Stat(binary + ".invocations"); err != nil {
		t.Fatalf("ordinary root-owned source binary was not invoked directly: %v", err)
	}
}

func TestReadExistingXrayConfigurationStagesRootOwnedInstallerBinary(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(root, "config.json")
	if err := os.WriteFile(primary, []byte(`{"log":{"loglevel":"info"},"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dumped := `{"log":{"loglevel":"error"},"inbounds":[],"outbounds":[]}`
	binary := filepath.Join(root, "xray")
	if err := os.WriteFile(binary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "xray-dump.json"), []byte(dumped), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary+".control.json", []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	allowFixtureOrphanOwnerPath(t, binary)
	stateDirectory := filepath.Join(root, "state")
	if err := os.Mkdir(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	managed := EngineSpec{ConfigPath: filepath.Join(root, "managed", "config.json")}
	existing := EngineSpec{Binary: binary, ConfigPath: primary, ConfigDirectory: configDirectory}
	executor := &Executor{MigrationMarkerPrefix: filepath.Join(stateDirectory, "agent-state.json.core-migration")}
	content, err := executor.readExistingConfig(context.Background(), core.EngineXray, managed, existing)
	if err != nil {
		t.Fatalf("readExistingConfig() error = %v", err)
	}
	if !strings.Contains(content, `"loglevel":"error"`) {
		t.Fatalf("root-owned installer Xray dump = %s", content)
	}
	if _, err := os.Stat(binary + ".invocations"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root-owned installer source binary was invoked directly: %v", err)
	}
	if staged, err := filepath.Glob(filepath.Join(stateDirectory, ".qcontrolhub-core-*.tmp")); err != nil || len(staged) != 0 {
		t.Fatalf("protected invocation copies after cleanup = %v, %v", staged, err)
	}
}

func TestReadExistingXrayConfigurationStagesOrphanOwnedInstallerBinary(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(root, "config.json")
	if err := os.WriteFile(primary, []byte(`{"log":{"loglevel":"info"},"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "20-log.json"), []byte(`{"log":{"loglevel":"debug"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dumped := `{"log":{"loglevel":"error"},"inbounds":[],"outbounds":[]}`
	binary := filepath.Join(root, "xray")
	if err := os.WriteFile(binary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "xray-dump.json"), []byte(dumped), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary+".control.json", []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	allowFixtureOrphanOwnerPath(t, binary)
	assignInactiveOrphanOwner(t, binary)
	if err := os.Chmod(binary, 0o744); err != nil {
		t.Fatal(err)
	}
	stateDirectory := filepath.Join(root, "state")
	if err := os.Mkdir(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	managed := EngineSpec{ConfigPath: filepath.Join(root, "managed", "config.json")}
	existing := EngineSpec{Binary: binary, ConfigPath: primary, ConfigDirectory: configDirectory}
	executor := &Executor{MigrationMarkerPrefix: filepath.Join(stateDirectory, "agent-state.json.core-migration")}
	content, err := executor.readExistingConfig(context.Background(), core.EngineXray, managed, existing)
	if err != nil {
		t.Fatalf("readExistingConfig() error = %v", err)
	}
	if !strings.Contains(content, `"loglevel":"error"`) {
		t.Fatalf("orphan-owned Xray dump = %s", content)
	}
	if _, err := os.Stat(binary + ".invocations"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan-owned source binary was invoked directly: %v", err)
	}
	if staged, err := filepath.Glob(filepath.Join(stateDirectory, ".qcontrolhub-core-*.tmp")); err != nil || len(staged) != 0 {
		t.Fatalf("protected invocation copies after cleanup = %v, %v", staged, err)
	}
}

func TestReadExistingXraySourceDigestTracksEverySupportedFormat(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"10-base.json":   `{"log":{"loglevel":"info"}}`,
		"20-extra.jsonc": `{"log":{"loglevel":"warning"}}`,
		"30-extra.toml":  `[log]\nloglevel = "error"`,
		"40-extra.yaml":  `log: {loglevel: debug}`,
		"50-extra.yml":   `log: {loglevel: none}`,
		"notes.txt":      "ignored by Xray",
	} {
		if err := os.WriteFile(filepath.Join(configDirectory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := EngineSpec{ConfigDirectory: configDirectory}
	digest, err := readExistingXraySourceDigest(spec)
	if err != nil {
		t.Fatalf("readExistingXraySourceDigest() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "40-extra.yaml"), []byte(`log: {loglevel: error}`), 0o600); err != nil {
		t.Fatal(err)
	}
	changedDigest, err := readExistingXraySourceDigest(spec)
	if err != nil {
		t.Fatalf("readExistingXraySourceDigest() after YAML change error = %v", err)
	}
	if changedDigest == digest {
		t.Fatal("source digest did not change after an Xray YAML source changed")
	}
}
