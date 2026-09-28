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
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("config retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "service.state")
	log := filepath.Join(root, "service.log")
	if err := os.WriteFile(state, []byte("active\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := filepath.Join(root, "systemctl")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n" +
		"case \"$1\" in\n" +
		"  is-active) /bin/cat '" + state + "' ;;\n" +
		"  stop) /usr/bin/printf 'inactive\\n' > '" + state + "' ;;\n" +
		"  restart) /usr/bin/printf 'active\\n' > '" + state + "' ;;\n" +
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
	if body, err := os.ReadFile(config); err != nil || string(body) != "config retained" {
		t.Fatalf("managed configuration changed: %q, %v", body, err)
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
	if got := strings.Count(string(commands), "enable qagent-mihomo.service\n"); got != 1 {
		t.Fatalf("service enabled %d times; want once after fresh installation: %s", got, commands)
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
