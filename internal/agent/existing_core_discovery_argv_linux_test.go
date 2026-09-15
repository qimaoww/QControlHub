//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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

func TestValidateExistingSpecPathsRejectsRelativeSingBoxWorkingDirectory(t *testing.T) {
	spec := EngineSpec{
		Binary: "/usr/bin/sing-box", ConfigPath: "/etc/sing-box/config.json",
		ConfigDirectory: "/etc/sing-box", WorkingDirectory: "var/lib/sing-box",
	}
	if err := validateExistingSpecPaths(core.EngineSingBox, spec); err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("relative sing-box working directory was not rejected: %v", err)
	}
}
