package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestManagedConfigFilesPairDedicatedExit(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			directory := t.TempDir()
			plan, err := serverconfig.PlanPresetAccounting(engine, `{"inbounds":[{"tag":"a","protocol":"socks","type":"socks","port":1080,"listen_port":1080}],"outbounds":[{"tag":"direct","protocol":"freedom","type":"direct"}]}`)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeManagedConfigFiles(engine, filepath.Join(directory, "config.json"), plan.Content, fileMetadata{mode: 0o600}); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256([]byte(plan.Content))
			bundle := filepath.Join(directory, "sources-v3-"+hex.EncodeToString(sum[:]))
			files, err := os.ReadDir(bundle)
			if err != nil || len(files) != 2 || files[0].Name() != "common.json" || files[1].Name() != "inbounds" {
				t.Fatalf("unexpected paired layout: %+v %v", files, err)
			}
			fragment, err := os.ReadFile(filepath.Join(bundle, "inbounds/a.json"))
			if err != nil || !strings.Contains(string(fragment), plan.Ports[0].Outbounds[0]) {
				t.Fatalf("dedicated exit missing from inbound file: %s %v", fragment, err)
			}
		})
	}
}

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
	fragment := filepath.Join(directory, "sources-v3-"+hex.EncodeToString(sum[:]), "inbounds/a.json")
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
	for _, prefix := range []string{"sources-", "sources-v2-"} {
		legacy := filepath.Join(directory, prefix+hex.EncodeToString(sum[:]), "inbounds")
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
	}
	if _, err := os.Stat(filepath.Join(directory, "sources-v3-"+hex.EncodeToString(sum[:]), "inbounds/VLESS-REALITY-443.json")); err != nil {
		t.Fatal(err)
	}
}

func TestManagedWireGuardConfigFilesPreserveV3AndRollbackV4(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "config.json")
	protocol, _ := serverconfig.FindProtocol(core.EngineSingBox, serverconfig.ProtocolWireGuard)
	input, err := serverconfig.NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	contentFor := func(tag string, port int) string {
		t.Helper()
		input.Tag, input.Port = tag, port
		generated, err := serverconfig.Generate(core.EngineSingBox, input)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := serverconfig.PrepareIndependentEgress(core.EngineSingBox, generated)
		if err != nil {
			t.Fatal(err)
		}
		return plan.Content
	}
	first, second := contentFor("wg-old", 51820), contentFor("wg-new", 51821)
	firstSum := sha256.Sum256([]byte(first))
	legacy := filepath.Join(directory, "sources-v3-"+hex.EncodeToString(firstSum[:]))
	if err := os.Mkdir(legacy, 0o750); err != nil {
		t.Fatal(err)
	}
	legacyFile := filepath.Join(legacy, "common.json")
	if err := os.WriteFile(legacyFile, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{first, second, first} {
		if err := writeManagedConfigFiles(core.EngineSingBox, destination, content, fileMetadata{mode: 0o600}); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(content))
		bundle := filepath.Join(directory, "sources-v4-"+hex.EncodeToString(sum[:]))
		files, err := serverconfig.SplitConfigFiles(core.EngineSingBox, content)
		if err != nil || len(files) != 2 || !strings.HasPrefix(files[1].Path, "endpoints/") ||
			!strings.Contains(files[1].Content, "qch-trf-") {
			t.Fatalf("endpoint and dedicated outbound were not paired: %+v %v", files, err)
		}
		for _, file := range files {
			path := filepath.Join(bundle, file.Path)
			value, err := os.ReadFile(path)
			if err != nil || string(value) != file.Content {
				t.Fatalf("wrong immutable endpoint snapshot: %s %v", path, err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("endpoint key fragment has unsafe permissions: %v", err)
			}
		}
	}
	if value, err := os.ReadFile(legacyFile); err != nil || string(value) != first {
		t.Fatalf("v3 rollback snapshot was modified: %v", err)
	}
	fragment := filepath.Join(directory, "sources-v4-"+hex.EncodeToString(firstSum[:]), "endpoints", "wg-old.json")
	if err := os.WriteFile(fragment, []byte(`{"endpoints":[{}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigFiles(core.EngineSingBox, destination, first, fileMetadata{mode: 0o600}); err == nil {
		t.Fatal("tampered endpoint bundle was accepted")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("bundle staging must never activate the runtime configuration")
	}
}
