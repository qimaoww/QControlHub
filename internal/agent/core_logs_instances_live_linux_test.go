//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Run only in the isolated node containers; never against an operator's host.
func TestSharedCoreLogLiveLifecycle(t *testing.T) {
	if os.Getenv("QCH_LOG_LIVE") != "1" {
		t.Skip("requires isolated systemd/OpenRC node and verified official kernels")
	}
	manager, err := NewServiceManager(os.Getenv("QCH_SERVICE_MANAGER"))
	if err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Specs: DefaultSpecsForServiceManager(manager.Kind()), Services: manager}
	for _, spec := range executor.Specs {
		if err := os.MkdirAll(filepath.Dir(spec.ConfigPath), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := ensureManagedCoreLogStreaming(ctx, executor.Specs, manager); err != nil {
		t.Fatal(err)
	}
	collector := NewCoreLogCollectorForExecutor(executor)
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("collector did not stop")
		}
	}()
	for attempt := 0; attempt < 100; attempt++ {
		collector.mu.Lock()
		ready := collector.runContext != nil
		collector.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for index, engine := range core.AllEngines() {
		t.Run(string(engine), func(t *testing.T) {
			specs := []EngineSpec{}
			shares := []string{"shr_a123456789abcdef", "shr_b123456789abcdef"}
			for user, share := range shares {
				spec, err := sharedInstanceSpec(engine, executor.Specs[engine], share, manager.Kind())
				if err != nil {
					t.Fatal(err)
				}
				if err := prepareSharedInstanceDirectory(engine, spec, manager.Kind()); err != nil {
					t.Fatal(err)
				}
				content := liveSharedLogConfig(engine, 24100+index*10+user)
				if err := os.WriteFile(spec.ConfigPath, []byte(content), 0o640); err != nil {
					t.Fatal(err)
				}
				if _, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, spec.ConfigPath); err != nil {
					t.Fatal(err)
				}
				if err := executor.ensureSharedInstanceService(ctx, engine, spec); err != nil {
					t.Fatal(err)
				}
				if user == 0 {
					if err := collector.prepareSharedLogSource(ctx, engine, spec); err != nil {
						t.Fatal(err)
					}
				} else { // A newly created instance is discovered while the Agent is running.
					waitLiveLogCondition(t, ctx, func() bool {
						return collector.InstanceStatuses()[string(engine)+"/"+share].CoreLogStatus == "active" || collector.InstanceStatuses()[string(engine)+"/"+share].CoreLogStatus == "waiting"
					})
				}
				if output, err := manager.command(ctx, spec.Service, core.ActionStart); err != nil {
					t.Fatalf("start: %v %s", err, output)
				}
				specs = append(specs, spec)
				waitLiveInstanceLog(t, ctx, collector, engine, share)
			}
			// Restart plus journal/file rotation must resume both identities.
			if manager.Kind() == ServiceManagerSystemd {
				if output, err := run(ctx, journalctlPath, "--namespace=qagent-cores", "--rotate"); err != nil {
					t.Fatalf("rotate: %v %s", err, output)
				}
				if output, err := run(ctx, journalctlPath, "--namespace=qagent-cores", "--vacuum-size=1K"); err != nil {
					t.Fatalf("vacuum: %v %s", err, output)
				}
				collector.notifySharedJournals()
			} else {
				collector.instancesMu.Lock()
				run := collector.instances[string(engine)+"/"+shares[0]]
				collector.instancesMu.Unlock()
				run.collector.rotateFile(run.collector.fileSources[0])
			}
			for i, spec := range specs {
				if _, err := manager.command(ctx, spec.Service, core.ActionRestart); err != nil {
					t.Fatal(err)
				}
				waitLiveInstanceLog(t, ctx, collector, engine, shares[i])
			}
			// Fail one source and continue collecting the other instance.
			if manager.Kind() == ServiceManagerOpenRC {
				path := filepath.Join(openRCCoreLogRoot, specs[0].Service+".log")
				if err := os.Chmod(path, 0o666); err != nil {
					t.Fatal(err)
				}
				waitLiveLogCondition(t, ctx, func() bool {
					return collector.InstanceStatuses()[string(engine)+"/"+shares[0]].CoreLogStatus == "failed"
				})
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
			} else {
				path := filepath.Join("/etc/systemd/system", specs[0].Service+".d")
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "99-untrusted.conf"), []byte("[Service]\nEnvironment=UNTRUSTED=1\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := run(ctx, systemctlPath, "daemon-reload"); err != nil {
					t.Fatal(err)
				}
				waitLiveLogCondition(t, ctx, func() bool {
					return collector.InstanceStatuses()[string(engine)+"/"+shares[0]].CoreLogStatus == "failed"
				})
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				if _, err := run(ctx, systemctlPath, "daemon-reload"); err != nil {
					t.Fatal(err)
				}
			}
			if manager.Kind() == ServiceManagerOpenRC {
				// File sources keep one reader per instance, so the healthy sibling
				// stays readable while the other one recovers. The shared journal is
				// one namespace-wide reader and is covered by its own recovery below.
				waitLiveLogCondition(t, ctx, func() bool {
					return collector.InstanceStatuses()[string(engine)+"/"+shares[1]].CoreLogStatus == "active"
				})
			}
			// Recovery is proven by a delivered line, not by an armed reader: wait
			// for the reader to re-arm at the current end of its source, then
			// restart the recovered service so its next entry becomes visible.
			waitLiveLogCondition(t, ctx, func() bool {
				status := collector.InstanceStatuses()[string(engine)+"/"+shares[0]].CoreLogStatus
				return status == "waiting" || status == "active"
			})
			if _, err := manager.command(ctx, specs[0].Service, core.ActionRestart); err != nil {
				t.Fatal(err)
			}
			waitLiveInstanceLog(t, ctx, collector, engine, shares[0])
			waitLiveLogCondition(t, ctx, func() bool {
				return collector.InstanceStatuses()[string(engine)+"/"+shares[0]].CoreLogStatus == "active"
			})
			for _, spec := range specs {
				if _, err := manager.command(ctx, spec.Service, core.ActionStop); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(filepath.Dir(spec.ConfigPath)); err != nil {
					t.Fatal(err)
				}
			}
			waitLiveLogCondition(t, ctx, func() bool { _, exists := collector.InstanceStatuses()[string(engine)+"/"+shares[0]]; return !exists })
			t.Logf("%s: two instances, dynamic discovery, startup, restart, rotation, pending reconnect batch, failure isolation, recovery, deletion", manager.Kind())
		})
	}
}

func waitLiveLogCondition(t *testing.T, ctx context.Context, condition func() bool) {
	t.Helper()
	until := time.Now().Add(20 * time.Second)
	for time.Now().Before(until) && ctx.Err() == nil {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, file, line, _ := runtime.Caller(1)
	t.Fatalf("live log condition timed out at %s:%d", filepath.Base(file), line)
}
func waitLiveInstanceLog(t *testing.T, ctx context.Context, c *CoreLogCollector, engine core.Engine, share string) {
	t.Helper()
	waitLiveLogCondition(t, ctx, func() bool {
		batch := c.NextBatch()
		if batch == nil {
			return false
		}
		found := false
		for _, entry := range batch.Entries {
			if entry.Engine == engine && entry.InstanceID == share {
				found = true
				if entry.Service == "" {
					t.Fatal("missing service provenance")
				}
			}
		}
		if repeated := c.NextBatch(); repeated.ID != batch.ID {
			t.Fatal("pending reconnect batch changed")
		}
		c.Acknowledge(batch.ID)
		return found
	})
}
func liveSharedLogConfig(engine core.Engine, port int) string {
	switch engine {
	case core.EngineMihomo:
		return fmt.Sprintf("mixed-port: %d\nlog-level: info\nmode: direct\n", port)
	case core.EngineXray:
		return fmt.Sprintf(`{"log":{"loglevel":"info"},"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"socks","settings":{"auth":"noauth","udp":true}}],"outbounds":[{"protocol":"freedom"}]}`, port)
	case core.EngineSingBox:
		return fmt.Sprintf(`{"log":{"level":"info"},"inbounds":[{"type":"socks","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`, port)
	default:
		return fmt.Sprintf(`{"server":"127.0.0.1","server_port":%d,"password":"fixture-password","method":"aes-256-gcm","mode":"tcp_and_udp"}`, port)
	}
}
