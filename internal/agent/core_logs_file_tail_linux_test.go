//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestOpenRCConsoleReadinessFollowsInitializedFileCursor(t *testing.T) {
	logRoot := t.TempDir()
	previous := openRCCoreLogRoot
	openRCCoreLogRoot = logRoot
	t.Cleanup(func() { openRCCoreLogRoot = previous })
	path := filepath.Join(logRoot, "qagent-sing-box.log")
	if err := os.WriteFile(path, []byte("history before collector\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewServiceManager(ServiceManagerOpenRC)
	if err != nil {
		t.Fatal(err)
	}
	collector := NewCoreLogCollectorForServiceManager(manager, map[core.Engine]EngineSpec{
		core.EngineSingBox: {Service: "qagent-sing-box"},
	})
	opened := make(chan struct{})
	releaseCursor := make(chan struct{})
	var openedOnce sync.Once
	collector.fileSources[0].beforeInitialCursor = func() {
		openedOnce.Do(func() {
			close(opened)
			<-releaseCursor
		})
	}
	t.Cleanup(func() {
		select {
		case <-releaseCursor:
		default:
			close(releaseCursor)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("OpenRC reader-ready fixture leaked")
		}
	}()
	ready := make(chan error, 1)
	go func() {
		ready <- collector.PrepareImportedSingBoxSource(context.Background(), nil,
			`{"log":{"level":"info","timestamp":true}}`)
	}()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("OpenRC reader did not reach initial cursor setup")
	}
	select {
	case err := <-ready:
		t.Fatalf("OpenRC reported readiness before its no-history cursor was initialized: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseCursor)
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("startup immediately after ready\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if entry, ok := waitForLine(t, collector, "startup immediately after ready"); !ok || entry.Engine != core.EngineSingBox {
		t.Fatalf("OpenRC startup entry after reader readiness = %+v, ok=%v", entry, ok)
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
