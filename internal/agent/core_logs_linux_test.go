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
	}
	collector := NewCoreLogCollectorForExecutor(executor)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
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
	deadline := time.Now().Add(5 * time.Second)
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
	if err := os.Symlink(filepath.Join(logRoot, "target.log"), path); err != nil {
		t.Fatal(err)
	}
	source := coreLogFileSource{path: path, root: logRoot, engine: core.EngineSingBox, kind: "test"}
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
	if err := os.Chmod(logRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	if file, err := openValidatedCoreLogFile(source); err == nil {
		file.Close()
		t.Fatal("writable imported log source parent was accepted")
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
	sources := coreLogJournalSources(map[core.Engine]EngineSpec{
		core.EngineMihomo:  {Service: "qagent-mihomo.service"},
		core.EngineXray:    {Service: "xray.service"},
		core.EngineSingBox: {Service: "sing-box.service"},
	})
	if len(sources) != 2 {
		t.Fatalf("source count = %d, want 2", len(sources))
	}
	managed, generic := sources[0], sources[1]
	if !containsArgument(managed.arguments, "--namespace=qagent-cores") ||
		!containsArgument(managed.arguments, "--unit=qagent-mihomo.service") {
		t.Fatalf("managed journal arguments = %v", managed.arguments)
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
		core.EngineXray:    {Service: "qagent-xray"},
		core.EngineSingBox: {Service: "sing-box"},
		core.EngineMihomo:  {Service: "qagent-mihomo"},
	})
	if len(sources) != 2 {
		t.Fatalf("managed file sources = %+v", sources)
	}
	found := map[core.Engine]string{}
	for _, source := range sources {
		found[source.engine] = source.path
	}
	if found[core.EngineXray] != filepath.Join(openRCCoreLogRoot, "qagent-xray.log") ||
		found[core.EngineMihomo] != filepath.Join(openRCCoreLogRoot, "qagent-mihomo.log") {
		t.Fatalf("managed file source paths = %+v", found)
	}
	if _, ok := found[core.EngineSingBox]; ok {
		t.Fatalf("unmanaged service should not produce a file source: %+v", found)
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
