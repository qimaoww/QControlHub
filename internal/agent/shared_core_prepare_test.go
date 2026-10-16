package agent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/ulikunitz/xz"
)

// Exercise the actual verified downloader and archive extraction for every
// supported engine. A private preparation may publish a missing binary, but
// must never invoke a host restart, enablement, or configuration rewrite.
func TestSharedStableCorePreparationAllEngines(t *testing.T) {
	requireAgentRoot(t)
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		t.Run(string(engine), func(t *testing.T) {
			for _, corrupt := range []bool{false, true} {
				t.Run(map[bool]string{false: "verified", true: "checksum-failure"}[corrupt], func(t *testing.T) {
					directory := t.TempDir()
					archiveName, repository, asset := sharedCoreReleaseFixture(t, engine)
					digest := sha256.Sum256(asset)
					metadata, _ := json.Marshal(githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{{Name: archiveName, Size: int64(len(asset)), Digest: "sha256:" + hex.EncodeToString(digest[:]), BrowserDownloadURL: "https://github.com/" + repository + "/releases/download/v1.19.30/" + archiveName}}})
					if corrupt {
						asset = append([]byte(nil), asset...)
						asset[len(asset)/2] ^= 1
					}
					requests := 0
					updater := &CoreUpdater{apiBase: githubAPIBase, goarch: "amd64", libc: "gnu", trustedURL: trustedCoreReleaseURL, downloadAttempts: 1, downloadAttemptTimeout: time.Second,
						client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
							requests++
							body := asset
							if request.URL.Host == "api.github.com" {
								if !strings.HasSuffix(request.URL.Path, "/releases/latest") {
									t.Fatalf("non-stable selector: %s", request.URL)
								}
								body = metadata
							}
							return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
						})}}
					configPath := filepath.Join(directory, "owner.conf")
					if err := os.WriteFile(configPath, []byte("owner configuration is unchanged"), 0600); err != nil {
						t.Fatal(err)
					}
					helper := filepath.Join(directory, "forbidden-service-helper")
					marker := filepath.Join(directory, "host-service-touched")
					if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 99\n"), 0700); err != nil {
						t.Fatal(err)
					}
					spec := EngineSpec{Binary: filepath.Join(directory, "core"), ConfigPath: configPath, Service: "qagent-owner.service"}
					output, err := updater.install(context.Background(), engine, spec, "stable", "", &ServiceManager{kind: ServiceManagerSystemd, executable: helper, enableExecutable: helper}, false)
					if requests != 2 {
						t.Fatalf("unexpected download count %d", requests)
					}
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Fatal("private preparation touched a service")
					}
					config, _ := os.ReadFile(configPath)
					if string(config) != "owner configuration is unchanged" {
						t.Fatal("owner config changed")
					}
					if corrupt {
						if err == nil || !strings.Contains(strings.ToLower(err.Error()), "sha-256") {
							t.Fatalf("corrupt asset accepted: %v", err)
						}
						if _, err := os.Stat(spec.Binary); !os.IsNotExist(err) {
							t.Fatal("failed preparation published a binary")
						}
						return
					}
					if err != nil || !strings.Contains(output, "verified asset SHA-256") {
						t.Fatalf("preparation: %s %v", output, err)
					}
					if _, err := run(context.Background(), spec.Binary, coreVersionArgs(engine)...); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func sharedCoreReleaseFixture(t *testing.T, engine core.Engine) (string, string, []byte) {
	t.Helper()
	padding := make([]byte, 2<<20)
	if _, err := rand.Read(padding); err != nil {
		t.Fatal(err)
	}
	script := []byte("#!/bin/sh\necho 'core v1.19.30'\nexit 0\n: <<'QCH_PADDING'\n" + hex.EncodeToString(padding) + "\nQCH_PADDING\n")
	var buffer bytes.Buffer
	switch engine {
	case core.EngineMihomo:
		writer := gzip.NewWriter(&buffer)
		_, _ = writer.Write(script)
		_ = writer.Close()
		return "mihomo-linux-amd64-v1.19.30.gz", "MetaCubeX/mihomo", buffer.Bytes()
	case core.EngineXray:
		writer := zip.NewWriter(&buffer)
		for name, data := range map[string][]byte{"xray": script, "geoip.dat": padding[:2048], "geosite.dat": padding[2048:4096]} {
			entry, err := writer.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = entry.Write(data)
		}
		_ = writer.Close()
		return "Xray-linux-64.zip", "XTLS/Xray-core", buffer.Bytes()
	default:
		var compressed io.WriteCloser
		binary, name, repository := "sing-box", "sing-box-1.19.30-linux-amd64.tar.gz", "SagerNet/sing-box"
		if engine == core.EngineShadowsocksRust {
			var err error
			compressed, err = xz.NewWriter(&buffer)
			if err != nil {
				t.Fatal(err)
			}
			binary, name, repository = "ssserver", "shadowsocks-v1.19.30.x86_64-unknown-linux-gnu.tar.xz", "shadowsocks/shadowsocks-rust"
		} else {
			compressed = gzip.NewWriter(&buffer)
		}
		writer := tar.NewWriter(compressed)
		if err := writer.WriteHeader(&tar.Header{Name: binary, Mode: 0755, Size: int64(len(script)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write(script)
		_ = writer.Close()
		_ = compressed.Close()
		return name, repository, buffer.Bytes()
	}
}

func TestSharedPrepareTaskShapeRetainsIsolation(t *testing.T) {
	client := &Client{creds: credentials{AgentID: "agt_fixture"}, executor: &Executor{Specs: DefaultSpecs()}}
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		task := core.Task{ID: "tsk_0123456789abcdef", AgentID: "agt_fixture", Status: core.TaskRunning, LeaseID: strings.Repeat("l", 32), Action: action, Engine: core.EngineMihomo, SharedInstance: true, SharedTrafficID: "shr_0123456789abcdef", InstallIfMissing: true, ConfigID: "cfg_private", ConfigVersion: 1, ConfigContent: "listeners: []"}
		if !client.validTask(task) {
			t.Fatalf("valid private preparation rejected: %+v", task)
		}
		task.SharedInstance = false
		if client.validTask(task) {
			t.Fatal("host installation accepted with share identity")
		}
		task.SharedInstance = true
		task.SharedTrafficID = ""
		if client.validTask(task) {
			t.Fatal("private preparation lost sharing identity")
		}
	}
	manual := core.Task{ID: "tsk_0123456789abcdef", AgentID: "agt_fixture", Status: core.TaskRunning, LeaseID: strings.Repeat("l", 32), Action: core.ActionInstall, Engine: core.EngineMihomo, SharedInstance: true, SharedTrafficID: "shr_0123456789abcdef", CoreVersion: core.CoreVersionStable}
	if !client.validTask(manual) {
		t.Fatal("manual private preparation rejected")
	}
	for _, action := range []core.Action{core.ActionStop, core.ActionStart, core.ActionRestart, core.ActionUninstall} {
		invalid := manual
		invalid.Action = action
		if client.validTask(invalid) {
			t.Fatalf("private host action accepted: %s", action)
		}
	}
	manual.CoreVersion = "v1.19.29"
	if client.validTask(manual) {
		t.Fatal("private version switch accepted")
	}
}

func TestSharedManualInstallationReachesClientExecutor(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		t.Run(string(engine), func(t *testing.T) {
			task := core.Task{ID: "tsk_0123456789abcdef", AgentID: "agt_fixture", Status: core.TaskRunning,
				LeaseID: strings.Repeat("l", 32), Action: core.ActionInstall, Engine: engine,
				SharedInstance: true, SharedTrafficID: "shr_0123456789abcdef", CoreVersion: core.CoreVersionStable}
			calls := 0
			client := &Client{creds: credentials{AgentID: task.AgentID}, executor: &Executor{Specs: DefaultSpecs()},
				config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "agent.json")},
				executeFunc: func(_ context.Context, received core.Task) (string, error) {
					calls++
					if received.SharedTrafficID != task.SharedTrafficID || !received.SharedInstance || received.CoreVersion != core.CoreVersionStable {
						t.Fatal("client lost controlled installation scope")
					}
					return "prepared", nil
				}}
			if !client.validTask(task) {
				t.Fatal("manual preparation rejected at dispatch")
			}
			if result := client.resultForTask(context.Background(), task); !result.Success || calls != 1 {
				t.Fatalf("manual preparation did not reach execution: %+v; calls=%d", result, calls)
			}
			for i, change := range []func(*core.Task){
				func(task *core.Task) { task.CoreVersion = "v1.19.29" },
				func(task *core.Task) { task.CoreSource = "official" },
				func(task *core.Task) { task.ConfigID = "cfg_private" },
				func(task *core.Task) { task.ConfigContent = "{}" },
				func(task *core.Task) { task.ConfigVersion = 1 },
				func(task *core.Task) { task.InstallIfMissing = true },
				func(task *core.Task) { task.SharedInstance = false },
				func(task *core.Task) { task.SharedTrafficID = "../owner" },
				func(task *core.Task) { task.Action = core.ActionStop },
				func(task *core.Task) { task.Action = core.ActionUninstall },
			} {
				invalid := task
				invalid.ID = fmt.Sprintf("tsk_%016x", i+1)
				change(&invalid)
				if client.validTask(invalid) {
					t.Fatalf("unsafe preparation accepted at dispatch: %+v", invalid)
				}
				if result := client.resultForTask(context.Background(), invalid); result.Success || calls != 1 {
					t.Fatalf("unsafe preparation reached execution: %+v; calls=%d", result, calls)
				}
			}
		})
	}
}
