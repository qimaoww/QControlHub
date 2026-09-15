//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestImportTaskCapturesSingBoxStartupFileBeforeResultDelivery(t *testing.T) {
	logRoot := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previous })
	content := `{"log":{"level":"info","timestamp":true,"output":"runtime.log"}}`
	executor := newImportedSingBoxLogExecutor(t, content)
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	client := &Client{
		config:   ClientConfig{StatePath: filepath.Join(t.TempDir(), "agent-state.json")},
		executor: executor,
		logs:     collector,
		executeFunc: func(context.Context, core.Task) (string, error) {
			return "imported", os.WriteFile(filepath.Join(logRoot, "runtime.log"), []byte("managed startup ready\n"), 0o600)
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	result := client.resultForTask(context.Background(), core.Task{
		ID: "tsk_0123456789abcdef", LeaseID: "lease_0123456789abcdef",
		Action: core.ActionImportExisting, Engine: core.EngineSingBox, ConfigContent: content,
	})
	if !result.Success {
		t.Fatalf("import result = %+v", result)
	}
	if _, ok := waitForLine(t, collector, "managed startup ready"); !ok {
		t.Fatal("startup line written during import did not reach the WSS batch queue")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("client import collector leaked")
	}
}

func TestDeployTaskOwnsSingBoxLogTransitionExactlyOnce(t *testing.T) {
	oldContent := `{"log":{"level":"info","output":"runtime.log"}}`
	for index, test := range []struct {
		name        string
		target      string
		token       string
		wantSuccess bool
		wantFile    bool
		execute     func(t *testing.T, executor *Executor, logRoot, oldContent, target, token string) error
	}{
		{
			name: "same path file success", target: `{"log":{"level":"debug","output":"runtime.log"}}`,
			token: "same-path-transition-once", wantSuccess: true, wantFile: true,
			execute: func(t *testing.T, executor *Executor, logRoot, _, target, token string) error {
				writeSingBoxTransitionConfig(t, executor, target)
				appendSingBoxTransitionLine(t, filepath.Join(logRoot, "runtime.log"), token)
				time.Sleep(1200 * time.Millisecond)
				return nil
			},
		},
		{
			name: "different path file success", target: `{"log":{"level":"info","output":"replacement.log"}}`,
			token: "replacement-startup-once", wantSuccess: true, wantFile: true,
			execute: func(t *testing.T, executor *Executor, logRoot, _, target, token string) error {
				writeSingBoxTransitionConfig(t, executor, target)
				appendSingBoxTransitionLine(t, filepath.Join(logRoot, "replacement.log"), token)
				return nil
			},
		},
		{
			name: "same path truncated success", target: `{"log":{"level":"warning","output":"runtime.log"}}`,
			token: "truncated-startup-once", wantSuccess: true, wantFile: true,
			execute: func(t *testing.T, executor *Executor, logRoot, _, target, token string) error {
				writeSingBoxTransitionConfig(t, executor, target)
				if err := os.Truncate(filepath.Join(logRoot, "runtime.log"), 0); err != nil {
					t.Fatal(err)
				}
				appendSingBoxTransitionLine(t, filepath.Join(logRoot, "runtime.log"), token)
				return nil
			},
		},
		{
			name: "failed deploy rollback", target: `{"log":{"level":"info","output":"failed.log"}}`,
			token: "rollback-startup-once", wantSuccess: false, wantFile: true,
			execute: func(t *testing.T, executor *Executor, logRoot, oldContent, target, token string) error {
				writeSingBoxTransitionConfig(t, executor, target)
				// On the previous implementation this lets the old binding enter
				// its retry interval before the rollback restores runtime.log.
				time.Sleep(1200 * time.Millisecond)
				writeSingBoxTransitionConfig(t, executor, oldContent)
				appendSingBoxTransitionLine(t, filepath.Join(logRoot, "runtime.log"), token)
				return errors.New("simulated managed restart failure")
			},
		},
		{
			name: "file to console", target: `{"log":{"level":"info","timestamp":true}}`,
			wantSuccess: true, wantFile: false,
			execute: func(t *testing.T, executor *Executor, _, _, target, _ string) error {
				writeSingBoxTransitionConfig(t, executor, target)
				return nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			logRoot := t.TempDir()
			previous := importedSingBoxLogRoot
			importedSingBoxLogRoot = logRoot
			t.Cleanup(func() { importedSingBoxLogRoot = previous })
			executor := newImportedSingBoxLogExecutor(t, oldContent)
			if err := os.WriteFile(filepath.Join(logRoot, "runtime.log"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
			// Keep the console readiness path deterministic without starting a
			// host journalctl process; the file reader remains the real runtime.
			collector.sources = nil
			collector.setSourceStatus(core.EngineSingBox, "journal", "active", "")
			if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { collector.Run(ctx); close(done) }()
			waitForCoreLogSourceStatus(t, collector, core.EngineSingBox, "active")
			key := string(core.EngineSingBox) + "\x00file"
			collector.mu.Lock()
			oldRun := collector.activeFiles[key]
			collector.mu.Unlock()
			if oldRun == nil {
				t.Fatal("initial imported file watcher was not active")
			}

			client := &Client{
				config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "agent-state.json")}, executor: executor, logs: collector,
				executeFunc: func(context.Context, core.Task) (string, error) {
					return "transition attempted", test.execute(t, executor, logRoot, oldContent, test.target, test.token)
				},
			}
			result := client.resultForTask(context.Background(), core.Task{
				ID: fmt.Sprintf("tsk_transition_%02d", index), LeaseID: fmt.Sprintf("lease_transition_%02d", index),
				Action: core.ActionDeploy, Engine: core.EngineSingBox, ConfigContent: test.target,
			})
			if result.Success != test.wantSuccess {
				t.Fatalf("deploy result = %+v, want success=%v", result, test.wantSuccess)
			}
			select {
			case <-oldRun.done:
			default:
				t.Fatal("previous file watcher remained alive after transition completion")
			}

			if test.wantFile {
				waitForCoreLogSourceStatus(t, collector, core.EngineSingBox, "active")
				count := 0
				if test.name == "same path file success" {
					count = deliverCoreLogTransitionAcrossReconnect(t, client, collector, test.token)
				} else {
					count = collectCoreLogTransitionToken(t, collector, test.token)
				}
				if count != 1 {
					t.Fatalf("transition token count = %d, want exactly one", count)
				}
				collector.mu.Lock()
				currentRun := collector.activeFiles[key]
				collector.mu.Unlock()
				if currentRun == nil || currentRun == oldRun || currentRun.source.epoch <= oldRun.source.epoch {
					t.Fatalf("source epoch did not advance: old=%+v current=%+v", oldRun, currentRun)
				}
				collector.setFileSourceStatus(oldRun.source, "failed", "collector-failed")
				if status := collector.Status()[core.EngineSingBox]; status.Status != "active" {
					t.Fatalf("stale source epoch overwrote current status: %+v", status)
				}
			} else {
				collector.mu.Lock()
				active := collector.activeFiles[key]
				preferred := collector.preferredKind[core.EngineSingBox]
				collector.mu.Unlock()
				if active != nil || preferred != "journal" {
					t.Fatalf("file-to-console transition left watcher=%v preferred=%q", active != nil, preferred)
				}
			}

			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("transition collector leaked after cancellation")
			}
			collector.mu.Lock()
			activeCount := len(collector.activeFiles)
			collector.mu.Unlock()
			if activeCount != 0 {
				t.Fatalf("transition collector retained %d active file runs", activeCount)
			}
		})
	}
}
