package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestManagedConfigFilesImmutableAndRollbackSafe(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "config.json")
	first := `{"inbounds":[{"tag":"a","port":1080}],"outbounds":[{"tag":"direct"}]}`
	second := `{"inbounds":[{"tag":"b","port":1081}],"outbounds":[{"tag":"direct"}]}`
	for _, content := range []string{first, second, first} {
		if err := writeManagedConfigFiles(core.EngineXray, destination, content, fileMetadata{mode: 0o600}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("bundles %v %v", entries, err)
	}
	sum := sha256.Sum256([]byte(first))
	fragment := filepath.Join(directory, "sources-v2-"+hex.EncodeToString(sum[:]), "inbounds/a.json")
	if err := os.WriteFile(fragment, []byte(`{"inbounds":[{}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigFiles(core.EngineXray, destination, first, fileMetadata{mode: 0o600}); err == nil {
		t.Fatal("accepted tampered bundle")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("staging must not activate runtime config")
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineShadowsocksRust} {
		if err := writeManagedConfigFiles(engine, destination, "single", fileMetadata{mode: 0o600}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManagedConfigFilesPreserveLegacyBundles(t *testing.T) {
	directory := t.TempDir()
	content := `{"inbounds":[{"tag":"VLESS-REALITY-443","port":443}],"outbounds":[{"tag":"direct"}]}`
	sum := sha256.Sum256([]byte(content))
	legacy := filepath.Join(directory, "sources-"+hex.EncodeToString(sum[:]), "inbounds")
	if err := os.MkdirAll(legacy, 0o750); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(legacy, "0000.json")
	if err := os.WriteFile(oldFile, []byte("legacy snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigFiles(core.EngineXray, filepath.Join(directory, "config.json"), content, fileMetadata{mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	if value, err := os.ReadFile(oldFile); err != nil || string(value) != "legacy snapshot" {
		t.Fatalf("legacy rollback bundle modified: %s %v", value, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "sources-v2-"+hex.EncodeToString(sum[:]), "inbounds/VLESS-REALITY-443.json")); err != nil {
		t.Fatal(err)
	}
}
