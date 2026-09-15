//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestImportedSingBoxLogSourceFollowsValidatedMigrationFile(t *testing.T) {
	logRoot := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previous })
	configDirectory := t.TempDir()
	configPath := filepath.Join(configDirectory, "config.json")
	content := `{"log":{"level":"info","output":"runtime.log"},"inbounds":[],"outbounds":[]}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	markerPrefix := filepath.Join(t.TempDir(), "migration")
	if err := writeCoreMigrationMarker(markerPrefix, core.EngineSingBox, coreMigrationComplete,
		coreMigrationConfigDigest(content), strings.Repeat("a", 64), "enabled", "disabled"); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{
		Specs:                 map[core.Engine]EngineSpec{core.EngineSingBox: {ConfigPath: configPath}},
		MigrationMarkerPrefix: markerPrefix,
		completedMigrations: map[core.Engine]completedCoreMigration{core.EngineSingBox: {
			Managed: EngineSpec{ConfigPath: configPath}, SourceDigest: strings.Repeat("a", 64),
		}},
		verifyCompletedMigration: func(context.Context, EngineSpec, EngineSpec, *ServiceManager) error { return nil },
	}
	collector := NewCoreLogCollectorForExecutor(executor)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && collector.Status()[core.EngineSingBox].Status != "waiting" {
		time.Sleep(25 * time.Millisecond)
	}
	path := filepath.Join(logRoot, "runtime.log")
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(path, []byte("2026-08-24 INFO imported start\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry, ok := waitForLine(t, collector, "2026-08-24 INFO imported start")
	if !ok || entry.Engine != core.EngineSingBox || entry.Level != "info" {
		t.Fatalf("imported log entry = %+v, ok=%v", entry, ok)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("2026-08-24 ERROR after rotate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry, ok = waitForLine(t, collector, "2026-08-24 ERROR after rotate")
	if !ok || entry.Level != "error" {
		t.Fatalf("rotated imported log entry = %+v, ok=%v", entry, ok)
	}
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("2026-08-24 WARN after truncate\n")
	_ = file.Close()
	entry, ok = waitForLine(t, collector, "2026-08-24 WARN after truncate")
	if !ok || entry.Level != "warning" {
		t.Fatalf("truncated imported log entry = %+v, ok=%v", entry, ok)
	}
	if err := os.WriteFile(configPath, []byte(`{"log":{"output":"other.log"},"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && collector.Status()[core.EngineSingBox].Status != "failed" {
		time.Sleep(50 * time.Millisecond)
	}
	if status := collector.Status()[core.EngineSingBox]; status.Status != "failed" {
		t.Fatalf("configuration drift status = %+v", status)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("imported file collector leaked after cancellation")
	}
}

func TestImportedSingBoxConsoleOutputsKeepManagedServiceSource(t *testing.T) {
	for _, managerKind := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		for _, logging := range []string{
			`{"log":{"level":"info","timestamp":true}}`,
			`{"log":{"output":"stdout"}}`,
			`{"log":{"output":"stderr"}}`,
			`{"log":{"disabled":true,"output":"runtime.log"}}`,
		} {
			t.Run(managerKind+"/"+logging, func(t *testing.T) {
				manager, err := NewServiceManager(managerKind)
				if err != nil {
					t.Fatal(err)
				}
				configPath := filepath.Join(t.TempDir(), "config.json")
				if err := os.WriteFile(configPath, []byte(logging), 0o600); err != nil {
					t.Fatal(err)
				}
				service := "qagent-sing-box.service"
				wantKind := "journal"
				if managerKind == ServiceManagerOpenRC {
					service = "qagent-sing-box"
					wantKind = "openrc"
				}
				executor := &Executor{Specs: map[core.Engine]EngineSpec{
					core.EngineSingBox: {ConfigPath: configPath, Service: service},
				}, Services: manager}
				collector := NewCoreLogCollectorForServiceManager(manager, executor.Specs)
				if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
					t.Fatal(err)
				}
				collector.mu.Lock()
				gotKind := collector.preferredKind[core.EngineSingBox]
				fileCount := 0
				for _, source := range collector.fileSources {
					if source.kind == "file" {
						fileCount++
					}
				}
				collector.mu.Unlock()
				if gotKind != wantKind || fileCount != 0 {
					t.Fatalf("console source kind=%q files=%d, want %q and zero files", gotKind, fileCount, wantKind)
				}
			})
		}
	}
}

func TestImportedSingBoxConsoleTransitionWaitsForCollectorReadiness(t *testing.T) {
	content := `{"log":{"level":"info","timestamp":true}}`
	for _, test := range []struct {
		name, manager, kind, status string
		wantErr                     bool
	}{
		{name: "systemd active", manager: ServiceManagerSystemd, kind: "journal", status: "active"},
		{name: "systemd failed", manager: ServiceManagerSystemd, kind: "journal", status: "failed", wantErr: true},
		{name: "openrc armed", manager: ServiceManagerOpenRC, kind: "openrc", status: "waiting"},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, err := NewServiceManager(test.manager)
			if err != nil {
				t.Fatal(err)
			}
			service := "qagent-sing-box.service"
			if test.manager == ServiceManagerOpenRC {
				service = "qagent-sing-box"
			}
			collector := NewCoreLogCollectorForServiceManager(manager, map[core.Engine]EngineSpec{
				core.EngineSingBox: {Service: service},
			})
			result := make(chan error, 1)
			waitContext := context.Background()
			if test.wantErr {
				var cancel context.CancelFunc
				waitContext, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
				defer cancel()
			}
			go func() {
				result <- collector.PrepareImportedSingBoxSource(waitContext, nil, content)
			}()
			select {
			case err := <-result:
				t.Fatalf("readiness barrier returned before collector status: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			code := ""
			if test.status == "failed" {
				code = "collector-unavailable"
			}
			collector.setSourceStatus(core.EngineSingBox, test.kind, test.status, code)
			err = <-result
			if (err != nil) != test.wantErr {
				t.Fatalf("readiness result = %v, wantErr=%v", err, test.wantErr)
			}
		})
	}
}

func TestImportedSingBoxFileSourceCapturesOnlyMigrationWindow(t *testing.T) {
	logRoot := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previous })
	path := filepath.Join(logRoot, "runtime.log")
	if err := os.WriteFile(path, []byte("old deployment log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := `{"log":{"output":"runtime.log"}}`
	executor := newImportedSingBoxLogExecutor(t, content)
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	if err := collector.PrepareImportedSingBoxSource(context.Background(), executor, content); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("migration startup log\n")
	_ = file.Close()
	if err := collector.CompleteImportedSingBoxSource(executor, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := waitForLine(t, collector, "migration startup log"); !ok {
		t.Fatal("migration-window startup log was not collected")
	}
	if batch := collector.NextBatch(); batch != nil {
		for _, entry := range batch.Entries {
			if entry.Message == "old deployment log" {
				t.Fatal("pre-window log content leaked")
			}
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("migration-window collector leaked")
	}
}

func TestImportedSingBoxSourceRequiresVerifiedOwnershipAndReplacesAtomically(t *testing.T) {
	logRoot := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previous })
	content := `{"log":{"output":"runtime.log"}}`
	executor := newImportedSingBoxLogExecutor(t, content)
	delete(executor.completedMigrations, core.EngineSingBox)
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	if err := collector.RefreshImportedSingBoxSource(executor); err == nil {
		t.Fatal("complete marker without verified migration ownership was accepted")
	}
	if status := collector.Status()[core.EngineSingBox]; status.Status != "failed" {
		t.Fatalf("unverified ownership status = %+v", status)
	}

	executor = newImportedSingBoxLogExecutor(t, content)
	collector = NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
				t.Errorf("concurrent refresh: %v", err)
			}
		}()
	}
	group.Wait()
	replacement := `{"log":{"output":"replacement.log"}}`
	if err := os.WriteFile(executor.Specs[core.EngineSingBox].ConfigPath, []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
		t.Fatal(err)
	}
	collector.mu.Lock()
	fileSources := 0
	filePath := ""
	for _, source := range collector.fileSources {
		if source.kind == "file" {
			fileSources++
			filePath = source.path
		}
	}
	collector.mu.Unlock()
	if fileSources != 1 || filePath != filepath.Join(logRoot, "replacement.log") {
		t.Fatalf("replacement file sources=%d path=%q", fileSources, filePath)
	}
	console := `{"log":{"level":"info","timestamp":true}}`
	if err := os.WriteFile(executor.Specs[core.EngineSingBox].ConfigPath, []byte(console), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
		t.Fatal(err)
	}
	collector.mu.Lock()
	active := collector.activeFiles[string(core.EngineSingBox)+"\x00file"]
	preferred := collector.preferredKind[core.EngineSingBox]
	collector.mu.Unlock()
	if active != nil || preferred != "journal" {
		t.Fatalf("source switch left active file=%v preferred=%q", active != nil, preferred)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("atomic replacement collector leaked")
	}
}

func TestImportedSingBoxFileSourceStopsOnServiceOwnershipDrift(t *testing.T) {
	logRoot := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previous })
	content := `{"log":{"output":"runtime.log"}}`
	executor := newImportedSingBoxLogExecutor(t, content)
	var driftMu sync.Mutex
	drifted := false
	executor.verifyCompletedMigration = func(context.Context, EngineSpec, EngineSpec, *ServiceManager) error {
		driftMu.Lock()
		defer driftMu.Unlock()
		if drifted {
			return os.ErrPermission
		}
		return nil
	}
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	if err := collector.PrepareImportedSingBoxSource(context.Background(), executor, content); err != nil {
		t.Fatal(err)
	}
	if err := collector.CompleteImportedSingBoxSource(executor, true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	path := filepath.Join(logRoot, "runtime.log")
	if err := os.WriteFile(path, []byte("before drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := waitForLine(t, collector, "before drift"); !ok {
		t.Fatal("pre-drift line was not collected")
	}
	driftMu.Lock()
	drifted = true
	driftMu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && collector.Status()[core.EngineSingBox].Status != "failed" {
		time.Sleep(50 * time.Millisecond)
	}
	if status := collector.Status()[core.EngineSingBox]; status.Status != "failed" {
		t.Fatalf("service ownership drift status = %+v", status)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ownership-drift collector leaked")
	}
}

func TestImportedSingBoxFileSourceRevalidatesDuringContinuousWrites(t *testing.T) {
	logRoot := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previous })
	content := `{"log":{"output":"runtime.log"}}`
	executor := newImportedSingBoxLogExecutor(t, content)
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	if err := collector.PrepareImportedSingBoxSource(context.Background(), executor, content); err != nil {
		t.Fatal(err)
	}
	if err := collector.CompleteImportedSingBoxSource(executor, true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	path := filepath.Join(logRoot, "runtime.log")
	writer, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		line := strings.Repeat("x", 1024) + "\n"
		for ctx.Err() == nil {
			if _, err := writer.WriteString(line); err != nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && collector.Status()[core.EngineSingBox].Status != "active" {
		time.Sleep(25 * time.Millisecond)
	}
	if err := os.WriteFile(executor.Specs[core.EngineSingBox].ConfigPath, []byte(`{"log":{"output":"other.log"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && collector.Status()[core.EngineSingBox].Status != "failed" {
		time.Sleep(25 * time.Millisecond)
	}
	if status := collector.Status()[core.EngineSingBox]; status.Status != "failed" {
		t.Fatalf("continuous-write drift status = %+v", status)
	}
	cancel()
	_ = writer.Close()
	<-writeDone
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("continuous-write drift leaked collector")
	}
}

func TestImportedSingBoxSourceRegistrationIsConcurrentAndCancelable(t *testing.T) {
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), map[core.Engine]EngineSpec{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	source := coreLogFileSource{path: filepath.Join(t.TempDir(), "missing.log"), root: t.TempDir(), engine: core.EngineSingBox, kind: "file"}
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() { defer group.Done(); collector.startFileSource(source) }()
	}
	group.Wait()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent source cancellation leaked")
	}
}
