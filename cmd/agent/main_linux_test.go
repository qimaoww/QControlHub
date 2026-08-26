//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/agent"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

var inspectExistingCoreHelper []byte

func TestMain(tests *testing.M) {
	helper, err := buildInspectExistingCoreHelper()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	inspectExistingCoreHelper = helper
	os.Exit(tests.Run())
}

func buildInspectExistingCoreHelper() ([]byte, error) {
	directory, err := os.MkdirTemp("", ".qagent-inspect-core-helper-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	sourcePath := filepath.Join(directory, "main.go")
	const source = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	executable, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	workingDirectory, _ := os.Getwd()
	record, err := os.OpenFile(executable+".checks.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(2)
	}
	_, _ = fmt.Fprintf(record, "cwd=%s args=%s\n", workingDirectory, strings.Join(os.Args[1:], " "))
	_ = record.Close()
	arguments := os.Args[1:]
	if len(arguments) >= 2 && arguments[0] == "run" && (arguments[1] == "-test" || arguments[1] == "-dump") {
		for index := 2; index+1 < len(arguments); index++ {
			if arguments[index] != "-config" {
				continue
			}
			contents, err := os.ReadFile(arguments[index+1])
			if err != nil || !json.Valid(contents) {
				os.Exit(1)
			}
			if arguments[1] == "-dump" {
				_, _ = os.Stdout.Write(contents)
			}
			return
		}
		os.Exit(2)
	}
	if len(arguments) > 0 && arguments[0] == "check" {
		return
	}
	os.Exit(2)
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		return nil, err
	}
	binaryPath := filepath.Join(directory, "inspect-core-helper")
	command := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binaryPath, sourcePath)
	if output, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build inspect core helper: %w: %s", err, strings.TrimSpace(string(output)))
	}
	helper, err := os.ReadFile(binaryPath)
	if err != nil {
		return nil, err
	}
	if len(helper) < 4 || string(helper[:4]) != "\x7fELF" {
		return nil, errors.New("inspect core helper is not ELF")
	}
	return helper, nil
}

func TestInspectExistingValidatesACopyOutsideTheSourceDirectory(t *testing.T) {
	root := t.TempDir()
	sourceDirectory := filepath.Join(root, "existing")
	if err := os.Mkdir(sourceDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(sourceDirectory, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(sourceDirectory, "xray")
	if err := os.WriteFile(binaryPath, inspectExistingCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	recordPath := binaryPath + ".checks.log"
	if err := os.WriteFile(recordPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(sourceDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := runUtilityCommand(map[core.Engine]agent.EngineSpec{
		core.EngineXray: {Binary: binaryPath, ConfigPath: configPath, Service: "xray.service"},
	}, []string{"inspect-existing", "xray"}); err != nil {
		t.Fatalf("inspect existing: %v", err)
	}
	after, err := os.ReadDir(sourceDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("inspection changed source directory: before=%d after=%d", len(before), len(after))
	}
	checks, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(checks), "args=run -test -config ") || strings.Contains(string(checks), "-config "+configPath) {
		t.Fatalf("Xray validation did not use the protected snapshot copy: %q", checks)
	}
}

func TestExistingSingBoxSpecCarriesConfigDirectoryAndServiceExecutable(t *testing.T) {
	t.Setenv("QCH_EXISTING_SING_BOX_BINARY", "/usr/lib/sing-box/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_CONFIG", "/etc/sing-box/config.json")
	t.Setenv("QCH_EXISTING_SING_BOX_CONFIG_DIRECTORY", "/etc/sing-box/conf.d")
	t.Setenv("QCH_EXISTING_SING_BOX_WORK_DIRECTORY", "/var/lib/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_SERVICE_BINARY", "/usr/local/bin/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_SERVICE", "sing-box.service")
	spec, ok := existingSpec("SING_BOX")
	if !ok {
		t.Fatal("complete sing-box directory mapping was not loaded")
	}
	want := agent.EngineSpec{
		Binary: "/usr/lib/sing-box/sing-box", ConfigPath: "/etc/sing-box/config.json",
		ConfigDirectory: "/etc/sing-box/conf.d", WorkingDirectory: "/var/lib/sing-box",
		ServiceBinary: "/usr/local/bin/sing-box",
		Service:       "sing-box.service",
	}
	if spec != want {
		t.Fatalf("existing sing-box spec = %+v, want %+v", spec, want)
	}
}

func TestExistingSingBoxSpecCapturesRelativeWorkingDirectory(t *testing.T) {
	t.Setenv("QCH_EXISTING_SING_BOX_BINARY", "/usr/bin/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_CONFIG", "/etc/sing-box/config.json")
	t.Setenv("QCH_EXISTING_SING_BOX_CONFIG_DIRECTORY", "/etc/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_WORK_DIRECTORY", "var/lib/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_SERVICE", "sing-box.service")
	spec, ok := existingSpec("SING_BOX")
	if !ok {
		t.Fatal("sing-box mapping was not loaded")
	}
	if spec.WorkingDirectory != "var/lib/sing-box" {
		t.Fatalf("relative sing-box working directory was not captured: %+v", spec)
	}
}

func TestInspectExistingOfficialSingBoxAllowsBoundedRelativeLogAndRejectsOtherRelativeResources(t *testing.T) {
	root := t.TempDir()
	workingDirectory := filepath.Join(root, "work")
	configDirectory := filepath.Join(root, "config")
	if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	fragmentPath := filepath.Join(configDirectory, "10-outbounds.json")
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragmentPath, []byte(`{"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(root, "sing-box")
	if err := os.WriteFile(binaryPath, inspectExistingCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	recordPath := binaryPath + ".checks.log"
	if err := os.WriteFile(recordPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QCH_SING_BOX_BINARY", binaryPath)
	t.Setenv("QCH_SING_BOX_CONFIG", configPath)
	t.Setenv("QCH_SING_BOX_CONFIG_DIRECTORY", configDirectory)
	t.Setenv("QCH_SING_BOX_WORK_DIRECTORY", workingDirectory)
	t.Setenv("QCH_SING_BOX_SERVICE_BINARY", binaryPath)
	t.Setenv("QCH_SING_BOX_SERVICE", "sing-box.service")
	spec := overrideSpec(agent.DefaultSpecs()[core.EngineSingBox], "SING_BOX")
	if spec.WorkingDirectory != workingDirectory {
		t.Fatalf("utility sing-box spec lost working directory: %+v", spec)
	}
	if err := runUtilityCommand(map[core.Engine]agent.EngineSpec{core.EngineSingBox: spec}, []string{"inspect-existing", "sing-box"}); err != nil {
		t.Fatalf("inspect official sing-box: %v", err)
	}
	checks, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "cwd=" + workingDirectory + " args=check -D " + workingDirectory + " -C " + configDirectory
	if !strings.Contains(string(checks), want) {
		t.Fatalf("official sing-box check did not preserve -D context: %q", checks)
	}
	if err := os.WriteFile(fragmentPath, []byte(`{"log":{"output":"relative.log"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runUtilityCommand(map[core.Engine]agent.EngineSpec{core.EngineSingBox: spec}, []string{"inspect-existing", "sing-box"}); err != nil {
		t.Fatalf("bounded relative official sing-box log output was rejected: %v", err)
	}
	if err := os.WriteFile(fragmentPath, []byte(`{"route":{"rule_set":[{"type":"local","tag":"blocked","path":"relative.srs"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runUtilityCommand(map[core.Engine]agent.EngineSpec{core.EngineSingBox: spec}, []string{"inspect-existing", "sing-box"}); err == nil || !strings.Contains(err.Error(), "cannot be migrated safely") {
		t.Fatalf("unrelated relative official sing-box resource was accepted: %v", err)
	}
}

func TestAgentStartupRefreshesAutomaticExistingCoreDiscovery(t *testing.T) {
	contents, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	mainSource := string(contents)
	for _, required := range []string{
		"manualExistingSpecs", "RefreshExistingCoreDiscovery", `statePath+".existing-cores"`,
		"ExistingDiscoveryIssues: discoveryIssues", "MigrationMarkerPrefix:",
	} {
		if !strings.Contains(mainSource, required) {
			t.Errorf("Agent startup discovery is missing %q", required)
		}
	}
}
