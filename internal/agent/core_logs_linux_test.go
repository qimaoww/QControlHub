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
	if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
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
	if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
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

func newImportedSingBoxLogExecutor(t *testing.T, content string) *Executor {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	markerPrefix := filepath.Join(t.TempDir(), "migration")
	sourceDigest := strings.Repeat("b", 64)
	if err := writeCoreMigrationMarker(markerPrefix, core.EngineSingBox, coreMigrationComplete,
		coreMigrationConfigDigest(content), sourceDigest, "enabled", "disabled"); err != nil {
		t.Fatal(err)
	}
	managed := EngineSpec{ConfigPath: configPath, Service: "qagent-sing-box.service"}
	return &Executor{
		Specs: map[core.Engine]EngineSpec{core.EngineSingBox: managed}, MigrationMarkerPrefix: markerPrefix,
		completedMigrations: map[core.Engine]completedCoreMigration{core.EngineSingBox: {
			Managed: managed, SourceDigest: sourceDigest,
		}},
		verifyCompletedMigration: func(context.Context, EngineSpec, EngineSpec, *ServiceManager) error { return nil },
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
	if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
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

func TestMissingSingBoxInstallationDoesNotReportCollectionFailure(t *testing.T) {
	for _, kind := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		t.Run(kind, func(t *testing.T) {
			manager, err := NewServiceManager(kind)
			if err != nil {
				t.Fatal(err)
			}
			service := "qagent-sing-box.service"
			if kind == ServiceManagerOpenRC {
				service = "qagent-sing-box"
			}
			executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: {
				Binary: filepath.Join(t.TempDir(), "missing-sing-box"), ConfigPath: filepath.Join(t.TempDir(), "missing-config.json"),
				Service: service,
			}}, Services: manager}
			collector := NewCoreLogCollectorForExecutor(executor)
			if status, exists := collector.Status()[core.EngineSingBox]; exists {
				t.Fatalf("missing sing-box installation reported source status: %+v", status)
			}
		})
	}
}

func TestImportedSingBoxLogSourceFailsClosed(t *testing.T) {
	logRoot := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previous })
	for _, output := range []string{"/etc/shadow", "../escape.log", logRoot, logRoot + "-other/log"} {
		if path, err := importedSingBoxLogPath(output); err == nil {
			t.Errorf("unsafe output %q resolved to %q", output, path)
		}
	}
	path := filepath.Join(logRoot, "runtime.log")
	executor := newImportedSingBoxLogExecutor(t, `{"log":{"output":"runtime.log"}}`)
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
		t.Fatal(err)
	}
	collector.mu.Lock()
	var source coreLogFileSource
	for _, candidate := range collector.fileSources {
		if candidate.kind == "file" {
			source = candidate
		}
	}
	collector.mu.Unlock()
	if source.path != path {
		t.Fatalf("validated file source path = %q, want %q", source.path, path)
	}
	if err := os.Symlink(filepath.Join(logRoot, "target.log"), path); err != nil {
		t.Fatal(err)
	}
	if file, err := openValidatedCoreLogFile(source); err == nil {
		file.Close()
		t.Fatal("symlinked imported log source was accepted")
	}
	_ = os.Remove(path)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if file, err := openValidatedCoreLogFile(source); err == nil {
		file.Close()
		t.Fatal("non-regular imported log source was accepted")
	}
	_ = os.Remove(path)
	if err := os.WriteFile(path, []byte("unsafe\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if file, err := openValidatedCoreLogFile(source); err == nil {
		file.Close()
		t.Fatal("group/other-writable imported log source was accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(path, 65534, -1); err != nil {
			t.Fatal(err)
		}
		if file, err := openValidatedCoreLogFile(source); err == nil {
			file.Close()
			t.Fatal("foreign-owned imported log source was accepted")
		}
		if err := os.Chown(path, 0, -1); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(logRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	if file, err := openValidatedCoreLogFile(source); err == nil {
		file.Close()
		t.Fatal("writable imported log source parent was accepted")
	}
}

func TestImportedSingBoxLogSourceRejectsHardLinks(t *testing.T) {
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
	outside := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(outside, []byte("must not be collected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logRoot, "runtime.log")
	if err := os.Link(outside, path); err != nil {
		t.Fatal(err)
	}
	if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && collector.Status()[core.EngineSingBox].Status != "failed" {
		time.Sleep(25 * time.Millisecond)
	}
	if status := collector.Status()[core.EngineSingBox]; status.Status != "failed" {
		t.Fatalf("hard-linked source status = %+v", status)
	}
	if batch := collector.NextBatch(); batch != nil {
		t.Fatalf("hard-linked outside contents reached batch: %+v", batch)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hard-link rejection leaked collector")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("before replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor = newImportedSingBoxLogExecutor(t, content)
	collector = NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	if err := collector.PrepareImportedSingBoxSource(context.Background(), executor, content); err != nil {
		t.Fatal(err)
	}
	if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	replacementOutside := filepath.Join(t.TempDir(), "replacement-outside.log")
	if err := os.WriteFile(replacementOutside, []byte("replacement must not be collected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(replacementOutside, path); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && collector.Status()[core.EngineSingBox].Status != "failed" {
		time.Sleep(25 * time.Millisecond)
	}
	if status := collector.Status()[core.EngineSingBox]; status.Status != "failed" {
		t.Fatalf("hard-linked replacement status = %+v", status)
	}
	if batch := collector.NextBatch(); batch != nil {
		for _, entry := range batch.Entries {
			if strings.Contains(entry.Message, "replacement must not be collected") {
				t.Fatalf("hard-linked replacement reached batch: %+v", batch)
			}
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hard-link replacement leaked collector")
	}
}

func TestImportedSingBoxActiveSourceRejectsHardLinkBeforePublish(t *testing.T) {
	for _, test := range []struct {
		name    string
		token   string
		payload string
	}{
		{name: "complete line", token: "external-complete-line", payload: "external-complete-line\n"},
		{name: "partial line", token: "external-partial-line", payload: "external-partial-line"},
		{
			name:    "oversized partial line",
			token:   "external-oversized-partial",
			payload: strings.Repeat("external-oversized-partial", coreLogFileMaxLine/len("external-oversized-partial")+2),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			logRoot := t.TempDir()
			previous := importedSingBoxLogRoot
			importedSingBoxLogRoot = logRoot
			t.Cleanup(func() { importedSingBoxLogRoot = previous })

			content := `{"log":{"output":"runtime.log"}}`
			executor := newImportedSingBoxLogExecutor(t, content)
			path := filepath.Join(logRoot, "runtime.log")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
			if err := collector.PrepareImportedSingBoxSource(context.Background(), executor, content); err != nil {
				t.Fatal(err)
			}
			if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { collector.Run(ctx); close(done) }()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) && collector.Status()[core.EngineSingBox].Status != "active" {
				time.Sleep(25 * time.Millisecond)
			}
			if status := collector.Status()[core.EngineSingBox]; status.Status != "active" {
				cancel()
				<-done
				t.Fatalf("file source did not become active: %+v", status)
			}

			// Let the active reader settle at EOF so the link and write happen
			// before its next read. The write still comes through the external
			// hard link to the inode that is already open by the collector.
			time.Sleep(100 * time.Millisecond)
			externalLink := filepath.Join(t.TempDir(), "external.log")
			if err := os.Link(path, externalLink); err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(externalLink, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString(test.payload); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}

			deadline = time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) && collector.Status()[core.EngineSingBox].Status != "failed" {
				time.Sleep(25 * time.Millisecond)
			}
			if status := collector.Status()[core.EngineSingBox]; status.Status != "failed" {
				cancel()
				<-done
				t.Fatalf("active hard-linked source status = %+v", status)
			}

			// The reader retries after two seconds. Leaving the hard link in
			// place must keep the source failed without publishing any bytes
			// from the rejected read, including partial and oversized records.
			time.Sleep(2200 * time.Millisecond)
			if status := collector.Status()[core.EngineSingBox]; status.Status != "failed" {
				cancel()
				<-done
				t.Fatalf("hard-linked source did not remain failed: %+v", status)
			}
			for batch := collector.NextBatch(); batch != nil; batch = collector.NextBatch() {
				for _, entry := range batch.Entries {
					if strings.Contains(entry.Message, test.token) {
						cancel()
						<-done
						t.Fatalf("external hard-link content reached batch before rejection: %+v", entry)
					}
				}
				if !collector.Acknowledge(batch.ID) {
					cancel()
					<-done
					t.Fatalf("failed to acknowledge batch %q", batch.ID)
				}
			}

			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("active hard-link rejection leaked collector")
			}
		})
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

func TestDecodeJournalCoreLogMapsManagedUnitsAndPriorities(t *testing.T) {
	t.Parallel()
	value := []byte(`{"MESSAGE":"accepted connection","_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"4","__REALTIME_TIMESTAMP":"1787310000123456"}`)
	units := map[string]core.Engine{"qagent-sing-box.service": core.EngineSingBox}
	entry, _, ok := decodeJournalCoreLog(value, units)
	if !ok {
		t.Fatal("managed journal entry was rejected")
	}
	if entry.Engine != core.EngineSingBox || entry.Level != "warning" || entry.Message != "accepted connection" {
		t.Fatalf("decoded entry = %+v", entry)
	}
	if entry.LoggedAt.UnixMicro() != 1787310000123456 {
		t.Fatalf("logged_at = %s", entry.LoggedAt)
	}
	if _, _, ok := decodeJournalCoreLog([]byte(`{"MESSAGE":"ignored","_SYSTEMD_UNIT":"ssh.service","PRIORITY":"6"}`), units); ok {
		t.Fatal("unmanaged service journal was accepted")
	}
}

func TestDecodeJournalCoreLogAcceptsJournaldByteArrayMessage(t *testing.T) {
	t.Parallel()
	value := []byte(`{"MESSAGE":[27,91,51,50,109,73,78,70,79,27,91,48,109,32,105,110,98,111,117,110,100,47,109,105,120,101,100,91,102,105,120,116,117,114,101,93,32,115,116,97,114,116,101,100],"_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"6","__REALTIME_TIMESTAMP":"1787310000123456","__CURSOR":"cursor-array"}`)
	entry, cursor, ok := decodeJournalCoreLog(value, map[string]core.Engine{
		"qagent-sing-box.service": core.EngineSingBox,
	})
	if !ok || cursor != "cursor-array" || entry.Engine != core.EngineSingBox ||
		entry.Message != "\x1b[32mINFO\x1b[0m inbound/mixed[fixture] started" {
		t.Fatalf("decoded journald byte-array entry = %+v, cursor=%q, ok=%v", entry, cursor, ok)
	}
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{})
	collector.appendJournal(entry, cursor)
	batch := collector.NextBatch()
	if batch == nil || len(batch.Entries) != 1 || batch.Entries[0].Message != entry.Message {
		t.Fatalf("journald byte-array batch = %+v", batch)
	}
	for name, message := range map[string]string{
		"fractional": `[27,1.5,65]`,
		"negative":   `[-1,65]`,
		"overflow":   `[256,65]`,
		"non-number": `[27,"65"]`,
	} {
		t.Run(name, func(t *testing.T) {
			invalid := []byte(`{"MESSAGE":` + message + `,"_SYSTEMD_UNIT":"qagent-sing-box.service"}`)
			if _, _, ok := decodeJournalCoreLog(invalid, map[string]core.Engine{"qagent-sing-box.service": core.EngineSingBox}); ok {
				t.Fatalf("invalid journald byte array was accepted: %s", invalid)
			}
		})
	}
	if _, ok := journalMessageField(make([]any, coreLogFileMaxLine+1)); ok {
		t.Fatal("oversized journald byte array was accepted")
	}
}

func TestCoreLogCollectorKeepsBatchUntilAcknowledged(t *testing.T) {
	t.Parallel()
	collector := NewCoreLogCollector()
	collector.append(core.CoreLogEntry{Engine: core.EngineXray, Level: "info", Message: "first", LoggedAt: time.Now()})
	collector.append(core.CoreLogEntry{Engine: core.EngineMihomo, Level: "debug", Message: "second", LoggedAt: time.Now()})
	first := collector.NextBatch()
	if first == nil || len(first.Entries) != 2 || !strings.HasPrefix(first.ID, "log_") {
		t.Fatalf("first batch = %+v", first)
	}
	retry := collector.NextBatch()
	if retry == nil || retry.ID != first.ID || len(retry.Entries) != 2 {
		t.Fatalf("retry batch = %+v", retry)
	}
	if collector.Acknowledge("log_0000000000000000") {
		t.Fatal("collector accepted an unrelated acknowledgment")
	}
	if !collector.Acknowledge(first.ID) || collector.NextBatch() != nil {
		t.Fatal("acknowledged batch remained queued")
	}
}

func TestDecodeJournalCoreLogBoundsMessages(t *testing.T) {
	t.Parallel()
	message := strings.Repeat("a", core.MaxCoreLogMessageBytes-1) + "日志"
	value := []byte(`{"MESSAGE":"` + message + `","_SYSTEMD_UNIT":"qagent-xray.service","PRIORITY":"6"}`)
	units := map[string]core.Engine{"qagent-xray.service": core.EngineXray}
	entry, _, ok := decodeJournalCoreLog(value, units)
	if !ok || len([]byte(entry.Message)) > core.MaxCoreLogMessageBytes {
		t.Fatalf("bounded entry = %d bytes, ok=%v", len([]byte(entry.Message)), ok)
	}
	nulEntry, _, ok := decodeJournalCoreLog([]byte(`{"MESSAGE":"before\u0000after","_SYSTEMD_UNIT":"qagent-xray.service","PRIORITY":"6"}`), units)
	if !ok || strings.ContainsRune(nulEntry.Message, '\x00') {
		t.Fatalf("NUL-containing journal entry was not sanitized: %+v, ok=%v", nulEntry, ok)
	}
}

func TestCoreLogSourcesMixManagedAndExactGenericUnits(t *testing.T) {
	t.Parallel()
	sources := coreLogJournalSources(
		map[core.Engine]EngineSpec{
			core.EngineMihomo:          {Service: "qagent-mihomo.service"},
			core.EngineXray:            {Service: "qagent-xray.service"},
			core.EngineSingBox:         {Service: "qagent-sing-box.service"},
			core.EngineShadowsocksRust: {Service: "qagent-shadowsocks-rust.service"},
		},
		map[core.Engine]EngineSpec{
			core.EngineXray:    {Service: "xray.service"},
			core.EngineSingBox: {Service: "sing-box.service"},
		},
	)
	if len(sources) != 2 {
		t.Fatalf("source count = %d, want 2", len(sources))
	}
	managed, generic := sources[0], sources[1]
	if !containsArgument(managed.arguments, "--namespace=qagent-cores") {
		t.Fatalf("managed journal namespace arguments = %v", managed.arguments)
	}
	for _, unit := range []string{
		"qagent-mihomo.service", "qagent-xray.service", "qagent-sing-box.service", "qagent-shadowsocks-rust.service",
	} {
		if !containsArgument(managed.arguments, "--unit="+unit) {
			t.Fatalf("managed journal arguments omit %s: %v", unit, managed.arguments)
		}
	}
	if containsArgument(generic.arguments, "--namespace=qagent-cores") ||
		!containsArgument(generic.arguments, "--unit=xray.service") ||
		!containsArgument(generic.arguments, "--unit=sing-box.service") {
		t.Fatalf("generic journal arguments = %v", generic.arguments)
	}
	if engine, ok := coreLogEngineForUnit("xray.service", generic.unitEngines); !ok || engine != core.EngineXray {
		t.Fatalf("generic Xray mapping = %q, %t", engine, ok)
	}
	if _, ok := coreLogEngineForUnit("ssh.service", generic.unitEngines); ok {
		t.Fatal("unrelated default-namespace unit was accepted")
	}
}

func TestCoreLogSourcesRejectArbitraryCustomUnitsAndDeduplicateCursors(t *testing.T) {
	t.Parallel()
	sources := coreLogJournalSources(map[core.Engine]EngineSpec{
		core.EngineMihomo:          {Service: "qagent-xray.service"},
		core.EngineXray:            {Service: "qagent-xray.service"},
		core.EngineSingBox:         {Service: "singbox.service"},
		core.EngineShadowsocksRust: {Service: "custom-ss.service"},
	})
	if len(sources) != 1 || containsArgument(sources[0].arguments, "--unit=qagent-xray.service") ||
		containsArgument(sources[0].arguments, "--unit=custom-ss.service") {
		t.Fatalf("custom source filtering = %+v", sources)
	}
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{})
	entry := core.CoreLogEntry{Engine: core.EngineSingBox, Level: "info", Message: "once", LoggedAt: time.Now()}
	collector.appendJournal(entry, "cursor-1")
	collector.appendJournal(entry, "cursor-1")
	batch := collector.NextBatch()
	if batch == nil || len(batch.Entries) != 1 {
		t.Fatalf("deduplicated batch = %+v", batch)
	}
}

func containsArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}

func TestCoreLogFileSourcesMapOnlyManagedServices(t *testing.T) {
	sources := coreLogFileSources(map[core.Engine]EngineSpec{
		core.EngineXray:            {Service: "qagent-xray"},
		core.EngineSingBox:         {Service: "qagent-sing-box"},
		core.EngineMihomo:          {Service: "qagent-mihomo"},
		core.EngineShadowsocksRust: {Service: "qagent-shadowsocks-rust"},
	})
	if len(sources) != 4 {
		t.Fatalf("managed file sources = %+v", sources)
	}
	found := map[core.Engine]string{}
	for _, source := range sources {
		found[source.engine] = source.path
	}
	if found[core.EngineXray] != filepath.Join(openRCCoreLogRoot, "qagent-xray.log") ||
		found[core.EngineMihomo] != filepath.Join(openRCCoreLogRoot, "qagent-mihomo.log") ||
		found[core.EngineSingBox] != filepath.Join(openRCCoreLogRoot, "qagent-sing-box.log") ||
		found[core.EngineShadowsocksRust] != filepath.Join(openRCCoreLogRoot, "qagent-shadowsocks-rust.log") {
		t.Fatalf("managed file source paths = %+v", found)
	}
	if unmanaged := coreLogFileSources(map[core.Engine]EngineSpec{
		core.EngineSingBox: {Service: "sing-box"},
	}); len(unmanaged) != 0 {
		t.Fatalf("unmanaged service produced file sources: %+v", unmanaged)
	}
}

func TestCoreLogFileSourcesSkipAmbiguousServices(t *testing.T) {
	sources := coreLogFileSources(map[core.Engine]EngineSpec{
		core.EngineXray:    {Service: "qagent-xray"},
		core.EngineSingBox: {Service: "qagent-xray"},
	})
	if len(sources) != 0 {
		t.Fatalf("ambiguous file sources = %+v", sources)
	}
}

func TestCoreLogCollectorTailsOpenRCLogFiles(t *testing.T) {
	t.Parallel()
	logRoot := t.TempDir()
	previous := openRCCoreLogRoot
	openRCCoreLogRoot = logRoot
	t.Cleanup(func() { openRCCoreLogRoot = previous })

	manager, err := NewServiceManager(ServiceManagerOpenRC)
	if err != nil {
		t.Fatal(err)
	}
	specs := map[core.Engine]EngineSpec{core.EngineXray: {Service: "qagent-xray"}}
	collector := NewCoreLogCollectorForServiceManager(manager, specs)

	path := filepath.Join(logRoot, "qagent-xray.log")
	if err := os.WriteFile(path, []byte("before-start\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		collector.Run(ctx)
		close(done)
	}()

	appendLine := func(content string) {
		file, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if openErr != nil {
			t.Fatal(openErr)
		}
		if _, writeErr := file.WriteString(content); writeErr != nil {
			t.Fatal(writeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}

	// The collector must not stream pre-existing content (journal --lines=0).
	waitForLine(t, collector, "")
	appendLine("xray started\n")
	entry, ok := waitForLine(t, collector, "xray started")
	if !ok || entry.Engine != core.EngineXray || entry.Level != "info" {
		t.Fatalf("tailed entry = %+v ok=%v", entry, ok)
	}

	// Truncation (rotation) must not break the tail and freshly written lines
	// after truncation must still be delivered.
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	appendLine("after-rotate\n")
	if entry, ok := waitForLine(t, collector, "after-rotate"); !ok || entry.Engine != core.EngineXray {
		t.Fatalf("post-rotation entry = %+v ok=%v", entry, ok)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("collector did not stop")
	}
}

func waitForLine(t *testing.T, collector *CoreLogCollector, want string) (core.CoreLogEntry, bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, batch := range []*core.CoreLogBatch{collector.NextBatch()} {
			if batch == nil {
				break
			}
			var matched *core.CoreLogEntry
			for i, entry := range batch.Entries {
				if want == "" {
					t.Fatalf("unexpected entry streamed before tail start: %+v", entry)
				}
				if entry.Message == want {
					candidate := batch.Entries[i]
					matched = &candidate
				}
			}
			if matched != nil {
				return *matched, true
			}
			collector.Acknowledge(batch.ID)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if want == "" {
		return core.CoreLogEntry{}, true
	}
	return core.CoreLogEntry{}, false
}

func TestCoreLogCollectorFollowsReplacedOpenRCLogFiles(t *testing.T) {
	logRoot := t.TempDir()
	previous := openRCCoreLogRoot
	openRCCoreLogRoot = logRoot
	t.Cleanup(func() { openRCCoreLogRoot = previous })

	manager, err := NewServiceManager(ServiceManagerOpenRC)
	if err != nil {
		t.Fatal(err)
	}
	collector := NewCoreLogCollectorForServiceManager(manager, map[core.Engine]EngineSpec{
		core.EngineXray: {Service: "qagent-xray"},
	})
	path := filepath.Join(logRoot, "qagent-xray.log")
	if err := os.WriteFile(path, []byte("old-file-line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		collector.Run(ctx)
		close(done)
	}()
	time.Sleep(1500 * time.Millisecond)

	// Replace the file the way an external rename-based rotation does.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new-file-line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := waitForLine(t, collector, "new-file-line"); !ok {
		t.Fatal("line written to the replaced log file was not streamed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("collector did not stop")
	}
}
