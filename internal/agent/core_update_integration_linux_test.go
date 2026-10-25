//go:build linux

package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Opt in only on a disposable systemd Docker node bootstrapped from deploy/.
// Release responses use local copies whose official digests are pinned below.
func TestCoreUpdateWithRealSharedServices(t *testing.T) {
	if os.Getenv("QCH_TEST_CORE_UPDATE") != "1" {
		t.Skip("set QCH_TEST_CORE_UPDATE=1 in an isolated systemd Docker node")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("real core update tests require a disposable Docker node")
	}
	input := os.Getenv("QCH_TEST_CORE_INPUT")
	if !filepath.IsAbs(input) {
		t.Fatal("QCH_TEST_CORE_INPUT must contain verified release archives")
	}
	oldArchive := filepath.Join(input, "sing-box-1.11.15-linux-amd64.tar.gz")
	verifyCoreUpdateTestArchive(t, oldArchive, "950af37eb2d7e55dddae34a18411cd617303fd99d2dc75bc76b6dd9fcd97d9c5")
	archive := verifyCoreUpdateTestArchive(t, filepath.Join(input, "sing-box-1.13.0-linux-amd64.tar.gz"),
		"86db0f9df3f822ca2adccde2f7c1c9e21d64e646a77bf258274fde3be399025b")
	manager := defaultSystemdServiceManager()
	base := DefaultSpecs()[core.EngineSingBox]
	shared, err := sharedInstanceSpec(core.EngineSingBox, base, "shr_0123456789abcdef", manager.Kind())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name              string
		mainIncompatible  bool
		shareIncompatible bool
		shareStopped      bool
		mainStopped       bool
		noMain            bool
		activationFault   bool
		missingBinary     bool
	}{
		{name: "reject incompatible running shared configuration", shareIncompatible: true},
		{name: "reject incompatible stopped shared configuration", shareIncompatible: true, shareStopped: true},
		{name: "reject incompatible main configuration", mainIncompatible: true},
		{name: "upgrade every running instance"},
		{name: "preserve stopped shared instance", shareStopped: true},
		{name: "preserve stopped main instance", mainStopped: true},
		{name: "upgrade shared-only deployment", noMain: true},
		{name: "shared startup failure restores every instance", activationFault: true},
		{name: "reject lost binary before service preparation", missingBinary: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			for _, spec := range []EngineSpec{base, shared} {
				_, _ = manager.command(ctx, spec.Service, core.ActionStop)
			}
			t.Cleanup(func() {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer stopCancel()
				for _, spec := range []EngineSpec{base, shared} {
					_, _ = manager.command(stopCtx, spec.Service, core.ActionStop)
				}
			})
			old, err := os.OpenFile(base.Binary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
			if err != nil {
				t.Fatal(err)
			}
			if err := extractCoreBinary(core.EngineSingBox, filepath.Base(oldArchive), oldArchive, old); err != nil {
				old.Close()
				t.Fatal(err)
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			mainContent := coreUpdateTestSingBoxContent(25191, test.mainIncompatible)
			sharedContent := coreUpdateTestSingBoxContent(25192, test.shareIncompatible)
			executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: base}, Services: manager,
				Updater: coreUpdateRealTestUpdater(archive)}
			if err := prepareSharedInstanceDirectory(core.EngineSingBox, shared, manager.Kind()); err != nil {
				t.Fatal(err)
			}
			if err := executor.ensureSharedInstanceService(ctx, core.EngineSingBox, shared); err != nil {
				t.Fatal(err)
			}
			for spec, content := range map[EngineSpec]string{base: mainContent, shared: sharedContent} {
				if err := os.WriteFile(spec.ConfigPath, []byte(content), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := ensureDefaultManagedConfigurationAccess(core.EngineSingBox, spec, manager); err != nil {
					t.Fatal(err)
				}
			}
			sharedTask := core.Task{ID: "tsk_0123456789abcdea", Engine: core.EngineSingBox, Action: core.ActionRestart,
				SharedInstance: true, SharedTrafficID: "shr_0123456789abcdef", ConfigID: "cfg_isolated_update", ConfigVersion: 1, ConfigContent: sharedContent}
			if err := executor.commitSharedDeployment(shared, sharedTask); err != nil {
				t.Fatal(err)
			}
			if test.noMain {
				if err := os.Remove(base.ConfigPath); err != nil {
					t.Fatal(err)
				}
			} else if !test.mainStopped {
				if output, err := serviceCommandAndVerifyWithManager(ctx, manager, base.Service, core.ActionStart); err != nil {
					t.Fatalf("start old main: %v %s", err, output)
				}
			}
			if !test.shareStopped {
				if output, err := serviceCommandAndVerifyWithManager(ctx, manager, shared.Service, core.ActionStart); err != nil {
					t.Fatalf("start old shared: %v %s", err, output)
				}
			}
			oldMainPID, _ := systemdUnitProperty(ctx, base.Service, "MainPID")
			oldSharePID, _ := systemdUnitProperty(ctx, shared.Service, "MainPID")
			if test.missingBinary {
				if err := os.Remove(base.Binary); err != nil {
					t.Fatal(err)
				}
			}
			client := &Client{executor: executor, config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "agent-state.json")}}
			if !test.mainIncompatible && !test.shareIncompatible {
				collector := NewCoreLogCollectorForExecutor(executor)
				runCoreLogCollector(t, collector)
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					collector.mu.Lock()
					started := collector.runContext != nil
					collector.mu.Unlock()
					if started {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := collector.prepareSharedLogSource(ctx, core.EngineSingBox, shared); err != nil {
					t.Fatal(err)
				}
				client.logs = collector
			}
			if test.activationFault {
				plan := managedCoreUpdateTransaction(executor, core.EngineSingBox, base, manager)
				fault := &coreUpdateBindFailure{coreUpdateTransaction: plan}
				t.Cleanup(fault.closeListener)
				client.executeFunc = func(ctx context.Context, task core.Task) (string, error) {
					return executor.Updater.installWithTransaction(ctx, task.Engine, base, task.CoreVersion, "", manager, true, fault)
				}
			}
			updatedAt := time.Now().UTC()
			result := client.resultForTask(ctx, core.Task{ID: "tsk_0123456789abcdeb", Engine: core.EngineSingBox, Action: core.ActionInstall, CoreVersion: "1.13.0"})
			if test.missingBinary {
				mainPID, _ := systemdUnitProperty(ctx, base.Service, "MainPID")
				sharePID, _ := systemdUnitProperty(ctx, shared.Service, "MainPID")
				_, binaryErr := os.Lstat(base.Binary)
				if result.Success || !strings.Contains(result.Error, "running without its installed binary") ||
					mainPID != oldMainPID || sharePID != oldSharePID || !os.IsNotExist(binaryErr) {
					t.Fatalf("lost-binary update changed a running deployment: result=%+v main=%s→%s shared=%s→%s binary=%v", result, oldMainPID, mainPID, oldSharePID, sharePID, binaryErr)
				}
				t.Log("lost binary rejected before bootstrap; both running processes were preserved")
				return
			}
			wantFailure := test.mainIncompatible || test.shareIncompatible || test.activationFault
			if result.Success == wantFailure {
				t.Fatalf("wrong update task result: success=%v error=%s output=%s", result.Success, result.Error, result.Output)
			}
			if test.noMain {
				if _, err := os.Stat(base.ConfigPath); !os.IsNotExist(err) {
					t.Fatalf("shared-only update created an unrequested main configuration: %v", err)
				}
			}
			version := binaryVersion(ctx, core.EngineSingBox, base.Binary)
			wantVersion := "1.13.0"
			if wantFailure {
				wantVersion = "1.11.15"
			}
			if !strings.Contains(version, wantVersion) {
				t.Fatalf("installed version=%s; want %s", version, wantVersion)
			}
			mainPID, _ := systemdUnitProperty(ctx, base.Service, "MainPID")
			sharePID, _ := systemdUnitProperty(ctx, shared.Service, "MainPID")
			mainStatus, _ := manager.status(ctx, base.Service)
			shareStatus, _ := manager.status(ctx, shared.Service)
			if (mainStatus == "active") == (test.noMain || test.mainStopped) || (shareStatus == "active") == test.shareStopped {
				t.Fatalf("instance states changed unexpectedly: main=%s shared=%s", mainStatus, shareStatus)
			}
			if wantFailure && !test.activationFault {
				if mainPID != oldMainPID || sharePID != oldSharePID || !strings.Contains(result.Error, "rejected") {
					t.Fatalf("incompatibility was not rejected before mutation: main %s→%s shared %s→%s error=%s", oldMainPID, mainPID, oldSharePID, sharePID, result.Error)
				}
			} else {
				for _, pair := range [][2]string{{oldMainPID, mainPID}, {oldSharePID, sharePID}} {
					if pair[1] != "0" && pair[1] != "" {
						exe, err := os.Readlink("/proc/" + pair[1] + "/exe")
						if err != nil || strings.HasSuffix(exe, " (deleted)") || pair[0] == pair[1] {
							t.Fatalf("old executable survived an update/recovery: pid=%v exe=%s error=%v", pair, exe, err)
						}
					}
				}
			}
			// This restart previously exposed the false-success incompatibility.
			if output, err := executor.Execute(ctx, sharedTask); err != nil {
				t.Fatalf("subsequent shared restart failed after update result: %v %s", err, output)
			}
			if client.logs != nil {
				found := false
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) && !found {
					if batch := client.logs.NextBatch(); batch != nil {
						for _, entry := range batch.Entries {
							if entry.InstanceID == sharedTask.SharedTrafficID && entry.Engine == core.EngineSingBox &&
								!entry.LoggedAt.Before(updatedAt) && strings.Contains(entry.Message, "sing-box started") {
								found = true
							}
						}
						client.logs.Acknowledge(batch.ID)
					}
					time.Sleep(20 * time.Millisecond)
				}
				if !found {
					t.Fatal("shared startup logs did not enter the upload queue after update/recovery and restart")
				}
				if state := client.logs.InstanceStatuses()[string(core.EngineSingBox)+"/"+sharedTask.SharedTrafficID]; state.CoreLogError != "" || state.CoreLogStatus != core.CoreLogStatusActive {
					t.Fatalf("shared log source did not clear its previous error: %+v", state)
				}
			}
			t.Logf("task_success=%v version=%s main=%s pid=%s→%s shared=%s pid=%s→%s later_shared_restart=success error=%s", result.Success, version, mainStatus, oldMainPID, mainPID, shareStatus, oldSharePID, sharePID, result.Error)
		})
	}
}

func verifyCoreUpdateTestArchive(t *testing.T, path, expected string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != expected {
		t.Fatalf("archive %s differs from its official release digest", filepath.Base(path))
	}
	return content
}

func TestSSRustRealUpdatePreflight(t *testing.T) {
	if os.Getenv("QCH_TEST_CORE_UPDATE") != "1" {
		t.Skip("requires an isolated Docker node and verified ssserver")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("ss-rust update preflight tests require Docker")
	}
	input := os.Getenv("QCH_TEST_CORE_INPUT")
	verifyCoreUpdateTestArchive(t, filepath.Join(input, "shadowsocks-v1.25.0.x86_64-unknown-linux-musl.tar.xz"),
		"8439bf43c324b0fc273e663d0b1f8926fd8f666cbd1e0fd59b35096f0e778e92")
	manager, err := NewServiceManager(os.Getenv("QCH_TEST_PREFLIGHT_MANAGER"))
	if err != nil {
		t.Fatal(err)
	}
	base := DefaultSpecsForServiceManager(manager.Kind())[core.EngineShadowsocksRust]
	executor := &Executor{Services: manager}
	for _, method := range []string{"aes-256-gcm", "removed-cipher-for-isolated-test"} {
		t.Run(method, func(t *testing.T) {
			content := `{"server":"127.0.0.1","server_port":25195,"password":"isolated-private-test-password","method":"` + method + `","mode":"tcp_and_udp","manager_address":"127.0.0.1:25196"}`
			if err := os.WriteFile(base.ConfigPath, []byte(content), 0o640); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := executor.validateCoreUpdateCandidate(ctx, core.EngineShadowsocksRust, base, content, filepath.Join(input, "ssserver"), input)
			if method == "aes-256-gcm" && err != nil {
				t.Fatalf("compatible target did not pass isolated startup: %v", err)
			}
			if method != "aes-256-gcm" && (err == nil || strings.Contains(err.Error(), "isolated-private-test-password")) {
				t.Fatalf("incompatible target was accepted or exposed a credential: %v", err)
			}
			for _, endpoint := range []string{"127.0.0.1:24001", "127.0.0.1:25195", "127.0.0.1:25196"} {
				listener, err := net.Listen("tcp", endpoint)
				if err != nil {
					t.Fatalf("update preflight exposed or retained a host listener at %s: %v", endpoint, err)
				}
				listener.Close()
			}
			t.Logf("manager=%s method=%s target_accepted=%v", manager.Kind(), method, err == nil)
		})
	}
}

func TestNativeCoreUpdateResourcePreflight(t *testing.T) {
	if os.Getenv("QCH_TEST_CORE_UPDATE") != "1" {
		t.Skip("requires an isolated systemd Docker node and verified kernels")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("native resource preflight tests require Docker")
	}
	input := os.Getenv("QCH_TEST_CORE_INPUT")
	verifyCoreUpdateTestArchive(t, filepath.Join(input, "Xray-linux-64.zip"), "0eec3ed4163c273d8c8bf821f7d219e257921cf584aa3fb970715e458e631978")
	verifyCoreUpdateTestArchive(t, filepath.Join(input, "mihomo-linux-amd64-v1.19.33.gz"), "132f6f3edf9a65e62f04853aed3358472e0b2b7666daa8419edfa2ddf5bb6323")
	manager := defaultSystemdServiceManager()
	executor := &Executor{Services: manager}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray} {
		t.Run(string(engine), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			base := DefaultSpecs()[engine]
			shared, err := sharedInstanceSpec(engine, base, "shr_0123456789abcdef", manager.Kind())
			if err != nil {
				t.Fatal(err)
			}
			if err := prepareSharedInstanceDirectory(engine, shared, manager.Kind()); err != nil {
				t.Fatal(err)
			}
			content := liveSharedLogConfig(engine, 25197)
			if engine == core.EngineXray {
				content = `{"log":{"loglevel":"warning"},"inbounds":[],"outbounds":[{"protocol":"freedom","tag":"direct"}],"routing":{"rules":[{"type":"field","ip":["geoip:private"],"outboundTag":"direct"},{"type":"field","domain":["geosite:cn"],"outboundTag":"direct"}]}}`
			}
			directory := filepath.Join(input, string(engine))
			for _, spec := range []EngineSpec{base, shared} {
				if err := os.WriteFile(spec.ConfigPath, []byte(content), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := executor.validateCoreUpdateCandidate(ctx, engine, spec, content, filepath.Join(directory, string(engine)), directory); err != nil {
					t.Fatalf("target rejected compatible %s: %v", spec.Service, err)
				}
			}
			if engine == core.EngineXray {
				resource := filepath.Join(directory, "geosite.dat")
				original, err := os.ReadFile(resource)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.WriteFile(resource, original, 0o644); err != nil {
						t.Error(err)
					}
				})
				if err := os.WriteFile(resource, []byte("invalid target geosite resource"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := executor.validateCoreUpdateCandidate(ctx, engine, shared, content, filepath.Join(directory, "xray"), directory); err == nil {
					t.Fatal("target accepted an incompatible staged Xray resource")
				}
			}
			t.Log("main and shared native preflight passed with target resources")
		})
	}
}

func coreUpdateTestSingBoxContent(port int, incompatible bool) string {
	outbound := map[string]any{"type": "direct", "tag": "direct"}
	if incompatible {
		outbound["override_address"], outbound["override_port"] = "127.0.0.1", 80
	}
	content, _ := json.Marshal(map[string]any{
		"log":       map[string]any{"level": "info", "timestamp": true},
		"inbounds":  []any{map[string]any{"type": "http", "listen": "127.0.0.1", "listen_port": port}},
		"outbounds": []any{outbound},
	})
	return string(content) + "\n"
}

func coreUpdateRealTestUpdater(archive []byte) *CoreUpdater {
	digest := sha256.Sum256(archive)
	metadata, _ := json.Marshal(githubRelease{TagName: "v1.13.0", Assets: []githubReleaseAsset{{
		Name: "sing-box-1.13.0-linux-amd64.tar.gz", Size: int64(len(archive)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/SagerNet/sing-box/releases/download/v1.13.0/sing-box-1.13.0-linux-amd64.tar.gz",
	}}})
	updater := NewCoreUpdater()
	updater.goarch, updater.libc = "amd64", "gnu"
	updater.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := archive
		if request.URL.Host == "api.github.com" {
			body = metadata
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
	})}
	return updater
}

type coreUpdateBindFailure struct {
	coreUpdateTransaction
	listener net.Listener
}

func (fault *coreUpdateBindFailure) Activate(ctx context.Context) (string, error) {
	var err error
	fault.listener, err = net.Listen("tcp", "127.0.0.1:25192")
	if err != nil {
		return "", err
	}
	return fault.coreUpdateTransaction.Activate(ctx)
}

func (fault *coreUpdateBindFailure) Recover(ctx context.Context) (string, error) {
	fault.closeListener()
	return fault.coreUpdateTransaction.Recover(ctx)
}

func (fault *coreUpdateBindFailure) closeListener() {
	if fault.listener != nil {
		_ = fault.listener.Close()
		fault.listener = nil
	}
}
