//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDeployCompiledSingBoxLogTransition(t *testing.T) {
	requireAgentRoot(t)
	for _, target := range []string{legacyEmptySingBoxAccounting, `{"inbounds":[{"type":"socks","tag":"listener","listen":"127.0.0.1","listen_port":21001}],"outbounds":[{"type":"direct","tag":"direct"}]}`} {
		for _, kind := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
			t.Run(fmt.Sprintf("%s/empty=%v", kind, target == legacyEmptySingBoxAccounting), func(t *testing.T) {
				dir := t.TempDir()
				binary := filepath.Join(dir, "sing-box")
				helper := filepath.Join(dir, "service")
				if err := os.WriteFile(binary, []byte("#!/bin/sh\necho with_v2ray_api\n"), 0700); err != nil {
					t.Fatal(err)
				}
				script := "#!/bin/sh\nstate='" + filepath.Join(dir, "service.state") + "'\ncase \"$*\" in *restart*|*start*) echo active > \"$state\";; *stop*) echo inactive > \"$state\";; *is-active*|*status*) if [ -f \"$state\" ] && [ \"$(cat \"$state\")\" = active ]; then echo active; else echo inactive; exit 3; fi;; esac\n"
				if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				service := "qagent-sing-box.service"
				if kind == ServiceManagerOpenRC {
					service = "qagent-sing-box"
				}
				spec := EngineSpec{Binary: binary, ConfigPath: filepath.Join(dir, "config.json"), Service: service}
				executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: spec}, Services: &ServiceManager{kind: kind, executable: helper, enableExecutable: helper}}
				collector := NewCoreLogCollectorForServiceManager(executor.Services, executor.Specs)
				collector.sources = nil
				source := "journal"
				if kind == ServiceManagerOpenRC {
					source = "openrc"
				}
				collector.setSourceStatus(core.EngineSingBox, source, "active", "")
				client := &Client{config: ClientConfig{StatePath: filepath.Join(dir, "state.json")}, executor: executor, logs: collector}
				for i, id := range []string{"tsk_compiled_first", "tsk_compiled_repeat"} {
					result := client.resultForTask(context.Background(), core.Task{ID: id, Action: core.ActionDeploy, Engine: core.EngineSingBox, ConfigContent: target})
					if !result.Success || strings.Contains(result.Output, "附属步骤失败") {
						t.Fatalf("deployment %d: %+v", i, result)
					}
					actual, err := os.ReadFile(spec.ConfigPath)
					if err != nil || string(actual) == target || (target == legacyEmptySingBoxAccounting && strings.Contains(string(actual), "v2ray_api")) {
						t.Fatalf("runtime transformation missing: %s %v", actual, err)
					}
					collector.mu.Lock()
					pending := collector.transitions[core.EngineSingBox] != nil
					collector.mu.Unlock()
					if pending {
						t.Fatal("transition remained pending")
					}
				}
			})
		}
	}
}

func TestCompiledSingBoxLogTargetKeepsDriftAndTransitionGuards(t *testing.T) {
	const original = legacyEmptySingBoxAccounting
	executor := newImportedSingBoxLogExecutor(t, original)
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	collector.sources = nil
	collector.setSourceStatus(core.EngineSingBox, "journal", "active", "")
	ctx := context.Background()
	if err := collector.PrepareManagedSingBoxSource(ctx, executor, original); err != nil {
		t.Fatal(err)
	}
	bound := collector.withManagedSingBoxTarget(ctx, executor)
	spec := executor.Specs[core.EngineSingBox]
	prepared, warning := executor.prepareNativeAccountingContent(ctx, core.EngineSingBox, spec, original)
	if warning != "" || prepared == original {
		t.Fatalf("transformation: %q %s", prepared, warning)
	}
	other := spec
	other.ConfigPath += ".other"
	if err := bindPreparedCoreLogTarget(bound, core.EngineSingBox, other, coreMigrationConfigDigest(prepared)); err == nil {
		t.Fatal("accepted another instance path")
	}
	if err := bindPreparedCoreLogTarget(bound, core.EngineMihomo, spec, coreMigrationConfigDigest(prepared)); err == nil {
		t.Fatal("accepted another engine")
	}
	if err := bindPreparedCoreLogTarget(bound, core.EngineSingBox, spec, coreMigrationConfigDigest(prepared)); err != nil {
		t.Fatal(err)
	}
	writeSingBoxTransitionConfig(t, executor, prepared+"\n")
	if err := collector.CompleteManagedSingBoxSource(executor, true); err == nil {
		t.Fatal("ignored post-compilation drift")
	}
	if status := collector.Status()[core.EngineSingBox]; status.Status != "failed" {
		t.Fatalf("drift status: %+v", status)
	}
	collector.setSourceStatus(core.EngineSingBox, "journal", "active", "")
	if err := collector.PrepareManagedSingBoxSource(ctx, executor, original); err != nil {
		t.Fatal(err)
	}
	if err := bindPreparedCoreLogTarget(bound, core.EngineSingBox, spec, coreMigrationConfigDigest(prepared)); err == nil {
		t.Fatal("expired callback changed a newer transition")
	}
	// A failed attempt must compare the exact previous file, not the compiled
	// target, then release the transition so the next operation can proceed.
	if err := collector.CompleteManagedSingBoxSource(executor, false); err != nil {
		t.Fatal(err)
	}
	collector.mu.Lock()
	pending := len(collector.transitions)
	collector.mu.Unlock()
	if pending != 0 {
		t.Fatal("failed attempt retained transition")
	}
}

func TestSharedSingBoxResultDoesNotRefreshMainLogSource(t *testing.T) {
	executor := newImportedSingBoxLogExecutor(t, `{"log":{"level":"info"}}`)
	writeSingBoxTransitionConfig(t, executor, "invalid main configuration")
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	collector.sources = nil
	collector.setSourceStatus(core.EngineSingBox, "journal", "active", "")
	before := collector.Status()[core.EngineSingBox]
	client := &Client{config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "state.json")}, executor: executor, logs: collector, executeFunc: func(context.Context, core.Task) (string, error) { return "shared stopped", nil }}
	result := client.resultForTask(context.Background(), core.Task{ID: "tsk_shared_stop", Engine: core.EngineSingBox, Action: core.ActionStop, SharedInstance: true, SharedTrafficID: "shr_0123456789abcdef"})
	if !result.Success || strings.Contains(result.Output, "refresh managed sing-box") {
		t.Fatalf("shared result: %+v", result)
	}
	after := collector.Status()[core.EngineSingBox]
	if after.Status != before.Status || after.Error != before.Error {
		t.Fatalf("shared task changed main logs: before=%+v after=%+v", before, after)
	}
}

func TestCompletedSingBoxImportRebindsCompiledLogTarget(t *testing.T) {
	const original = legacyEmptySingBoxAccounting
	executor := newImportedSingBoxLogExecutor(t, original)
	spec := executor.Specs[core.EngineSingBox]
	prepared, warning := executor.prepareNativeAccountingContent(context.Background(), core.EngineSingBox, spec, original)
	if warning != "" || prepared == original {
		t.Fatalf("transformation: %s %s", prepared, warning)
	}
	writeSingBoxTransitionConfig(t, executor, prepared)
	if err := writeCoreMigrationMarkerWithRequestDigest(executor.MigrationMarkerPrefix, core.EngineSingBox, coreMigrationComplete, coreMigrationConfigDigest(prepared), coreMigrationConfigDigest(original), strings.Repeat("b", 64), "enabled", "disabled"); err != nil {
		t.Fatal(err)
	}
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	collector.sources = nil
	collector.setSourceStatus(core.EngineSingBox, "journal", "active", "")
	client := &Client{config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "state.json")}, executor: executor, logs: collector}
	result := client.resultForTask(context.Background(), core.Task{ID: "tsk_import_replay", Engine: core.EngineSingBox, Action: core.ActionImportExisting, ConfigContent: original})
	if !result.Success || strings.Contains(result.Output, "附属步骤失败") {
		t.Fatalf("completed import replay: %+v", result)
	}
}

func TestCompiledSingBoxFileSourcePreservesStartupWindow(t *testing.T) {
	logRoot := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previous })
	original := strings.Replace(legacyEmptySingBoxAccounting, `{"inbounds"`, `{"log":{"level":"info","output":"runtime.log"},"inbounds"`, 1)
	executor := newImportedSingBoxLogExecutor(t, original)
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	collector.sources = nil
	client := &Client{config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "state.json")}, executor: executor, logs: collector, executeFunc: func(ctx context.Context, task core.Task) (string, error) {
		spec := executor.Specs[task.Engine]
		prepared, warning := executor.prepareNativeAccountingContent(ctx, task.Engine, spec, task.ConfigContent)
		if warning != "" || prepared == task.ConfigContent {
			t.Fatalf("transformation: %s %s", prepared, warning)
		}
		if err := bindPreparedCoreLogTarget(ctx, task.Engine, spec, coreMigrationConfigDigest(prepared)); err != nil {
			return "", err
		}
		writeSingBoxTransitionConfig(t, executor, prepared)
		appendSingBoxTransitionLine(t, filepath.Join(logRoot, "runtime.log"), "compiled startup once")
		return "deployed", nil
	}}
	result := client.resultForTask(context.Background(), core.Task{ID: "tsk_compiled_file", Engine: core.EngineSingBox, Action: core.ActionDeploy, ConfigContent: original})
	if !result.Success || strings.Contains(result.Output, "附属步骤失败") {
		t.Fatalf("file deployment: %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("file reader leaked")
		}
	}()
	if count := collectCoreLogTransitionToken(t, collector, "compiled startup once"); count != 1 {
		t.Fatalf("startup count=%d", count)
	}
}
