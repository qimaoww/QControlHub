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

func TestPresetAutoInstallStableAndKeepInstalledVersion(t *testing.T) {
	requireAgentRoot(t)
	asset := mihomoVersionFixture(t, true)
	digest := sha256.Sum256(asset)
	release, err := json.Marshal(githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{{
		Name: "mihomo-linux-amd64-v1.19.30.gz", Size: int64(len(asset)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-linux-amd64-v1.19.30.gz",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		for _, installed := range []bool{false, true} {
			t.Run(string(action)+"/installed="+map[bool]string{true: "yes", false: "no"}[installed], func(t *testing.T) {
				directory := t.TempDir()
				logPath := filepath.Join(directory, "service.log")
				serviceHelper := filepath.Join(directory, "service-helper")
				if err := os.WriteFile(serviceHelper, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+logPath+"'\ncase \"$1\" in\nis-active) echo active;;\nesac\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				spec := EngineSpec{Binary: filepath.Join(directory, "mihomo"), ConfigPath: filepath.Join(directory, "config.yaml"), Service: "qagent-mihomo.service"}
				const oldConfig = "listeners: []\nrules: ['MATCH,DIRECT']\n"
				if err := os.WriteFile(spec.ConfigPath, []byte(oldConfig), 0o600); err != nil {
					t.Fatal(err)
				}
				const development = "#!/bin/sh\necho 'Mihomo development; do not replace'\n"
				if installed {
					if err := os.WriteFile(spec.Binary, []byte(development), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				requests := 0
				updater := &CoreUpdater{apiBase: githubAPIBase, goarch: "amd64", libc: "gnu", trustedURL: trustedCoreReleaseURL,
					downloadAttempts: 1, downloadAttemptTimeout: time.Second,
					client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
						requests++
						body := asset
						if request.URL.Host == "api.github.com" {
							if request.URL.Path != "/repos/MetaCubeX/mihomo/releases/latest" {
								t.Fatalf("automatic install did not select latest stable: %s", request.URL)
							}
							body = release
						}
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
					})}}
				executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineMihomo: spec}, Updater: updater,
					Services: &ServiceManager{kind: ServiceManagerSystemd, executable: serviceHelper, enableExecutable: serviceHelper}}
				task := core.Task{Action: action, Engine: core.EngineMihomo, InstallIfMissing: true, ConfigVersion: 3,
					ConfigContent: "listeners: [{name: fixed, type: socks, port: 21001}]\nrules: ['MATCH,DIRECT']\n"}
				output, err := executor.Execute(context.Background(), task)
				if err != nil {
					t.Fatalf("compound execution failed: %v\n%s", err, output)
				}
				if installed {
					binary, _ := os.ReadFile(spec.Binary)
					if requests != 0 || string(binary) != development || !strings.Contains(output, "keeping the current version") {
						t.Fatalf("existing development binary was changed: requests=%d output=%s", requests, output)
					}
				} else if requests != 2 || !strings.Contains(output, "installed mihomo release v1.19.30") {
					t.Fatalf("stable release not installed: requests=%d output=%s", requests, output)
				}
				config, _ := os.ReadFile(spec.ConfigPath)
				want := oldConfig
				if action == core.ActionDeploy {
					var warning string
					want, warning = executor.prepareNativeAccountingContent(context.Background(), task.Engine, spec, task.ConfigContent)
					if warning != "" {
						t.Fatal(warning)
					}
				}
				if string(config) != want {
					t.Fatalf("wrong configuration effect for %s: %s", action, config)
				}
				// A retry after installation must not download, replace the
				// binary or restart an installation a second time.
				before := requests
				if _, err := executor.Execute(context.Background(), task); err != nil || requests != before {
					t.Fatalf("retry installed again: %v, requests %d -> %d", err, before, requests)
				}
			})
		}
	}
}

func TestPresetAutoInstallFailureStopsConfiguration(t *testing.T) {
	requireAgentRoot(t)
	directory := t.TempDir()
	spec := EngineSpec{Binary: filepath.Join(directory, "mihomo"), ConfigPath: filepath.Join(directory, "config.yaml"), Service: "qagent-mihomo.service"}
	const original = "listeners: []\n"
	if err := os.WriteFile(spec.ConfigPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineMihomo: spec}, Updater: &CoreUpdater{
		apiBase: githubAPIBase, goarch: "amd64", client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: http.NoBody, Request: request}, nil
		})},
	}}
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		_, err := executor.Execute(context.Background(), core.Task{Action: action, Engine: core.EngineMihomo,
			InstallIfMissing: true, ConfigVersion: 1, ConfigContent: "listeners: [{name: a, type: socks, port: 21001}]\n"})
		if err == nil || !strings.Contains(err.Error(), "stable core installation failed; configuration was not executed") {
			t.Fatalf("install failure did not stop %s: %v", action, err)
		}
		config, _ := os.ReadFile(spec.ConfigPath)
		if string(config) != original {
			t.Fatal("install failure changed node configuration")
		}
		if _, err := os.Stat(spec.Binary); !os.IsNotExist(err) {
			t.Fatal("failed installation left a binary")
		}
	}
}

func TestPresetAutoInstallRejectsInvalidTaskShape(t *testing.T) {
	executor := &Executor{Specs: DefaultSpecs()}
	for _, task := range []core.Task{
		{Action: core.ActionRestart, Engine: core.EngineMihomo, InstallIfMissing: true},
		{Action: core.ActionValidate, Engine: core.EngineMihomo, InstallIfMissing: true},
		{Action: core.ActionDeploy, Engine: core.EngineMihomo, InstallIfMissing: true, ConfigVersion: 1, ConfigContent: "listeners: []", CoreVersion: "development"},
		{Action: core.ActionDeploy, Engine: core.EngineMihomo, InstallIfMissing: true, ConfigVersion: 1, ConfigContent: "listeners: []", SharedTrafficID: "shared"},
	} {
		if _, err := executor.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "invalid automatic") {
			t.Fatalf("invalid compound task accepted: %+v %v", task, err)
		}
	}
}
