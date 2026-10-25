//go:build linux

package agent

// Regression coverage for source recovery, selection and per-instance health.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedHostJournalFailureClearsActive(t *testing.T) {
	directory := t.TempDir()
	helper := filepath.Join(directory, "journalctl")
	trigger := filepath.Join(directory, "fail-host")
	observed := filepath.Join(directory, "host-failed")
	script := `#!/bin/sh
set -eu
capture=0
namespace=0
for argument in "$@"; do
 case "$argument" in --show-cursor) capture=1 ;; --namespace=qagent-cores) namespace=1 ;; esac
done
if [ "$capture" = 1 ]; then echo '-- cursor: review-cursor'; exit 0; fi
if [ "$namespace" = 1 ]; then while :; do sleep 0.05; done; fi
if [ ! -e "$QCH_REVIEW_FAIL_HOST" ]; then
 echo '{"MESSAGE":"shared host delivered","_SYSTEMD_UNIT":"qagent-xray@shr_0123456789abcdef.service","PRIORITY":"6","__CURSOR":"review-host-line"}'
 while [ ! -e "$QCH_REVIEW_FAIL_HOST" ]; do sleep 0.05; done
fi
echo 'host journal read failure' >&2
touch "$QCH_REVIEW_HOST_FAILED"
exit 1
`
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := journalctlPath
	journalctlPath = helper
	t.Cleanup(func() { journalctlPath = previous })
	t.Setenv("QCH_REVIEW_FAIL_HOST", trigger)
	t.Setenv("QCH_REVIEW_HOST_FAILED", observed)
	const share = "shr_0123456789abcdef"
	const unit = "qagent-xray@" + share + ".service"
	root := NewCoreLogCollector(map[core.Engine]EngineSpec{core.EngineXray: {Service: "qagent-xray.service"}})
	child := NewCoreLogCollector(map[core.Engine]EngineSpec{core.EngineXray: {Service: unit}})
	child.parent, child.instanceID, child.instanceService = root, share, unit
	key := string(core.EngineXray) + "/" + share
	root.instances = map[string]*coreLogInstanceRun{key: {collector: child, spec: EngineSpec{Service: unit}, engine: core.EngineXray, shareID: share}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	for _, namespace := range []bool{true, false} {
		go func(namespace bool) {
			root.runSharedJournal(ctx, namespace, make(chan struct{}, 1))
			done <- struct{}{}
		}(namespace)
	}
	t.Cleanup(func() {
		cancel()
		for i := 0; i < 2; i++ {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("journal follower did not stop")
			}
		}
	})
	waitForCoreLogStatus(t, child, core.EngineXray, "active host delivery", func(s CoreLogSourceStatus) bool { return s.Status == "active" && s.Source == "journal-host" })
	if batch := root.NextBatch(); batch == nil || len(batch.Entries) != 1 || batch.Entries[0].InstanceID != share {
		t.Fatalf("missing isolated shared delivery: %+v", batch)
	}
	if err := os.WriteFile(trigger, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(observed); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(observed); err != nil {
		t.Fatal("host reader did not reach the injected failure")
	}
	// Give runSharedJournal the entire retry interval to report the failure.
	time.Sleep(2200 * time.Millisecond)
	state := root.InstanceStatuses()[key]
	t.Logf("after actual host reader exit and retry: %+v", state)
	if state.CoreLogStatus == core.CoreLogStatusActive {
		t.Error("shared journal-host remains active after an observed read failure")
	}
	if root.Status()[core.EngineXray].Status == core.CoreLogStatusActive {
		t.Error("shared delivery incorrectly credited main instance")
	}
}

func TestConsoleRefreshPreservesWorkingFallback(t *testing.T) {
	for _, kind := range []string{core.CoreLogSourceJournalHost, core.CoreLogSourceFallback} {
		for _, action := range []string{"refresh", "deploy", "start", "restart", "install", "failed-deploy"} {
			t.Run(kind+"/"+action, func(t *testing.T) {
				configPath := filepath.Join(t.TempDir(), "config.json")
				content := `{"log":{"level":"info"},"inbounds":[],"outbounds":[]}`
				if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: {Binary: "/bin/sh", ConfigPath: configPath, Service: "qagent-sing-box.service"}}}
				collector := NewCoreLogCollectorForExecutor(executor)
				collector.setSourceStatus(core.EngineSingBox, core.CoreLogSourceJournal, "waiting", "awaiting-first-line")
				collector.setSourceStatus(core.EngineSingBox, core.CoreLogSourceJournal, "failed", "collector-failed")
				collector.markCoreLogDelivery(core.EngineSingBox, kind)
				before := collector.Status()[core.EngineSingBox]
				if action == "refresh" {
					if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
						t.Fatal(err)
					}
				} else {
					client := &Client{executor: executor, logs: collector, config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "state.json")}, executeFunc: func(context.Context, core.Task) (string, error) {
						if action == "failed-deploy" {
							return "rolled back", errors.New("fixture: deployment rolled back")
						}
						return "fixture lifecycle completed", nil
					}}
					taskAction := core.Action(action)
					if action == "failed-deploy" {
						taskAction = core.ActionDeploy
					}
					result := client.resultForTask(context.Background(), core.Task{ID: "tsk_0123456789abcdef", Engine: core.EngineSingBox, Action: taskAction, ConfigContent: content})
					if result.Success == (action == "failed-deploy") {
						t.Fatalf("unexpected fixture task result: %+v", result)
					}
				}
				after := collector.Status()[core.EngineSingBox]
				t.Logf("before=%+v after=%+v", before, after)
				if after.Status != "active" || after.Source != kind || after.Error != "" {
					t.Errorf("%s replaced proven live %s with %+v", action, kind, after)
				}
			})
		}
	}
}

func TestLostManagedConfigurationReportsFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"log":{"level":"info"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: {Binary: "/bin/sh", ConfigPath: configPath, Service: "qagent-sing-box.service"}}}
	collector := NewCoreLogCollectorForExecutor(executor)
	collector.serviceState = staticCoreLogServiceState("active")
	collector.markCoreLogDelivery(core.EngineSingBox, core.CoreLogSourceJournal)
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	err := collector.RefreshImportedSingBoxSource(executor)
	state := collector.Status()[core.EngineSingBox]
	t.Logf("running service lost previously observed config: status=%+v refresh_error=%v", state, err)
	if state.Status != "failed" || err == nil {
		t.Error("loss of an observed active configuration is indistinguishable from never configured")
	}
}

func TestStoppedServiceDoesNotHideJournalReadFailure(t *testing.T) {
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{core.EngineXray: {Service: "qagent-xray.service"}})
	collector.serviceState = staticCoreLogServiceState("inactive")
	source := newCoreLogJournalSource(map[string]core.Engine{"qagent-xray.service": core.EngineXray}, true)
	err := journalCommandError("Failed to open journal: Permission denied", errors.New("exit status 1"))
	if !errors.Is(err, os.ErrPermission) {
		t.Fatal("journal stderr lost its permission-denied classification")
	}
	collector.reportJournalSourceFailure(source, "qagent-xray.service", core.EngineXray, err)
	state := collector.Status()[core.EngineXray]
	t.Logf("inactive service + actual stderr-shaped read failure: %+v", state)
	if state.Status != "failed" {
		t.Error("journal permission failure was hidden by the stopped-service classification")
	}
	collector.reportJournalSourceFailure(source, "qagent-xray.service", core.EngineXray, io.EOF)
	if state := collector.Status()[core.EngineXray]; state.Status != core.CoreLogStatusWaiting || state.Error != core.CoreLogErrorServiceInactive {
		t.Fatalf("clean EOF from a stopped service is not a read failure: %+v", state)
	}
}

func TestHostJournalDeliveryReleasesConsoleReadiness(t *testing.T) {
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{core.EngineXray: {Service: "qagent-xray@shr_0123456789abcdef.service"}})
	collector.markCoreLogDelivery(core.EngineXray, core.CoreLogSourceJournalHost)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := collector.waitForConsoleSource(ctx, core.EngineXray)
	t.Logf("healthy host readiness: %v", err)
	if err != nil {
		t.Error("a host transport that has delivered cannot satisfy the console readiness barrier")
	}
}

func TestOpenRCMissingLogAllowsFirstStartWithoutLosingStartup(t *testing.T) {
	logRoot := t.TempDir()
	previous := openRCCoreLogRoot
	openRCCoreLogRoot = logRoot
	t.Cleanup(func() { openRCCoreLogRoot = previous })
	collector := NewCoreLogCollectorForServiceManager(&ServiceManager{kind: ServiceManagerOpenRC},
		map[core.Engine]EngineSpec{core.EngineSingBox: {Service: "qagent-sing-box"}})
	collector.serviceState = staticCoreLogServiceState("inactive")
	runCoreLogCollector(t, collector)
	waitForCoreLogKindStatus(t, collector, core.EngineSingBox, core.CoreLogSourceOpenRC, core.CoreLogStatusWaiting)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := collector.waitForConsoleSource(ctx, core.EngineSingBox); err != nil {
		t.Fatalf("OpenRC service cannot create its first log because the start barrier waits for that log: %v", err)
	}
	if status := collector.Status()[core.EngineSingBox]; status.Status != core.CoreLogStatusWaiting || status.Error != core.CoreLogErrorSourceMissing {
		t.Fatalf("a future output file was reported as already active: %+v", status)
	}
	path := filepath.Join(logRoot, "qagent-sing-box.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	appendSingBoxTransitionLine(t, path, "openrc-first-start-before-reader-open")
	if _, found := waitForLine(t, collector, "openrc-first-start-before-reader-open"); !found {
		t.Fatal("the first startup line was skipped when the output file appeared")
	}
	if coreLogTransportReady(CoreLogSourceStatus{Source: core.CoreLogSourceFallback, Status: core.CoreLogStatusWaiting, Error: core.CoreLogErrorSourceMissing}) {
		t.Fatal("an absent systemd fallback was treated as a proven journal replacement")
	}
}

func TestFileRecoveryResumesUnreadOffset(t *testing.T) {
	logRoot := t.TempDir()
	previous := openRCCoreLogRoot
	openRCCoreLogRoot = logRoot
	t.Cleanup(func() { openRCCoreLogRoot = previous })
	path := filepath.Join(logRoot, "qagent-sing-box.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	collector := NewCoreLogCollectorForServiceManager(&ServiceManager{kind: ServiceManagerOpenRC}, map[core.Engine]EngineSpec{core.EngineSingBox: {Service: "qagent-sing-box"}})
	collector.serviceState = staticCoreLogServiceState("active")
	runCoreLogCollector(t, collector)
	waitForCoreLogKindStatus(t, collector, core.EngineSingBox, "openrc", "waiting")
	appendSingBoxTransitionLine(t, path, "before-transient-failure")
	if _, ok := waitForLine(t, collector, "before-transient-failure"); !ok {
		t.Fatal("initial line not collected")
	}
	if batch := collector.NextBatch(); batch != nil {
		collector.Acknowledge(batch.ID)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	waitForCoreLogKindStatus(t, collector, core.EngineSingBox, "openrc", "failed")
	appendSingBoxTransitionLine(t, path, "unread-during-failure")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForCoreLogStatus(t, collector, core.EngineSingBox, "recovered file reader", func(state CoreLogSourceStatus) bool {
		return state.Source == "openrc" && (state.Status == "waiting" || state.Status == "active")
	})
	appendSingBoxTransitionLine(t, path, "after-source-recovery")
	found, resumed := false, false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (!found || !resumed) {
		if batch := collector.NextBatch(); batch != nil {
			for _, entry := range batch.Entries {
				found = found || entry.Message == "unread-during-failure"
				resumed = resumed || entry.Message == "after-source-recovery"
			}
			collector.Acknowledge(batch.ID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	contents, _ := os.ReadFile(path)
	t.Logf("recovered status=%+v recovered_line=%v future_line=%v retained_file=%q", collector.Status()[core.EngineSingBox], found, resumed, string(contents))
	if !resumed {
		t.Error("reader did not resume future delivery")
	}
	if !found {
		t.Error("same-inode retained line produced during the failure was skipped by rearming at EOF")
	}
}

func TestRemovedSharedDeliveryDoesNotChangeMain(t *testing.T) {
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{core.EngineSingBox: {Service: "qagent-sing-box.service"}})
	collector.selectSourceStatus(core.EngineSingBox, "file", "not-configured", "config-missing")
	before := collector.Status()[core.EngineSingBox]
	// An already running journal snapshot may still return a removed instance's
	// final record before notifySharedJournals cancels that follower.
	collector.markJournalDelivery(core.CoreLogEntry{Engine: core.EngineSingBox, InstanceID: "shr_0123456789abcdef", Service: "qagent-sing-box@shr_0123456789abcdef.service"}, core.CoreLogSourceJournalHost)
	after := collector.Status()[core.EngineSingBox]
	t.Logf("main before=%+v after removed shared delivery=%+v", before, after)
	if after.Status != before.Status || after.Source != before.Source {
		t.Error("a removed shared instance's delivery was credited to main")
	}
}

func TestLifecycleMutationsRemainSerialized(t *testing.T) {
	entered := make(chan core.Action, 2)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	client := &Client{config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "state.json")}, executeFunc: func(_ context.Context, task core.Task) (string, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if old >= n || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- task.Action
		if task.Action == core.ActionInstall {
			<-release
		}
		return "isolated synthetic lifecycle completed", nil
	}}
	results := make(chan core.TaskResultRequest, 2)
	go func() {
		results <- client.resultForTask(context.Background(), core.Task{ID: "tsk_0000000000000001", Engine: core.EngineSingBox, Action: core.ActionInstall})
	}()
	if action := <-entered; action != core.ActionInstall {
		t.Fatal(action)
	}
	go func() {
		results <- client.resultForTask(context.Background(), core.Task{ID: "tsk_0000000000000002", Engine: core.EngineSingBox, Action: core.ActionDeploy})
	}()
	select {
	case action := <-entered:
		close(release)
		t.Fatalf("second mutation overlapped owner update: %s", action)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			if !result.Success {
				t.Fatal(result.Error)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("serialized mutation failed to finish")
		}
	}
	t.Logf("maximum simultaneous lifecycle mutations=%d", maximum.Load())
	if maximum.Load() != 1 {
		t.Error("mutations were not serialized")
	}
}
