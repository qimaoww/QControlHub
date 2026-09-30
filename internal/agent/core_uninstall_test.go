//go:build linux

package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestUninstallManagedCoreStopsDisablesAndRemovesBinary(t *testing.T) {
	root := t.TempDir()
	coreDir := filepath.Join(root, "cores")
	configDir := filepath.Join(root, "config")
	for _, directory := range []string{coreDir, configDir} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(coreDir, "mihomo")
	config := filepath.Join(configDir, "config.yaml")
	configBackup := config + ".bak-20260929T000000Z-012345abcdef"
	coreBackups := []string{
		filepath.Join(coreDir, "mihomo.bak-20260927T000000Z-012345abcdef"),
		filepath.Join(coreDir, "mihomo.bak-20260928T000000Z-fedcba987654"),
	}
	otherCoreBackup := filepath.Join(coreDir, "xray.bak-20260928T000000Z-012345abcdef")
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, backup := range append(coreBackups, otherCoreBackup) {
		if err := os.WriteFile(backup, []byte("old binary"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(config, []byte("config retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configBackup, []byte("config backup retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "service.state")
	log := filepath.Join(root, "service.log")
	failEnable := filepath.Join(root, "fail-enable")
	if err := os.WriteFile(state, []byte("active\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := filepath.Join(root, "systemctl")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n" +
		"case \"$1\" in\n" +
		"  is-active) /bin/cat '" + state + "' ;;\n" +
		"  stop) /usr/bin/printf 'inactive\\n' > '" + state + "' ;;\n" +
		"  restart) /usr/bin/printf 'active\\n' > '" + state + "' ;;\n" +
		"  enable) if [ -e '" + failEnable + "' ]; then exit 1; fi ;;\n" +
		"esac\n"
	if err := os.WriteFile(service, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{
		Specs: map[core.Engine]EngineSpec{core.EngineMihomo: {
			Binary: binary, ConfigPath: config, Service: "qagent-mihomo.service",
		}},
		Services: &ServiceManager{kind: ServiceManagerSystemd, executable: service, enableExecutable: service},
	}
	for range 2 { // A retry after removal is safe and stays successful.
		if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionUninstall, Engine: core.EngineMihomo}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(binary); !os.IsNotExist(err) {
		t.Fatalf("binary still exists: %v", err)
	}
	for _, backup := range coreBackups {
		if _, err := os.Lstat(backup); !os.IsNotExist(err) {
			t.Fatalf("managed core backup still exists: %s: %v", backup, err)
		}
	}
	if _, err := os.Stat(otherCoreBackup); err != nil {
		t.Fatalf("another core's backup was removed: %v", err)
	}
	if body, err := os.ReadFile(config); err != nil || string(body) != "config retained" {
		t.Fatalf("managed configuration changed: %q, %v", body, err)
	}
	if body, err := os.ReadFile(configBackup); err != nil || string(body) != "config backup retained" {
		t.Fatalf("configuration backup changed: %q, %v", body, err)
	}
	commands, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"stop qagent-mihomo.service", "disable qagent-mihomo.service", "disable --runtime qagent-mihomo.service",
	} {
		if !strings.Contains(string(commands), command) {
			t.Fatalf("missing service command %q in %q", command, commands)
		}
	}
	asset := mihomoVersionFixture(t, true)
	digest := sha256.Sum256(asset)
	release, err := json.Marshal(githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{{
		Name: "mihomo-linux-amd64-v1.19.30.gz", Size: int64(len(asset)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-linux-amd64-v1.19.30.gz",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	executor.Updater = &CoreUpdater{apiBase: githubAPIBase, goarch: "amd64", libc: "gnu", trustedURL: trustedCoreReleaseURL,
		downloadAttempts: 1, downloadAttemptTimeout: time.Second,
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body := asset
			if request.URL.Host == "api.github.com" {
				body = release
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
		})}}
	if err := os.WriteFile(failEnable, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionStable}); err == nil {
		t.Fatal("first install succeeded despite failed service enablement")
	}
	if _, err := os.Lstat(binary); !os.IsNotExist(err) {
		t.Fatalf("failed first install left a binary that would make retries skip enablement: %v", err)
	}
	if status, err := os.ReadFile(state); err != nil || string(status) != "inactive\n" {
		t.Fatalf("failed first install left service running: %q, %v", status, err)
	}
	if err := os.Remove(failEnable); err != nil {
		t.Fatal(err)
	}
	for range 2 { // Reinstall enables the service; a later update preserves its state.
		if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionStable}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("reinstalled binary: %v", err)
	}
	commands, err = os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(commands), "enable qagent-mihomo.service\n"); got != 2 {
		t.Fatalf("service enable attempted %d times; want failed first attempt and successful retry only: %s", got, commands)
	}
}

func TestOpenRCUninstallAndReinstallUpdatesRunlevel(t *testing.T) {
	runlevels, initRoot := useOpenRCTestRunlevels(t)
	const service = "qagent-mihomo"
	writeOpenRCTestService(t, initRoot, service)
	if err := os.Symlink(filepath.Join(initRoot, service), filepath.Join(runlevels, "default", service)); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	coreDir := filepath.Join(root, "cores")
	for _, directory := range []string{stateRoot, coreDir} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeOpenRCTestActiveState(t, stateRoot, service, "active")
	manager, log := newOpenRCTestManager(t, stateRoot, runlevels, initRoot)
	binary := filepath.Join(coreDir, "mihomo")
	if err := os.WriteFile(binary, []byte("old binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	spec := EngineSpec{Binary: binary, ConfigPath: filepath.Join(root, "config.yaml"), Service: service}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineMihomo: spec}, Services: manager}
	if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionUninstall, Engine: core.EngineMihomo}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(binary); !os.IsNotExist(err) {
		t.Fatalf("OpenRC uninstall left binary: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(runlevels, "default", service)); !os.IsNotExist(err) {
		t.Fatalf("OpenRC uninstall left enabled runlevel link: %v", err)
	}
	asset := mihomoVersionFixture(t, true)
	digest := sha256.Sum256(asset)
	release, err := json.Marshal(githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{{
		Name: "mihomo-linux-amd64-v1.19.30.gz", Size: int64(len(asset)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-linux-amd64-v1.19.30.gz",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	executor.Updater = &CoreUpdater{apiBase: githubAPIBase, goarch: "amd64", libc: "gnu", trustedURL: trustedCoreReleaseURL,
		downloadAttempts: 1, downloadAttemptTimeout: time.Second,
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body := asset
			if request.URL.Host == "api.github.com" {
				body = release
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
		})}}
	if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionStable}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(runlevels, "default", service)); err != nil {
		t.Fatalf("OpenRC reinstall did not enable runlevel link: %v", err)
	}
	commands, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"rc-service qagent-mihomo stop", "rc-update del qagent-mihomo default", "rc-service qagent-mihomo start", "rc-update add qagent-mihomo default"} {
		if !strings.Contains(string(commands), command) {
			t.Fatalf("missing OpenRC command %q in %s", command, commands)
		}
	}
}

func TestUninstallKeepsBinaryWhenServiceStopOrDisableFails(t *testing.T) {
	for _, failedCommand := range []string{"stop", "disable"} {
		t.Run(failedCommand, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "mihomo")
			if err := os.WriteFile(binary, []byte("keep binary"), 0o700); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(root, "state")
			if err := os.WriteFile(state, []byte("active\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			service := filepath.Join(root, "systemctl")
			contents := "#!/bin/sh\n" +
				"[ \"$1\" = '" + failedCommand + "' ] && exit 1\n" +
				"case \"$1\" in\n" +
				"  is-active) /bin/cat '" + state + "' ;;\n" +
				"  stop) /usr/bin/printf 'inactive\\n' > '" + state + "' ;;\n" +
				"esac\n"
			if err := os.WriteFile(service, []byte(contents), 0o700); err != nil {
				t.Fatal(err)
			}
			executor := &Executor{
				Specs: map[core.Engine]EngineSpec{core.EngineMihomo: {
					Binary: binary, ConfigPath: filepath.Join(root, "config.yaml"), Service: "qagent-mihomo.service",
				}},
				Services: &ServiceManager{kind: ServiceManagerSystemd, executable: service, enableExecutable: service},
			}
			if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionUninstall, Engine: core.EngineMihomo}); err == nil {
				t.Fatal("service failure did not abort core removal")
			}
			if body, err := os.ReadFile(binary); err != nil || string(body) != "keep binary" {
				t.Fatalf("failed service operation removed the binary: %q, %v", body, err)
			}
		})
	}
}

func TestUninstallRequiresStoppedSharedInstancesToBeDisabled(t *testing.T) {
	root := t.TempDir()
	shares := filepath.Join(root, "shares")
	if err := os.Mkdir(shares, 0o700); err != nil {
		t.Fatal(err)
	}
	shareID := "shr_0123456789abcdef"
	if err := os.Mkdir(filepath.Join(shares, shareID), 0o700); err != nil {
		t.Fatal(err)
	}
	enablement := filepath.Join(root, "enablement")
	if err := os.WriteFile(enablement, []byte("enabled\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := filepath.Join(root, "systemctl")
	contents := "#!/bin/sh\ncase \"$1\" in\n" +
		"  is-active) echo inactive; exit 3 ;;\n" +
		"  is-enabled) /bin/cat '" + enablement + "'; exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(service, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Services: &ServiceManager{kind: ServiceManagerSystemd, executable: service, enableExecutable: service}}
	base := EngineSpec{Binary: filepath.Join(root, "mihomo"), Service: "qagent-mihomo.service"}
	if err := executor.checkSharedCoreInstancesInactiveAtRoot(context.Background(), core.EngineMihomo, base, shares); err == nil || !strings.Contains(err.Error(), "must be disabled") {
		t.Fatalf("enabled but stopped shared service was accepted: %v", err)
	}
	if err := os.WriteFile(enablement, []byte("disabled\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := executor.checkSharedCoreInstancesInactiveAtRoot(context.Background(), core.EngineMihomo, base, shares); err != nil {
		t.Fatalf("stopped and disabled shared service was rejected: %v", err)
	}
}

func TestUninstallIncludesXrayResourceBackupsOnlyForManagedMapping(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	want := []string{
		"xray", "geoip.dat", "geosite.dat",
		"xray.bak-20260928T000000Z-012345abcdef",
		"geoip.dat.bak-20260928T000000Z-012345abcdef",
		"geosite.dat.bak-20260928T000000Z-012345abcdef",
	}
	for _, name := range want[3:] {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("old resource"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manager := defaultSystemdServiceManager()
	spec := DefaultSpecsForServiceManager(manager.Kind())[core.EngineXray]
	files, err := managedCoreRemovalFiles(root, core.EngineXray, spec, manager)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(want) {
		t.Fatalf("Xray removal files = %v, want %v", files, want)
	}
	for _, name := range want {
		if !slices.Contains(files, name) {
			t.Fatalf("Xray removal omitted %s: %v", name, files)
		}
	}
	custom := spec
	custom.Binary = filepath.Join(directory, "xray")
	files, err = managedCoreRemovalFiles(root, core.EngineXray, custom, manager)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(files, "geoip.dat") || slices.Contains(files, "geosite.dat") {
		t.Fatalf("custom mapping would remove unowned Xray resources: %v", files)
	}
}

func TestUninstallRejectsUnsafeCoreBackupBeforeStopping(t *testing.T) {
	for _, test := range []struct {
		name         string
		requiresRoot bool
		prepare      func(string, string) error
	}{
		{name: "symlink", prepare: func(backup, victim string) error { return os.Symlink(victim, backup) }},
		{name: "directory", prepare: func(backup, _ string) error { return os.Mkdir(backup, 0o700) }},
		{name: "group-writable", prepare: func(backup, _ string) error {
			if err := os.WriteFile(backup, []byte("backup"), 0o600); err != nil {
				return err
			}
			return os.Chmod(backup, 0o660)
		}},
		{name: "foreign-owner", requiresRoot: true, prepare: func(backup, _ string) error {
			if err := os.WriteFile(backup, []byte("backup"), 0o600); err != nil {
				return err
			}
			return os.Chown(backup, 65534, -1)
		}},
		{name: "unrecognized-name", prepare: func(backup, _ string) error { return os.WriteFile(backup+"-extra", []byte("backup"), 0o600) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.requiresRoot && os.Geteuid() != 0 {
				t.Skip("changing backup ownership requires root")
			}
			root := t.TempDir()
			binary := filepath.Join(root, "mihomo")
			victim := filepath.Join(root, "victim")
			backup := filepath.Join(root, "mihomo.bak-20260928T000000Z-012345abcdef")
			for _, name := range []string{binary, victim} {
				if err := os.WriteFile(name, []byte("keep"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := test.prepare(backup, victim); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(root, "service.log")
			service := filepath.Join(root, "systemctl")
			contents := "#!/bin/sh\ncase \"$1\" in\n" +
				"  is-active) echo inactive; exit 3 ;;\n" +
				"  *) echo \"$*\" >> '" + log + "' ;;\n" +
				"esac\n"
			if err := os.WriteFile(service, []byte(contents), 0o700); err != nil {
				t.Fatal(err)
			}
			executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineMihomo: {
				Binary: binary, ConfigPath: filepath.Join(root, "config.yaml"), Service: "qagent-mihomo.service",
			}}, Services: &ServiceManager{kind: ServiceManagerSystemd, executable: service, enableExecutable: service}}
			if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionUninstall, Engine: core.EngineMihomo}); err == nil {
				t.Fatal("unsafe core backup was accepted")
			}
			if body, err := os.ReadFile(binary); err != nil || string(body) != "keep" {
				t.Fatalf("unsafe backup caused binary removal: %q, %v", body, err)
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatalf("unsafe backup caused service mutation: %v", err)
			}
			if body, err := os.ReadFile(victim); err != nil || string(body) != "keep" {
				t.Fatalf("unsafe backup changed victim: %q, %v", body, err)
			}
		})
	}
}

func TestUninstallRejectsUnsafeBinaryBeforeStoppingService(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "mihomo")); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineMihomo: {
		Binary: filepath.Join(root, "mihomo"), ConfigPath: victim, Service: "qagent-mihomo.service",
	}}}
	if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionUninstall, Engine: core.EngineMihomo}); err == nil {
		t.Fatal("symlinked core binary was accepted")
	}
	if body, err := os.ReadFile(victim); err != nil || string(body) != "keep" {
		t.Fatalf("symlink target changed: %q, %v", body, err)
	}
}
