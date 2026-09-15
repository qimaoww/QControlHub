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

func TestFileReadersRejectMetadataDriftAfterValidatedOpen(t *testing.T) {
	for _, sourceKind := range []string{"openrc", "imported"} {
		for _, driftKind := range []string{"mode", "owner"} {
			t.Run(sourceKind+"/"+driftKind, func(t *testing.T) {
				if driftKind == "owner" && os.Geteuid() != 0 {
					t.Skip("owner drift requires root")
				}
				logRoot := t.TempDir()
				var collector *CoreLogCollector
				var executor *Executor
				var path string
				if sourceKind == "openrc" {
					previous := openRCCoreLogRoot
					openRCCoreLogRoot = logRoot
					t.Cleanup(func() { openRCCoreLogRoot = previous })
					manager, err := NewServiceManager(ServiceManagerOpenRC)
					if err != nil {
						t.Fatal(err)
					}
					path = filepath.Join(logRoot, "qagent-sing-box.log")
					collector = NewCoreLogCollectorForServiceManager(manager, map[core.Engine]EngineSpec{
						core.EngineSingBox: {Service: "qagent-sing-box"},
					})
				} else {
					previous := importedSingBoxLogRoot
					importedSingBoxLogRoot = logRoot
					t.Cleanup(func() { importedSingBoxLogRoot = previous })
					content := `{"log":{"output":"runtime.log"}}`
					executor = newImportedSingBoxLogExecutor(t, content)
					collector = NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
					collector.sources = nil
					path = filepath.Join(logRoot, "runtime.log")
				}
				if err := os.WriteFile(path, []byte("history before validated open\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if executor != nil {
					content := `{"log":{"output":"runtime.log"}}`
					if err := collector.PrepareImportedSingBoxSource(context.Background(), executor, content); err != nil {
						t.Fatal(err)
					}
					if err := collector.CompleteImportedSingBoxSource(executor, true); err != nil {
						t.Fatal(err)
					}
				}
				collector.mu.Lock()
				sourceIndex := -1
				for index := range collector.fileSources {
					if collector.fileSources[index].engine == core.EngineSingBox && collector.fileSources[index].path == path {
						sourceIndex = index
						break
					}
				}
				collector.mu.Unlock()
				if sourceIndex < 0 {
					t.Fatal("sing-box file source was not configured")
				}

				driftResult := make(chan error, 1)
				stopWriter := make(chan struct{})
				writerDone := make(chan struct{})
				var driftOnce sync.Once
				collector.fileSources[sourceIndex].beforeInitialCursor = func() {
					driftOnce.Do(func() {
						writer, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
						if err == nil {
							if driftKind == "mode" {
								err = os.Chmod(path, 0o666)
							} else {
								err = os.Chown(path, 65534, 65534)
							}
						}
						driftResult <- err
						if err != nil {
							if writer != nil {
								_ = writer.Close()
							}
							close(writerDone)
							return
						}
						go func() {
							defer close(writerDone)
							defer writer.Close()
							ticker := time.NewTicker(2 * time.Millisecond)
							defer ticker.Stop()
							for {
								select {
								case <-stopWriter:
									return
								case <-ticker.C:
									if _, err := writer.WriteString("must not publish after unsafe metadata drift\n"); err != nil {
										return
									}
								}
							}
						}()
					})
				}

				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() { collector.Run(ctx); close(done) }()
				select {
				case err := <-driftResult:
					if err != nil {
						cancel()
						<-done
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					cancel()
					<-done
					t.Fatal("file reader did not reach the validated-open drift boundary")
				}
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) && collector.Status()[core.EngineSingBox].Status != "failed" {
					time.Sleep(10 * time.Millisecond)
				}
				close(stopWriter)
				<-writerDone
				if status := collector.Status()[core.EngineSingBox]; status.Status != "failed" {
					cancel()
					<-done
					t.Fatalf("metadata drift source status = %+v", status)
				}
				if batch := collector.NextBatch(); batch != nil {
					cancel()
					<-done
					t.Fatalf("unsafe metadata drift published a core log batch: %+v", batch)
				}
				if sourceKind == "openrc" {
					waitContext, waitCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
					err := collector.PrepareImportedSingBoxSource(waitContext, nil, `{"log":{"level":"info"}}`)
					waitCancel()
					if err == nil {
						cancel()
						<-done
						t.Fatal("unsafe OpenRC reader was reported ready")
					}
				}
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("metadata drift collector leaked")
				}
				collector.mu.Lock()
				activeCount := len(collector.activeFiles)
				collector.mu.Unlock()
				if activeCount != 0 {
					t.Fatalf("metadata drift left %d active file readers", activeCount)
				}
			})
		}
	}
}
