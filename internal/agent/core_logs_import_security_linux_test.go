//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
	if err := collector.CompleteImportedSingBoxSource(executor, true); err != nil {
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
	if err := collector.CompleteImportedSingBoxSource(executor, true); err != nil {
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
			if err := collector.CompleteImportedSingBoxSource(executor, true); err != nil {
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
