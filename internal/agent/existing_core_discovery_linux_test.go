//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const existingDiscoveryCoreHelperName = "existing-discovery-core"

var existingDiscoveryCoreHelper []byte

func TestMain(tests *testing.M) {
	helper, err := buildExistingDiscoveryCoreHelper()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	existingDiscoveryCoreHelper = helper
	os.Exit(tests.Run())
}

func buildExistingDiscoveryCoreHelper() ([]byte, error) {
	directory, err := os.MkdirTemp("", ".qcontrolhub-existing-discovery-core-")
	if err != nil {
		return nil, fmt.Errorf("create discovery core helper directory: %w", err)
	}
	defer os.RemoveAll(directory)
	sourcePath := filepath.Join(directory, "main.go")
	const source = `package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	arguments := os.Args[1:]
	if len(arguments) != 3 && len(arguments) != 5 {
		os.Exit(2)
	}
	if arguments[0] != "check" {
		os.Exit(2)
	}
	if arguments[1] == "-C" {
		if len(arguments) != 3 || !filepath.IsAbs(arguments[2]) {
			os.Exit(2)
		}
		info, err := os.Stat(arguments[2])
		if err != nil || !info.IsDir() {
			os.Exit(1)
		}
		entries, err := os.ReadDir(arguments[2])
		if err != nil {
			os.Exit(1)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			contents, err := os.ReadFile(filepath.Join(arguments[2], entry.Name()))
			if err != nil || !json.Valid(contents) {
				os.Exit(1)
			}
		}
		return
	}
	if arguments[1] == "-D" {
		if len(arguments) != 5 || arguments[3] != "-C" || !filepath.IsAbs(arguments[2]) || !filepath.IsAbs(arguments[4]) {
			os.Exit(2)
		}
		if outputPath := os.Getenv("QCH_TEST_ARGS_OUT"); outputPath != "" {
			_ = os.WriteFile(outputPath, []byte(strings.Join(arguments, "\x00")), 0o600)
		}
		info, err := os.Stat(arguments[2])
		if err != nil || !info.IsDir() {
			os.Exit(1)
		}
		entries, err := os.ReadDir(arguments[4])
		if err != nil {
			os.Exit(1)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			contents, err := os.ReadFile(filepath.Join(arguments[4], entry.Name()))
			if err != nil || !json.Valid(contents) {
				os.Exit(1)
			}
		}
		return
	}
	if arguments[1] != "-c" || !filepath.IsAbs(arguments[2]) {
		os.Exit(2)
	}
	contents, err := os.ReadFile(arguments[2])
	if err != nil || !json.Valid(contents) {
		os.Exit(1)
	}
	if len(arguments) == 5 {
		if arguments[3] != "-C" || !filepath.IsAbs(arguments[4]) {
			os.Exit(2)
		}
		info, err := os.Stat(arguments[4])
		if err != nil || !info.IsDir() {
			os.Exit(1)
		}
	}
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		return nil, fmt.Errorf("write discovery core helper source: %w", err)
	}
	binaryPath := filepath.Join(directory, existingDiscoveryCoreHelperName)
	command := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binaryPath, sourcePath)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("build discovery core helper: %w: %s", err, strings.TrimSpace(string(output)))
	}
	helper, err := os.ReadFile(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("read discovery core helper: %w", err)
	}
	if len(helper) < 4 || string(helper[:4]) != "\x7fELF" {
		return nil, errors.New("discovery core helper is not an ELF executable")
	}
	return helper, nil
}

func TestExistingCoreDiscoveryFindsAndRefreshesAfterAgentRestart(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("initial discovery after upgraded Agent restart: %v", err)
	}
	assertDiscoveredSingBoxSpec(t, specs[core.EngineSingBox], fixture.realBinary, fixture.serviceBinary, fixture.configPath, fixture.configDirectory, "")
	if len(issues) != 0 {
		t.Fatalf("initial discovery issues = %+v", issues)
	}
	info, err := os.Stat(fixture.discoveryStatePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("discovery state mode = %v, %v", info, err)
	}

	refreshedDirectory := filepath.Join(fixture.root, "refreshed-conf.d")
	if err := os.Mkdir(refreshedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refreshedDirectory, "20-route.json"), []byte(`{"route":{"final":"direct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.writeExecStart(t, "sing-box.service", systemdExecStart(
		fixture.serviceBinary,
		fixture.serviceBinary+" run -c "+fixture.configPath+" -C "+refreshedDirectory,
	))
	specs, issues, err = RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("refresh discovery after restart: %v", err)
	}
	assertDiscoveredSingBoxSpec(t, specs[core.EngineSingBox], fixture.realBinary, fixture.serviceBinary, fixture.configPath, refreshedDirectory, "")
	if len(issues) != 0 {
		t.Fatalf("refreshed discovery issues = %+v", issues)
	}
	state, err := loadExistingCoreDiscoveryState(fixture.discoveryStatePath)
	if err != nil {
		t.Fatalf("reload refreshed discovery state: %v", err)
	}
	if got := state.Specs[core.EngineSingBox].ConfigDirectory; got != refreshedDirectory {
		t.Fatalf("persisted refreshed config directory = %q", got)
	}
}

func TestManagedCoreUnitPolicyMatchesProjectUnits(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate discovery test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../.."))
	for engine, fileName := range map[core.Engine]string{
		core.EngineXray:    "qagent-xray.service",
		core.EngineSingBox: "qagent-sing-box.service",
	} {
		spec := DefaultSpecs()[engine]
		contents, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "systemd", fileName))
		if err != nil {
			t.Fatal(err)
		}
		actual := make([]string, 0)
		for _, line := range strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n") {
			if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, ";") {
				actual = append(actual, line)
			}
		}
		expected := managedCoreUnitLines(engine, spec)
		if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
			t.Fatalf("%s project unit does not match the supported execution policy", engine)
		}
	}
}

func TestExistingCoreDiscoverySupportsProtectedEtcSingBoxBinaryLayout(t *testing.T) {
	if candidates := existingDiscoveryCandidates[core.EngineSingBox]; !stringInSlice(protectedEtcSingBoxExecutable, candidates.executables) ||
		!stringInSlice(protectedEtcSingBoxExecutable, candidates.directExecutables) {
		t.Fatalf("production sing-box candidates do not include protected direct layout: %+v", candidates)
	}

	fixture := newExistingCoreDiscoveryFixture(t)
	serviceBinary := fixture.useDirectSingBoxBinary(t, -1)
	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("discover protected /etc-style sing-box binary: %v", err)
	}
	spec := specs[core.EngineSingBox]
	assertDiscoveredSingBoxSpec(t, spec, serviceBinary, serviceBinary, fixture.configPath, fixture.configDirectory, "")
	if len(issues) != 0 {
		t.Fatalf("protected /etc-style discovery issues = %+v", issues)
	}
	managed := fixture.managedSpecs[core.EngineSingBox]
	managed.ConfigPath = filepath.Join(fixture.root, "validated-merged-config.json")
	content, err := (&Executor{}).readExistingConfig(context.Background(), core.EngineSingBox, managed, spec)
	if err != nil {
		t.Fatalf("read protected /etc-style merged snapshot: %v", err)
	}
	if !strings.Contains(content, `"inbounds"`) || !strings.Contains(content, `"outbounds"`) {
		t.Fatalf("merged /etc-style sing-box snapshot omitted a source: %s", content)
	}
}

func TestExistingCoreDiscoverySupportsOfficialSingBoxWorkingDirectoryArgv(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	workDirectory := filepath.Join(fixture.root, "work")
	configDirectory := filepath.Join(fixture.root, "etc-sing-box")
	for _, directory := range []string{workDirectory, configDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(configDirectory, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "10-outbounds.json"), []byte(`{"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	existingDiscoveryCandidates[core.EngineSingBox] = existingDiscoveryCandidateSet{
		services:    []string{"sing-box.service", "singbox.service"},
		executables: []string{fixture.serviceBinary},
		configs:     []string{configPath},
	}
	official := fixture.serviceBinary + " -D " + workDirectory + " -C " + configDirectory + " run"
	fixture.writeExecStart(t, "sing-box.service", systemdExecStart(fixture.serviceBinary, official))
	fixture.writeExecStart(t, "singbox.service", systemdExecStart(fixture.serviceBinary, official))

	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("discover official sing-box argv: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("official sing-box discovery issues = %+v", issues)
	}
	assertDiscoveredSingBoxSpec(t, specs[core.EngineSingBox], fixture.realBinary, fixture.serviceBinary, configPath, configDirectory, workDirectory)

	managed := fixture.managedSpecs[core.EngineSingBox]
	managedConfigDirectory := filepath.Join(fixture.root, "managed-config")
	if err := os.Mkdir(managedConfigDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	managed.ConfigPath = filepath.Join(managedConfigDirectory, "config.json")
	content, err := (&Executor{}).readExistingConfig(context.Background(), core.EngineSingBox, managed, specs[core.EngineSingBox])
	if err != nil {
		t.Fatalf("read official sing-box merged snapshot: %v", err)
	}
	if !strings.Contains(content, `"inbounds"`) || !strings.Contains(content, `"outbounds"`) {
		t.Fatalf("official sing-box merged snapshot omitted a source: %s", content)
	}
}

func TestValidateExistingSourceInvocationPassesOriginalWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	config := filepath.Join(root, "config")
	for _, directory := range []string{work, config} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	recorded := filepath.Join(root, "args")
	script := filepath.Join(root, "record-check")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + recorded + "\"\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	spec := EngineSpec{
		Binary: script, ConfigPath: filepath.Join(config, "config.json"),
		ConfigDirectory: config, WorkingDirectory: work,
	}
	if err := validateExistingSourceInvocation(context.Background(), core.EngineSingBox, spec); err != nil {
		t.Fatalf("validateExistingSourceInvocation error: %v", err)
	}
	value, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatalf("read recorded source check arguments: %v", err)
	}
	args := strings.Fields(string(value))
	if len(args) != 5 || args[0] != "check" || args[1] != "-D" ||
		args[2] != work || args[3] != "-C" || args[4] != config {
		t.Fatalf("source check did not receive the original -D context: %q", args)
	}
}

func TestExistingCoreDiscoveryRejectsOfficialSingBoxRelativeResource(t *testing.T) {
	tests := map[string]string{
		"log output":                    `{"log":{"output":"relative.log"}}`,
		"local rule set":                `{"route":{"rule_set":[{"type":"local","tag":"geo","format":"binary","path":"ruleset.srs"}]}}`,
		"clash external ui":             `{"experimental":{"clash_api":{"external_ui":"dashboard"}}}`,
		"acme data directory":           `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","acme":{"data_directory":"acme-data"}}}]}`,
		"client cert path array":        `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","client_certificate_path":["client.pem"]}}]}`,
		"ssh private key path":          `{"outbounds":[{"type":"ssh","server":"example.com","server_port":22,"user":"root","private_key_path":"id_ed25519"}]}`,
		"tor data directory":            `{"outbounds":[{"type":"tor","server":"127.0.0.1","server_port":9050,"data_directory":"tor-data"}]}`,
		"outbound ech config path":      `{"outbounds":[{"type":"vless","server":"example.com","server_port":443,"uuid":"abc","tls":{"enabled":true,"ech":{"config_path":"ech.json"}}}]}`,
		"certificate directory":         `{"certificate":{"certificate_directory_path":["certs"]}}`,
		"tailscale state dir":           `{"endpoints":[{"type":"tailscale","tag":"ts","state_directory":"state"}]}`,
		"ccm credential path":           `{"services":[{"type":"ccm","tag":"ccm","credential_path":"creds.json"}]}`,
		"derp config path":              `{"services":[{"type":"derp","tag":"derp","config_path":"derp.json"}]}`,
		"hysteria2 masquerade dir":      `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":{"type":"file","directory":"webroot"}}]}`,
		"hysteria2 masquerade file url": `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":"file:webroot"}]}`,
	}
	for name, fragment := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newExistingCoreDiscoveryFixture(t)
			workDirectory := filepath.Join(fixture.root, "work")
			configDirectory := filepath.Join(fixture.root, "etc-sing-box")
			for _, directory := range []string{workDirectory, configDirectory} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			configPath := filepath.Join(configDirectory, "config.json")
			if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(configDirectory, "10-resource.json"), []byte(fragment), 0o600); err != nil {
				t.Fatal(err)
			}
			existingDiscoveryCandidates[core.EngineSingBox] = existingDiscoveryCandidateSet{
				services:    []string{"sing-box.service", "singbox.service"},
				executables: []string{fixture.serviceBinary},
				configs:     []string{configPath},
			}
			official := fixture.serviceBinary + " -D " + workDirectory + " -C " + configDirectory + " run"
			fixture.writeExecStart(t, "sing-box.service", systemdExecStart(fixture.serviceBinary, official))
			fixture.writeExecStart(t, "singbox.service", systemdExecStart(fixture.serviceBinary, official))

			specs, issues, err := RefreshExistingCoreDiscovery(
				context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
				fixture.managedSpecs, nil,
			)
			if err != nil {
				t.Fatalf("reject official sing-box relative resource: %v", err)
			}
			if len(specs) != 0 || issues[core.EngineSingBox] == "" {
				t.Fatalf("relative official sing-box resource was not rejected: specs=%+v issues=%+v", specs, issues)
			}
		})
	}
}

func TestExistingCoreDiscoveryAcceptsOfficialSingBoxAbsoluteLocalResource(t *testing.T) {
	tests := map[string]string{
		"absolute local rule set path":           `{"route":{"rule_set":[{"type":"local","tag":"geo","format":"binary","path":"/etc/sing-box/ruleset.srs"}]}}`,
		"absolute clash external ui":             `{"experimental":{"clash_api":{"external_ui":"/srv/sing-box/dashboard"}}}`,
		"absolute acme data directory":           `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","acme":{"data_directory":"/var/lib/acme"}}}]}`,
		"absolute client cert path array":        `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","client_certificate_path":["/etc/client.pem"]}}]}`,
		"absolute ssh private key path":          `{"outbounds":[{"type":"ssh","server":"example.com","server_port":22,"user":"root","private_key_path":"/etc/sing-box/id_ed25519"}]}`,
		"absolute tor data directory":            `{"outbounds":[{"type":"tor","server":"127.0.0.1","server_port":9050,"data_directory":"/var/lib/tor"}]}`,
		"absolute outbound ech config path":      `{"outbounds":[{"type":"vless","server":"example.com","server_port":443,"uuid":"abc","tls":{"enabled":true,"ech":{"config_path":"/etc/sing-box/ech.json"}}}]}`,
		"absolute certificate directory":         `{"certificate":{"certificate_directory_path":["/etc/sing-box/certs"]}}`,
		"absolute tailscale state dir":           `{"endpoints":[{"type":"tailscale","tag":"ts","state_directory":"/var/lib/tailscale"}]}`,
		"absolute ccm credential path":           `{"services":[{"type":"ccm","tag":"ccm","credential_path":"/etc/sing-box/creds.json"}]}`,
		"absolute derp config path":              `{"services":[{"type":"derp","tag":"derp","config_path":"/etc/sing-box/derp.json"}]}`,
		"absolute hysteria2 masquerade dir":      `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":{"type":"file","directory":"/var/www/sing-box"}}]}`,
		"absolute hysteria2 masquerade file url": `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":"file:///var/www/sing-box"}]}`,
	}
	for name, fragment := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newExistingCoreDiscoveryFixture(t)
			workDirectory := filepath.Join(fixture.root, "work")
			configDirectory := filepath.Join(fixture.root, "etc-sing-box")
			for _, directory := range []string{workDirectory, configDirectory} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			configPath := filepath.Join(configDirectory, "config.json")
			if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(configDirectory, "10-resource.json"), []byte(fragment), 0o600); err != nil {
				t.Fatal(err)
			}
			existingDiscoveryCandidates[core.EngineSingBox] = existingDiscoveryCandidateSet{
				services:    []string{"sing-box.service", "singbox.service"},
				executables: []string{fixture.serviceBinary},
				configs:     []string{configPath},
			}
			official := fixture.serviceBinary + " -D " + workDirectory + " -C " + configDirectory + " run"
			fixture.writeExecStart(t, "sing-box.service", systemdExecStart(fixture.serviceBinary, official))
			fixture.writeExecStart(t, "singbox.service", systemdExecStart(fixture.serviceBinary, official))

			specs, issues, err := RefreshExistingCoreDiscovery(
				context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
				fixture.managedSpecs, nil,
			)
			if err != nil {
				t.Fatalf("accept official sing-box absolute resource: %v", err)
			}
			if len(issues) != 0 || specs[core.EngineSingBox].Service != "sing-box.service" {
				t.Fatalf("absolute official sing-box resource was rejected: specs=%+v issues=%+v", specs, issues)
			}
		})
	}
}

func TestExistingCoreDiscoveryRejectsUnsupportedOfficialSingBoxArgv(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	workDirectory := filepath.Join(fixture.root, "work")
	configDirectory := filepath.Join(fixture.root, "etc-sing-box")
	for _, directory := range []string{workDirectory, configDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(configDirectory, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	existingDiscoveryCandidates[core.EngineSingBox] = existingDiscoveryCandidateSet{
		services:    []string{"sing-box.service", "singbox.service"},
		executables: []string{fixture.serviceBinary},
		configs:     []string{configPath},
	}
	variants := map[string]string{
		"relative working":  fixture.serviceBinary + " -D work -C " + configDirectory + " run",
		"relative config":   fixture.serviceBinary + " -D " + workDirectory + " -C etc-sing-box run",
		"missing run":       fixture.serviceBinary + " -D " + workDirectory + " -C " + configDirectory,
		"unknown flag":      fixture.serviceBinary + " -D " + workDirectory + " -C " + configDirectory + " run --unknown",
		"duplicate config":  fixture.serviceBinary + " -D " + workDirectory + " -C " + configDirectory + " -C " + configDirectory + " run",
		"repeated working":  fixture.serviceBinary + " -D " + workDirectory + " -D " + workDirectory + " -C " + configDirectory + " run",
		"wrong config file": fixture.serviceBinary + " -D " + workDirectory + " -C " + configDirectory + " run -c " + configPath,
		"wrapper command":   fixture.serviceBinary + " -D " + workDirectory + " -C " + configDirectory + " run; /bin/true",
	}
	for name, argv := range variants {
		t.Run(name, func(t *testing.T) {
			fixture.writeExecStart(t, "sing-box.service", systemdExecStart(fixture.serviceBinary, argv))
			fixture.writeExecStart(t, "singbox.service", systemdExecStart(fixture.serviceBinary, argv))
			specs, issues, err := RefreshExistingCoreDiscovery(
				context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
				fixture.managedSpecs, nil,
			)
			if err != nil {
				t.Fatalf("reject unsupported official sing-box argv: %v", err)
			}
			if len(specs) != 0 || issues[core.EngineSingBox] == "" {
				t.Fatalf("unsupported official sing-box argv was accepted: specs=%+v issues=%+v", specs, issues)
			}
		})
	}
}

func TestExistingCoreDiscoveryRejectsNonRootEtcSingBoxBinary(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreDiscoveryFixture(t)
	serviceBinary := fixture.useDirectSingBoxBinary(t, 65534)
	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("reject non-root /etc-style sing-box binary: %v", err)
	}
	reason := issues[core.EngineSingBox]
	if len(specs) != 0 || !strings.Contains(reason, "root 所有") || strings.Contains(reason, "不在受支持的标准路径") {
		t.Fatalf("non-root /etc-style discovery = specs %+v reason %q", specs, reason)
	}
	state, err := loadExistingCoreDiscoveryState(fixture.discoveryStatePath)
	if err != nil || len(state.Specs) != 0 || state.Issues[core.EngineSingBox] != reason {
		t.Fatalf("persisted non-root discovery state = %+v, %v", state, err)
	}
	executor := &Executor{
		Specs: fixture.managedSpecs,
		ExistingDiscoveryIssues: map[core.Engine]string{
			core.EngineSingBox: reason,
		},
	}
	runtime := executor.Runtime(context.Background())[core.EngineSingBox]
	if runtime.ExistingConfigAvailable || runtime.ExistingConfigUnsupportedReason != reason {
		t.Fatalf("non-root /etc-style runtime = %+v", runtime)
	}
	if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionReadConfig, Engine: core.EngineSingBox}); err == nil || !strings.Contains(err.Error(), "core tasks are disabled") {
		t.Fatalf("non-root /etc-style read-config error = %v", err)
	}
	if info, err := os.Lstat(serviceBinary); err != nil {
		t.Fatal(err)
	} else if uid, known := fileOwnerUID(info); !known || uid != 65534 {
		t.Fatalf("non-root executable fixture owner = %d, known=%v", uid, known)
	}
}

func TestExistingCoreDiscoveryRejectsSymlinkedEtcSingBoxBinary(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	serviceBinary := fixture.useDirectSingBoxBinary(t, -1)
	realBinary := serviceBinary + ".real"
	if err := os.Rename(serviceBinary, realBinary); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realBinary, serviceBinary); err != nil {
		t.Fatal(err)
	}
	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("reject symlinked /etc-style sing-box binary: %v", err)
	}
	reason := issues[core.EngineSingBox]
	if len(specs) != 0 || !strings.Contains(reason, "非符号链接") {
		t.Fatalf("symlinked /etc-style discovery = specs %+v reason %q", specs, reason)
	}
}

func TestExistingCoreDiscoveryClearsStateWhenNoCandidateRemains(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	if _, _, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil); err != nil {
		t.Fatal(err)
	}
	fixture.writeStatus(t, "sing-box.service", "inactive")
	specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil {
		t.Fatalf("clear absent discovery: %v", err)
	}
	if len(specs) != 0 || len(issues) != 0 {
		t.Fatalf("absent discovery = specs %+v issues %+v", specs, issues)
	}
	state, err := loadExistingCoreDiscoveryState(fixture.discoveryStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Specs) != 0 || len(state.Issues) != 0 {
		t.Fatalf("stale persisted discovery = %+v", state)
	}
}

func TestExistingCoreDiscoveryKeepsMappingForInterruptedMigrationRecovery(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	specs, _, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := specs[core.EngineSingBox]
	if err := writeCoreMigrationMarker(
		fixture.markerPrefix, core.EngineSingBox, coreMigrationInProgress,
		coreMigrationConfigDigest(`{"inbounds":[]}`), coreMigrationSourceDigest(spec), "enabled", "disabled",
	); err != nil {
		t.Fatal(err)
	}
	fixture.writeStatus(t, "sing-box.service", "inactive")
	specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil {
		t.Fatalf("retain interrupted discovery: %v", err)
	}
	if specs[core.EngineSingBox] != spec || len(issues) != 0 {
		t.Fatalf("interrupted discovery = specs %+v issues %+v", specs, issues)
	}
}

func TestExistingCoreDiscoveryKeepsCompletedMappingForRestartSafetyGate(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	specs, _, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := specs[core.EngineSingBox]
	if err := writeCoreMigrationMarker(
		fixture.markerPrefix, core.EngineSingBox, coreMigrationComplete,
		coreMigrationConfigDigest(`{"inbounds":[]}`), coreMigrationSourceDigest(spec), "enabled", "disabled",
	); err != nil {
		t.Fatal(err)
	}
	fixture.writeStatus(t, "sing-box.service", "active")
	fixture.writeStatus(t, "qagent-sing-box.service", "inactive")
	fixture.writeEnableState(t, "sing-box.service", "disabled")
	fixture.writeEnableState(t, "qagent-sing-box.service", "enabled")
	specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil {
		t.Fatalf("retain completed discovery for restart gate: %v", err)
	}
	if specs[core.EngineSingBox] != spec || len(issues) != 0 {
		t.Fatalf("completed discovery = specs %+v issues %+v", specs, issues)
	}
	executor := &Executor{
		Specs: fixture.managedSpecs, ExistingSpecs: specs, ExistingDiscoveryIssues: issues,
		MigrationMarkerPrefix: fixture.markerPrefix,
	}
	if err := executor.LoadCoreMigrationState(); err != nil {
		t.Fatalf("load completed automatic discovery: %v", err)
	}
	if _, pending := executor.ExistingSpecs[core.EngineSingBox]; !pending {
		t.Fatal("unsafe automatic discovery mapping was suppressed")
	}
	if issue := executor.ExistingDiscoveryIssues[core.EngineSingBox]; !strings.Contains(issue, "迁移状态不再安全") {
		t.Fatalf("automatic restart safety issue = %q", issue)
	}
	if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionStart, Engine: core.EngineSingBox}); err == nil || !strings.Contains(err.Error(), "core tasks are disabled") {
		t.Fatalf("automatic restart start-task error = %v", err)
	}
}

func TestExistingCoreDiscoveryReportsAmbiguousAndUnsupportedServices(t *testing.T) {
	t.Run("ambiguous", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		fixture.writeStatus(t, "singbox.service", "active")
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "多个活动") {
			t.Fatalf("ambiguous discovery = specs %+v issues %+v", specs, issues)
		}
	})

	t.Run("unsupported wrapper", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		wrapper := filepath.Join(fixture.root, "complex-wrapper")
		contents := fmt.Sprintf("#!/bin/sh\nset -eu\nexport FOO=bar\nexec %s \"$@\"\n# extra\n", fixture.realBinary)
		if err := os.WriteFile(wrapper, []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(fixture.serviceBinary); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(wrapper, fixture.serviceBinary); err != nil {
			t.Fatal(err)
		}
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		reason := issues[core.EngineSingBox]
		if len(specs) != 0 || !strings.Contains(reason, "wrapper 不在安全支持范围") {
			t.Fatalf("unsupported-wrapper discovery = specs %+v reason %q", specs, reason)
		}
		state, err := loadExistingCoreDiscoveryState(fixture.discoveryStatePath)
		if err != nil || state.Issues[core.EngineSingBox] != reason {
			t.Fatalf("persisted unsupported-wrapper issue = %+v, %v", state.Issues, err)
		}
		runtime := (&Executor{
			Specs:                   fixture.managedSpecs,
			ExistingDiscoveryIssues: map[core.Engine]string{core.EngineSingBox: reason},
		}).Runtime(context.Background())[core.EngineSingBox]
		if runtime.Installed || runtime.ServiceStatus != "active" || runtime.ExistingConfigUnsupportedReason != reason || runtime.ExistingConfigAvailable {
			t.Fatalf("unsupported-wrapper runtime = %+v", runtime)
		}
	})

	t.Run("custom managed unit", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		unitPath := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service")
		if err := os.WriteFile(unitPath, []byte("[Unit]\nDescription=administrator unit\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "QAgent 专用服务") {
			t.Fatalf("custom managed unit discovery = specs %+v issues %+v", specs, issues)
		}
	})

	t.Run("active managed unit", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		fixture.writeStatus(t, "qagent-sing-box.service", "active")
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 0 || specs[core.EngineSingBox].Service != "sing-box.service" {
			t.Fatalf("safe active managed unit discovery = specs %+v issues %+v", specs, issues)
		}
		activeState, readErr := os.ReadFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.active"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if got := strings.TrimSpace(string(activeState)); got != "active" {
			t.Fatalf("discovery mutated active managed unit to %q", got)
		}
	})

	t.Run("active managed ExecStart drift", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		fixture.writeStatus(t, "qagent-sing-box.service", "active")
		if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.managed-exec-start"), []byte(systemdExecStart(
			DefaultSpecs()[core.EngineSingBox].Binary,
			DefaultSpecs()[core.EngineSingBox].Binary+" run -c /etc/qagent/sing-box/changed.json",
		)), 0o600); err != nil {
			t.Fatal(err)
		}
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "安全 unit") {
			t.Fatalf("drifted active managed unit discovery = specs %+v issues %+v", specs, issues)
		}
	})

	t.Run("effective managed identity and hooks", func(t *testing.T) {
		tests := map[string]func(existingCoreDiscoveryFixture) error{
			"user override": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.user"), []byte("root\n"), 0o600)
			},
			"start hook": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.ExecStartPre"), []byte("/bin/true\n"), 0o600)
			},
			"comment marker": func(fixture existingCoreDiscoveryFixture) error {
				unitPath := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service")
				return os.WriteFile(unitPath, []byte("[Unit]\n#Description=sing-box core managed by QAgent\n[Service]\nUser=qcontrolhub-core\nGroup=qcontrolhub-core\nExecStart="+DefaultSpecs()[core.EngineSingBox].Binary+" run -c "+DefaultSpecs()[core.EngineSingBox].ConfigPath+"\n"), 0o600)
			},
			"duplicate marker": func(fixture existingCoreDiscoveryFixture) error {
				unitPath := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service")
				return os.WriteFile(unitPath, []byte("[Unit]\nDescription=sing-box core managed by QAgent\nDescription=sing-box core managed by QAgent\n[Service]\nUser=qcontrolhub-core\nGroup=qcontrolhub-core\nExecStart="+DefaultSpecs()[core.EngineSingBox].Binary+" run -c "+DefaultSpecs()[core.EngineSingBox].ConfigPath+"\n"), 0o600)
			},
		}
		for name, mutate := range tests {
			t.Run(name, func(t *testing.T) {
				fixture := newExistingCoreDiscoveryFixture(t)
				fixture.writeStatus(t, "qagent-sing-box.service", "active")
				if err := mutate(fixture); err != nil {
					t.Fatal(err)
				}
				specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "安全 unit") {
					t.Fatalf("unsafe effective managed unit discovery = specs %+v issues %+v", specs, issues)
				}
			})
		}
	})

	t.Run("effective execution context and drop-ins", func(t *testing.T) {
		tests := map[string]func(existingCoreDiscoveryFixture) error{
			"root directory": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.RootDirectory"), []byte("/sandbox\n"), 0o600)
			},
			"bind read-only paths": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.BindReadOnlyPaths"), []byte("/source:/target\n"), 0o600)
			},
			"environment": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.Environment"), []byte("QCH_UNEXPECTED=1\n"), 0o600)
			},
			"environment file": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.EnvironmentFiles"), []byte("/etc/qagent/unexpected.env\n"), 0o600)
			},
			"working directory": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.WorkingDirectory"), []byte("/other\n"), 0o600)
			},
			"type": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.Type"), []byte("oneshot\n"), 0o600)
			},
			"unknown fragment context": func(fixture existingCoreDiscoveryFixture) error {
				unitPath := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service")
				contents, err := os.ReadFile(unitPath)
				if err != nil {
					return err
				}
				contents = []byte(strings.Replace(string(contents), "WorkingDirectory=/var/lib/qcontrolhub-sing-box\n", "RootDirectory=/sandbox\nWorkingDirectory=/var/lib/qcontrolhub-sing-box\n", 1))
				return os.WriteFile(unitPath, contents, 0o600)
			},
			"unknown drop-in": func(fixture existingCoreDiscoveryFixture) error {
				path := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service.d", "99-unknown.conf")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte("[Service]\nEnvironment=QCH_UNEXPECTED=1\n"), 0o600); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.DropInPaths"), []byte(path+"\n"), 0o600)
			},
			"modified project drop-in": func(fixture existingCoreDiscoveryFixture) error {
				path := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service.d", "10-qcontrolhub-bind-low-ports.conf")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte("[Service]\nEnvironment=QCH_UNEXPECTED=1\n"), 0o600); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.DropInPaths"), []byte(path+"\n"), 0o600)
			},
		}
		for name, mutate := range tests {
			t.Run(name, func(t *testing.T) {
				fixture := newExistingCoreDiscoveryFixture(t)
				fixture.writeStatus(t, "qagent-sing-box.service", "active")
				if err := mutate(fixture); err != nil {
					t.Fatal(err)
				}
				specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "安全 unit") {
					t.Fatalf("unsafe execution context discovery = specs %+v issues %+v", specs, issues)
				}
			})
		}
	})

	t.Run("project-managed capability and log drop-ins", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		fixture.writeStatus(t, "qagent-sing-box.service", "active")
		dropInDirectory := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service.d")
		if err := os.MkdirAll(dropInDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		paths := []string{
			filepath.Join(dropInDirectory, "10-qcontrolhub-bind-low-ports.conf"),
			filepath.Join(dropInDirectory, "20-qcontrolhub-volatile-logs.conf"),
		}
		contents := [][]byte{[]byte(managedCoreCapabilityDropIn), []byte(managedCoreLogDropIn)}
		for index, path := range paths {
			if err := os.WriteFile(path, contents[index], 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.DropInPaths"), []byte(strings.Join(paths, " ")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 0 || specs[core.EngineSingBox].Service != "sing-box.service" {
			t.Fatalf("project-managed drop-ins were rejected: specs=%+v issues=%+v", specs, issues)
		}
	})
}

func TestExistingCoreDiscoveryRejectsEnvironmentFileDropIn(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	fixture.writeStatus(t, "qagent-sing-box.service", "active")
	dropInPath := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service.d", "50-unexpected-env.conf")
	if err := os.MkdirAll(filepath.Dir(dropInPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dropInPath, []byte("[Service]\nEnvironmentFile=/etc/qagent/unexpected.env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// daemon-reload applies the drop-in, so systemd's effective EnvironmentFiles
	// property becomes non-empty even though the singular EnvironmentFile property
	// does not exist in systemd's D-Bus interface.
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.DropInPaths"), []byte(dropInPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.EnvironmentFiles"), []byte("/etc/qagent/unexpected.env\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "安全 unit") {
		t.Fatalf("EnvironmentFile= drop-in discovery = specs %+v issues %+v", specs, issues)
	}
}

func TestExistingCoreDiscoveryManualMappingWinsAndStatePermissionsFailClosed(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	manual := EngineSpec{Binary: "/manual/sing-box", ConfigPath: "/manual/config.json", Service: "sing-box.service"}
	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, map[core.Engine]EngineSpec{core.EngineSingBox: manual},
	)
	if err != nil {
		t.Fatal(err)
	}
	if specs[core.EngineSingBox] != manual || len(issues) != 0 {
		t.Fatalf("manual precedence = specs %+v issues %+v", specs, issues)
	}
	state, err := loadExistingCoreDiscoveryState(fixture.discoveryStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Specs) != 0 || len(state.Issues) != 0 {
		t.Fatalf("manual mapping was persisted as automatic state: %+v", state)
	}
	if err := os.Chmod(fixture.discoveryStatePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil); err == nil || !strings.Contains(err.Error(), "protected regular file") {
		t.Fatalf("unsafe persisted discovery permissions error = %v", err)
	}
}

type existingCoreDiscoveryFixture struct {
	root               string
	stateDirectory     string
	discoveryStatePath string
	markerPrefix       string
	realBinary         string
	serviceBinary      string
	configPath         string
	configDirectory    string
	managedSpecs       map[core.Engine]EngineSpec
}

func (fixture *existingCoreDiscoveryFixture) useDirectSingBoxBinary(t *testing.T, owner int) string {
	t.Helper()
	directory := filepath.Join(fixture.root, "etc", "sing-box", "bin")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	serviceBinary := filepath.Join(directory, "sing-box")
	if err := os.WriteFile(serviceBinary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	if owner >= 0 {
		if err := os.Chown(serviceBinary, owner, owner); err != nil {
			t.Fatal(err)
		}
	}
	fixture.realBinary = serviceBinary
	fixture.serviceBinary = serviceBinary
	existingDiscoveryCandidates[core.EngineSingBox] = existingDiscoveryCandidateSet{
		services:          []string{"sing-box.service", "singbox.service"},
		executables:       []string{serviceBinary},
		directExecutables: []string{serviceBinary},
		configs:           []string{fixture.configPath},
	}
	fixture.writeExecStart(t, "sing-box.service", systemdExecStart(
		serviceBinary,
		serviceBinary+" run -c "+fixture.configPath+" -C "+fixture.configDirectory,
	))
	fixture.writeExecStart(t, "singbox.service", systemdExecStart(
		serviceBinary,
		serviceBinary+" run -c "+fixture.configPath+" -C "+fixture.configDirectory,
	))
	return serviceBinary
}

func newExistingCoreDiscoveryFixture(t *testing.T) existingCoreDiscoveryFixture {
	t.Helper()
	root := t.TempDir()
	stateDirectory := filepath.Join(root, "systemctl")
	configDirectory := filepath.Join(root, "conf.d")
	managedDirectory := filepath.Join(root, "managed")
	managedUnitDirectory := filepath.Join(root, "managed-units")
	agentStateDirectory := filepath.Join(root, "agent-state")
	for _, directory := range []string{stateDirectory, configDirectory, managedDirectory, managedUnitDirectory, agentStateDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "10-outbounds.json"), []byte(`{"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(existingDiscoveryCoreHelper) == 0 {
		t.Fatal("discovery core helper was not built")
	}
	realBinary := filepath.Join(root, existingDiscoveryCoreHelperName)
	if err := os.WriteFile(realBinary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	serviceBinary := filepath.Join(root, "sing-box")
	if err := os.Symlink(realBinary, serviceBinary); err != nil {
		t.Fatal(err)
	}
	fakeSystemctl := filepath.Join(root, "fake-systemctl")
	script := "#!/bin/sh\nset -eu\nstate=" + shellQuote(stateDirectory) + "\ncommand=$1\nshift\nservice=$1\nshift\ncase \"$command\" in\n  is-active) value=$(cat \"$state/$service.active\"); printf '%s\\n' \"$value\"; [ \"$value\" = active ] ;;\n  is-enabled) value=$(cat \"$state/$service.enabled\"); printf '%s\\n' \"$value\"; [ \"$value\" = enabled ] ;;\n  show) property=ExecStart; for argument in \"$@\"; do case \"$argument\" in --property=*) property=${argument#--property=} ;; esac; done; case \"$property\" in ExecStart) if [ \"$service\" = qagent-sing-box.service ]; then cat \"$state/$service.managed-exec-start\"; else cat \"$state/$service.exec-start\"; fi ;; LoadState) cat \"$state/$service.load-state\" ;; FragmentPath) cat \"$state/$service.fragment-path\" ;; Description|User|Group) cat \"$state/$service.$(printf '%s' \"$property\" | tr '[:upper:]' '[:lower:]')\" ;; Type|WorkingDirectory|RootDirectory|RootImage|BindPaths|BindReadOnlyPaths|Environment|EnvironmentFiles|DropInPaths) cat \"$state/$service.$property\" ;; ExecCondition|ExecStartPre|ExecStartPost|ExecReload|ExecStop|ExecStopPost) cat \"$state/$service.$property\" ;; *) exit 1 ;; esac ;;\n  *) exit 1 ;;\nesac\n"
	if err := os.WriteFile(fakeSystemctl, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previousSystemctl := systemctlPath
	previousCandidates := existingDiscoveryCandidates
	previousManagedUnitRoot := existingDiscoveryManagedUnitRoot
	systemctlPath = fakeSystemctl
	existingDiscoveryManagedUnitRoot = managedUnitDirectory
	existingDiscoveryCandidates = map[core.Engine]existingDiscoveryCandidateSet{
		core.EngineSingBox: {
			services:    []string{"sing-box.service", "singbox.service"},
			executables: []string{serviceBinary},
			configs:     []string{configPath},
		},
	}
	t.Cleanup(func() {
		systemctlPath = previousSystemctl
		existingDiscoveryCandidates = previousCandidates
		existingDiscoveryManagedUnitRoot = previousManagedUnitRoot
	})
	fixture := existingCoreDiscoveryFixture{
		root: root, stateDirectory: stateDirectory,
		discoveryStatePath: filepath.Join(agentStateDirectory, "agent-state.json.existing-cores"),
		markerPrefix:       filepath.Join(agentStateDirectory, "agent-state.json.core-migration"),
		realBinary:         realBinary, serviceBinary: serviceBinary,
		configPath: configPath, configDirectory: configDirectory,
		managedSpecs: map[core.Engine]EngineSpec{core.EngineSingBox: DefaultSpecs()[core.EngineSingBox]},
	}
	managedUnitPath := filepath.Join(managedUnitDirectory, "qagent-sing-box.service")
	managedSpec := DefaultSpecs()[core.EngineSingBox]
	managedUnitContents := strings.Join(managedCoreUnitLines(core.EngineSingBox, managedSpec), "\n") + "\n"
	if err := os.WriteFile(managedUnitPath, []byte(managedUnitContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service.load-state"), []byte("loaded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service.fragment-path"), []byte(managedUnitPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service.managed-exec-start"), []byte(systemdExecStart(
		DefaultSpecs()[core.EngineSingBox].Binary,
		DefaultSpecs()[core.EngineSingBox].Binary+" run -c "+DefaultSpecs()[core.EngineSingBox].ConfigPath,
	)), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"description": "sing-box core managed by QAgent\n",
		"user":        "qcontrolhub-core\n",
		"group":       "qcontrolhub-core\n",
	} {
		if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service."+name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, hook := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecReload", "ExecStop", "ExecStopPost"} {
		if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service."+hook), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for property, value := range map[string]string{
		"Type":              "simple\n",
		"WorkingDirectory":  "/var/lib/qcontrolhub-sing-box\n",
		"RootDirectory":     "\n",
		"RootImage":         "\n",
		"BindPaths":         "\n",
		"BindReadOnlyPaths": "\n",
		"Environment":       "\n",
		"EnvironmentFiles":  "\n",
		"DropInPaths":       "\n",
	} {
		if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service."+property), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture.writeStatus(t, "sing-box.service", "active")
	fixture.writeStatus(t, "singbox.service", "inactive")
	fixture.writeStatus(t, "qagent-sing-box.service", "inactive")
	fixture.writeEnableState(t, "sing-box.service", "enabled")
	fixture.writeEnableState(t, "singbox.service", "disabled")
	fixture.writeEnableState(t, "qagent-sing-box.service", "disabled")
	fixture.writeExecStart(t, "sing-box.service", systemdExecStart(
		serviceBinary,
		serviceBinary+" run -c "+configPath+" -C "+configDirectory,
	))
	fixture.writeExecStart(t, "singbox.service", systemdExecStart(
		serviceBinary,
		serviceBinary+" run -c "+configPath+" -C "+configDirectory,
	))
	return fixture
}

func (fixture existingCoreDiscoveryFixture) writeStatus(t *testing.T, service, status string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, service+".active"), []byte(status+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture existingCoreDiscoveryFixture) writeEnableState(t *testing.T, service, state string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, service+".enabled"), []byte(state+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture existingCoreDiscoveryFixture) writeExecStart(t *testing.T, service, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, service+".exec-start"), []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertDiscoveredSingBoxSpec(t *testing.T, spec EngineSpec, realBinary, serviceBinary, configPath, configDirectory, workDirectory string) {
	t.Helper()
	if spec.Binary != realBinary || spec.ServiceBinary != serviceBinary || spec.ConfigPath != configPath ||
		spec.ConfigDirectory != configDirectory || spec.WorkingDirectory != workDirectory || spec.Service != "sing-box.service" {
		t.Fatalf("discovered sing-box spec = %+v", spec)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestValidateExistingSpecPathsRejectsRelativeSingBoxWorkingDirectory(t *testing.T) {
	spec := EngineSpec{
		Binary: "/usr/bin/sing-box", ConfigPath: "/etc/sing-box/config.json",
		ConfigDirectory: "/etc/sing-box", WorkingDirectory: "var/lib/sing-box",
	}
	if err := validateExistingSpecPaths(core.EngineSingBox, spec); err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("relative sing-box working directory was not rejected: %v", err)
	}
}
