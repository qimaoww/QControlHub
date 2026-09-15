package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPersistentCoreLogOutputsAreRejected(t *testing.T) {
	t.Parallel()
	fixtures := []struct {
		engine  core.Engine
		content string
	}{
		{core.EngineXray, `{"log":{"loglevel":"info","access":"/var/log/xray.log"}}`},
		{core.EngineSingBox, `{"log":{"level":"info","output":"/var/log/sing-box.log"}}`},
		{core.EngineSingBox, `{"log":{"level":"info","output":"none"}}`},
		{core.EngineShadowsocksRust, `{"log":{"config_path":"/etc/log4rs.yaml"}}`},
		{core.EngineShadowsocksRust, `{"log":{"writers":[{"file":{"directory":"/var/log/shadowsocks-rust"}}]}}`},
		{core.EngineShadowsocksRust, `{"log":{"writers":[{"syslog":{}}]}}`},
	}
	for _, fixture := range fixtures {
		if err := validateNoPersistentCoreLogs(fixture.engine, fixture.content); err == nil {
			t.Errorf("%s persistent log output was accepted", fixture.engine)
		}
	}
	if err := validateNoPersistentCoreLogs(core.EngineXray, `{"log":{"loglevel":"info"}}`); err != nil {
		t.Fatalf("stdout Xray logging was rejected: %v", err)
	}
	if err := validateNoPersistentCoreLogs(core.EngineXray, `{"log":{"loglevel":"info","access":"none"}}`); err != nil {
		t.Fatalf("disabled Xray file logging was rejected: %v", err)
	}
	if err := validateNoPersistentCoreLogs(core.EngineXray, `{"log":{"access":" NONE "}}`); err == nil {
		t.Fatal("Xray file destination disguised as a none variant was accepted")
	}
	if err := validateNoPersistentCoreLogs(core.EngineShadowsocksRust, `{"log":{"level":1,"writers":[{"console":{}}]}}`); err != nil {
		t.Fatalf("console SS Rust logging was rejected: %v", err)
	}
}

func TestNormalizeImportedXrayLogDestinations(t *testing.T) {
	t.Parallel()
	content := `{"log":{"access":"/var/log/xray/access.log","error":" NONE ","loglevel":"info","dnsLog":true,"maskAddress":"half"},"inbounds":[],"outbounds":[]}`
	normalized, err := normalizeImportedXrayLogDestinations(content)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(normalized), &root); err != nil {
		t.Fatal(err)
	}
	logging := root["log"].(map[string]any)
	if logging["access"] != "" || logging["error"] != "" || logging["loglevel"] != "info" ||
		logging["dnsLog"] != true || logging["maskAddress"] != "half" {
		t.Fatalf("normalized Xray log policy = %+v", logging)
	}
	unchanged := `{"log":{"access":"none","error":"","loglevel":"warning"},"inbounds":[]}`
	if got, err := normalizeImportedXrayLogDestinations(unchanged); err != nil || got != unchanged {
		t.Fatalf("non-persistent Xray log policy changed: %q, %v", got, err)
	}
	if _, err := normalizeImportedXrayLogDestinations(`{"log":{"access":42}}`); err == nil {
		t.Fatal("non-string Xray log destination was accepted")
	}
}

func TestNormalizeImportedSingBoxLogDestination(t *testing.T) {
	root := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = root
	t.Cleanup(func() { importedSingBoxLogRoot = previous })

	unsafe := `{"log":{"level":"info","timestamp":true,"output":"/var/log/sing-box/box.log"},"inbounds":[],"outbounds":[]}`
	normalized, err := normalizeImportedSingBoxLogDestination(unsafe)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(normalized), &decoded); err != nil {
		t.Fatal(err)
	}
	logging := decoded["log"].(map[string]any)
	if logging["output"] != "stdout" || logging["level"] != "info" || logging["timestamp"] != true {
		t.Fatalf("normalized sing-box log policy = %+v", logging)
	}

	for name, content := range map[string]string{
		"relative file": `{"log":{"output":"runtime.log"}}`,
		"managed file":  `{"log":{"output":"` + filepath.ToSlash(filepath.Join(root, "runtime.log")) + `"}}`,
	} {
		got, err := normalizeImportedSingBoxLogDestination(content)
		if err != nil {
			t.Errorf("%s normalization failed: %v", name, err)
			continue
		}
		output, destination, err := singBoxLogOutput(got)
		if err != nil || output != "stdout" || destination != singBoxLogDestinationConsole {
			t.Errorf("%s normalized to %q/%d: %v", name, output, destination, err)
		}
	}
	for name, content := range map[string]string{
		"console":  `{"log":{"output":"stderr"}}`,
		"disabled": `{"log":{"disabled":true,"output":"/var/log/sing-box/box.log"}}`,
	} {
		if got, err := normalizeImportedSingBoxLogDestination(content); err != nil || got != content {
			t.Errorf("%s changed: %q, %v", name, got, err)
		}
	}
	if _, err := normalizeImportedSingBoxLogDestination("{\"log\":{\"output\":\"bad\\npath\"}}"); err == nil {
		t.Fatal("sing-box log output containing a control character was normalized")
	}
}

func TestNormalizeImportedSSRustLogDestinations(t *testing.T) {
	t.Parallel()
	content := `{"log":{"level":1,"format":{"without_time":true},"config_path":"/etc/log4rs.yaml","writers":[{"console":{"level":2}},{"file":{"directory":"/var/log/shadowsocks-rust","level":3}},{"syslog":{"facility":1}}]},"servers":[]}`
	normalized, err := normalizeImportedSSRustLogDestinations(content)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(normalized), &root); err != nil {
		t.Fatal(err)
	}
	logging := root["log"].(map[string]any)
	if _, exists := logging["config_path"]; exists {
		t.Fatal("legacy log4rs config path survived normalization")
	}
	writers := logging["writers"].([]any)
	if len(writers) != 1 || writers[0].(map[string]any)["console"].(map[string]any)["level"] != float64(2) {
		t.Fatalf("normalized SS Rust writers = %+v", writers)
	}
	if logging["level"] != float64(1) || logging["format"].(map[string]any)["without_time"] != true {
		t.Fatalf("global SS Rust log policy changed: %+v", logging)
	}

	onlyFile := `{"log":{"writers":[{"file":{"directory":"logs","level":3,"format":{"without_time":true},"rotation":"daily"}}]}}`
	normalized, err = normalizeImportedSSRustLogDestinations(onlyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(normalized), &root); err != nil {
		t.Fatal(err)
	}
	writers = root["log"].(map[string]any)["writers"].([]any)
	console := writers[0].(map[string]any)["console"].(map[string]any)
	if len(writers) != 1 || console["level"] != float64(3) || console["format"].(map[string]any)["without_time"] != true {
		t.Fatalf("file-only writer did not become an equivalent console writer: %+v", writers)
	}

	unchanged := `{"log":{"writers":[]},"servers":[]}`
	if got, err := normalizeImportedSSRustLogDestinations(unchanged); err != nil || got != unchanged {
		t.Fatalf("disabled SS Rust logging changed: %q, %v", got, err)
	}
	if _, err := normalizeImportedSSRustLogDestinations(`{"log":{"writers":[{"network":{}}]}}`); err == nil {
		t.Fatal("unknown SS Rust writer was accepted")
	}
}

func TestImportedSingBoxRejectsManagedFileLogOutput(t *testing.T) {
	root := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = root
	t.Cleanup(func() { importedSingBoxLogRoot = previous })
	executor := &Executor{}
	spec := EngineSpec{Binary: "/usr/bin/true", ConfigPath: filepath.Join(t.TempDir(), "config.json")}
	content := `{"log":{"output":"runtime.log"},"inbounds":[],"outbounds":[]}`
	if _, err := executor.validateImportedSnapshot(context.Background(), core.EngineSingBox, spec, content); err == nil {
		t.Fatal("imported file log output bypassed console-only validation")
	}
	unsafe := `{"log":{"output":"/etc/shadow"},"inbounds":[],"outbounds":[]}`
	if _, err := executor.validateImportedSnapshot(context.Background(), core.EngineSingBox, spec, unsafe); err == nil {
		t.Fatal("imported log.output outside the managed boundary was accepted")
	}
}
