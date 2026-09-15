//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestExistingSingBoxSnapshotMergesExactConfigDirectory(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	validationDirectory := filepath.Join(root, "managed")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(validationDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(root, "config.json")
	if err := os.WriteFile(primary, []byte(`{"log":{"level":"info","timestamp":true,"output":"/var/log/sing-box/box.log"},"inbounds":[{"tag":"primary"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "10-outbounds.json"), []byte(`{"outbounds":[{"tag":"direct","type":"direct"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "20-log.json"), []byte(`{"log":{"level":"debug"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "sing-box")
	if err := os.WriteFile(binary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := EngineSpec{Binary: binary, ConfigPath: primary, ConfigDirectory: configDirectory, Service: "sing-box.service"}
	managed := EngineSpec{Binary: binary, ConfigPath: filepath.Join(validationDirectory, "config.json"), Service: "qagent-sing-box.service"}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: managed}, ExistingSpecs: map[core.Engine]EngineSpec{core.EngineSingBox: existing}}
	content, err := executor.readExistingConfig(context.Background(), core.EngineSingBox, managed, existing)
	if err != nil {
		t.Fatalf("read merged sing-box snapshot: %v", err)
	}
	for _, required := range []string{`"tag": "primary"`, `"tag": "direct"`, `"level": "debug"`} {
		if !strings.Contains(content, required) {
			t.Errorf("merged snapshot is missing %s: %s", required, content)
		}
	}
	if !strings.Contains(content, `"output": "stdout"`) || !strings.Contains(content, `"timestamp": true`) {
		t.Fatalf("unsafe existing sing-box file log was not normalized to managed console output: %s", content)
	}
	if strings.Contains(content, `"level": "info"`) {
		t.Fatalf("later path unexpectedly replaced the earlier sorted sing-box value: %s", content)
	}

	if err := os.Symlink(filepath.Join(configDirectory, "10-outbounds.json"), filepath.Join(configDirectory, "30-linked.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.readExistingConfig(context.Background(), core.EngineSingBox, managed, existing); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlinked config-directory entry error = %v", err)
	}
	if err := os.Remove(filepath.Join(configDirectory, "30-linked.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configDirectory, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.readExistingConfig(context.Background(), core.EngineSingBox, managed, existing); err == nil || !strings.Contains(err.Error(), "writable by group") {
		t.Fatalf("group-writable config-directory error = %v", err)
	}
	if err := os.Chmod(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	realDirectory := configDirectory + "-real"
	if err := os.Rename(configDirectory, realDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDirectory, configDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.readExistingConfig(context.Background(), core.EngineSingBox, managed, existing); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("symlinked config-directory error = %v", err)
	}
}

func TestDecodeExtendedJSONPreservesCommentShapedStrings(t *testing.T) {
	content := `{
		// leading comment
		"url": "http://example.com#frag", # trailing hash comment
		"path": "a//b/*c*/d", /* block comment */
		"escaped": "a\"b\\c"
	}`
	decoded, err := decodeExtendedJSON(content)
	if err != nil {
		t.Fatalf("decodeExtendedJSON() error = %v", err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("decoded value = %T, want object", decoded)
	}
	if object["url"] != "http://example.com#frag" {
		t.Fatalf("url was corrupted: %v", object["url"])
	}
	if object["path"] != "a//b/*c*/d" {
		t.Fatalf("comment-shaped string was corrupted: %v", object["path"])
	}
	if object["escaped"] != "a\"b\\c" {
		t.Fatalf("escaped string was corrupted: %v", object["escaped"])
	}
}

func TestExistingSingBoxExtendedJSONConfigDirectorySnapshot(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(root, "config.json")
	if err := os.WriteFile(primary, []byte(`{"log": { "level": "info" } // primary
, "inbounds": [{ "tag": "primary" }]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "10-outbounds.json"), []byte(`{
  // outbound fragment
  "outbounds": [{ "tag": "direct", "url": "http://example.com#frag" }]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "20-log.json"), []byte(`{"log": { "level": "debug" } /* override */}`), 0o600); err != nil {
		t.Fatal(err)
	}
	existing := EngineSpec{ConfigPath: primary, ConfigDirectory: configDirectory}
	content, _, err := readExistingConfigurationSources(existing)
	if err != nil {
		t.Fatalf("readExistingConfigurationSources() error = %v", err)
	}
	for _, required := range []string{`"tag": "primary"`, `"tag": "direct"`, `"level": "debug"`, `"http://example.com#frag"`} {
		if !strings.Contains(content, required) {
			t.Errorf("extended-JSON snapshot is missing %s: %s", required, content)
		}
	}
	if strings.Contains(content, `"level": "info"`) {
		t.Fatalf("later sorted source unexpectedly replaced the win-by-destination value: %s", content)
	}
	// The comment-shaped URL text must remain inside the string, undecorated.
	if strings.Contains(content, "example.com#frag ") || strings.Contains(content, "// outbound fragment") {
		t.Fatalf("comment stripping altered string content or left comments in the snapshot: %s", content)
	}
}

func TestExistingSingBoxExtendedJSONRejectsMalformedSources(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", `{"inbounds": []}`)
	for _, source := range []string{"10-broken.json", "20-trailing.json"} {
		if err := os.WriteFile(filepath.Join(configDirectory, source), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	existing := EngineSpec{ConfigPath: filepath.Join(root, "config.json"), ConfigDirectory: configDirectory}

	if err := os.WriteFile(filepath.Join(configDirectory, "10-broken.json"), []byte(`{"a":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readExistingConfigurationSources(existing); err == nil || !strings.Contains(err.Error(), "decode configuration source") {
		t.Fatalf("invalid JSON source error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(configDirectory, "10-broken.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "20-trailing.json"), []byte(`{"b":1} garbage`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readExistingConfigurationSources(existing); err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("non-comment trailing source error = %v", err)
	}
}

func TestExtendedJSONClosedBlockCommentParses(t *testing.T) {
	decoded, err := decodeExtendedJSON(`{"a": 1 /* closed */ , "b": [1 /* inline */, 2]}`)
	if err != nil {
		t.Fatalf("decodeExtendedJSON() with closed block comment error = %v", err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("decoded value = %T, want object", decoded)
	}
	b, ok := object["b"].([]any)
	if !ok || len(b) != 2 || b[0] != json.Number("1") || b[1] != json.Number("2") {
		t.Fatalf("closed block comment structure was corrupted: %v", object["b"])
	}
}

func TestExtendedJSONRejectsUnterminatedBlockComments(t *testing.T) {
	for _, content := range []string{
		`{"a":1} /* unterminated`,
		`{"a": /* unterminated`,
		`{"a": 1, /* unterminated`,
	} {
		if _, err := decodeExtendedJSON(content); err == nil {
			t.Errorf("decodeExtendedJSON(%q) unexpectedly accepted an unterminated block comment", content)
		} else if !strings.Contains(err.Error(), "unexpected end of JSON comment") {
			t.Errorf("decodeExtendedJSON(%q) error = %v, want unexpected end of JSON comment", content, err)
		}
	}

	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"inbounds": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "10-fragment.json"), []byte(`{"outbounds": []} /* unterminated`), 0o600); err != nil {
		t.Fatal(err)
	}
	existing := EngineSpec{ConfigPath: filepath.Join(root, "config.json"), ConfigDirectory: configDirectory}
	if _, _, err := readExistingConfigurationSources(existing); err == nil || !strings.Contains(err.Error(), "unexpected end of JSON comment") {
		t.Fatalf("directory fragment with unterminated block comment error = %v", err)
	}
}
