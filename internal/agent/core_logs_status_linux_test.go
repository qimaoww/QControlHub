//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// coreLogJournalFixtureScript is a journalctl stand-in that decides per
// invocation which journal (managed namespace or host) it emulates.
const coreLogJournalFixtureScript = `#!/bin/sh
set -u
namespace=0
capture=0
for argument in "$@"; do
	case "$argument" in
		--namespace=qagent-cores) namespace=1 ;;
		--show-cursor) capture=1 ;;
	esac
done
if [ "$capture" = 1 ]; then
	printf '%s\n' "-- cursor: ${QCH_TEST_JOURNAL_CURSOR:-fixture-cursor}"
	exit 0
fi
if [ "$namespace" = 1 ]; then side=namespace; else side=host; fi
emit="${QCH_TEST_JOURNAL_EMIT:-}"
fail="${QCH_TEST_JOURNAL_FAIL:-}"
once="${QCH_TEST_JOURNAL_FAIL_ONCE:-}"
if [ -n "$once" ] && [ "$once" = "$side" ]; then
	counter="${QCH_TEST_JOURNAL_COUNTER:-}/$side"
	count=0
	if [ -f "$counter" ]; then count=$(cat "$counter"); fi
	count=$((count + 1))
	printf '%s' "$count" > "$counter"
	if [ "$count" -lt 2 ]; then
		fail="$side"
		emit=""
	else
		emit="$side"
		fail=""
	fi
fi
if [ "$emit" != "$side" ] && [ "$fail" != "$side" ]; then
	while :; do sleep 1; done
fi
if [ "$fail" = "$side" ]; then
	printf '%s\n' "journal unavailable for $side" >&2
	exit 1
fi
if [ -n "${QCH_TEST_JOURNAL_TRIGGER:-}" ]; then
	while [ ! -e "$QCH_TEST_JOURNAL_TRIGGER" ]; do sleep 0.05; done
fi
printf '%s
' "$QCH_TEST_JOURNAL_LINE"
while :; do sleep 1; done
`

func installCoreLogJournalFixture(t *testing.T) {
	t.Helper()
	helper := filepath.Join(t.TempDir(), "journalctl-fixture")
	if err := os.WriteFile(helper, []byte(coreLogJournalFixtureScript), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := journalctlPath
	journalctlPath = helper
	t.Cleanup(func() { journalctlPath = previous })
}

func runCoreLogCollector(t *testing.T, collector *CoreLogCollector) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("core log collector did not stop")
		}
	})
}

func waitForCoreLogStatus(t *testing.T, collector *CoreLogCollector, engine core.Engine, what string,
	match func(CoreLogSourceStatus) bool) CoreLogSourceStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if status := collector.Status()[engine]; match(status) {
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("core log source status did not become %s: %+v", what, collector.Status()[engine])
	return CoreLogSourceStatus{}
}

func waitForCoreLogKindStatus(t *testing.T, collector *CoreLogCollector, engine core.Engine, kind, want string) CoreLogSourceStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		collector.mu.Lock()
		status := collector.kindStatus[engine][kind]
		collector.mu.Unlock()
		if status.Status == want {
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("core log %s/%s status did not become %s", engine, kind, want)
	return CoreLogSourceStatus{}
}

func staticCoreLogServiceState(status string) func(string) (string, error) {
	return func(string) (string, error) { return status, nil }
}

func TestMissingManagedSingBoxConfigReportsNotConfiguredThenRecovers(t *testing.T) {
	installCoreLogJournalFixture(t)
	logRoot := t.TempDir()
	previousLogRoot := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previousLogRoot })
	configPath := filepath.Join(t.TempDir(), "config.json")
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: {
		Binary: "/bin/sh", ConfigPath: configPath, Service: "qagent-sing-box.service",
	}}}
	collector := NewCoreLogCollectorForExecutor(executor)
	collector.serviceState = staticCoreLogServiceState("active")
	status := collector.Status()[core.EngineSingBox]
	if status.Status != core.CoreLogStatusNotConfigured || status.Error != core.CoreLogErrorConfigMissing ||
		status.Source != core.CoreLogSourceFile || status.LastAt != nil {
		t.Fatalf("missing managed configuration status = %+v", status)
	}
	trigger := filepath.Join(t.TempDir(), "journal-trigger")
	t.Setenv("QCH_TEST_JOURNAL_EMIT", "namespace")
	t.Setenv("QCH_TEST_JOURNAL_TRIGGER", trigger)
	t.Setenv("QCH_TEST_JOURNAL_LINE",
		`{"MESSAGE":"managed sing-box started","_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"6","__CURSOR":"first-line-cursor"}`)
	runCoreLogCollector(t, collector)
	if err := os.WriteFile(configPath, []byte(`{"log":{"level":"info","timestamp":true},"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
		t.Fatal(err)
	}
	status = waitForCoreLogStatus(t, collector, core.EngineSingBox, "waiting after configuration appeared",
		func(status CoreLogSourceStatus) bool {
			return status.Status == core.CoreLogStatusWaiting && status.Error == core.CoreLogErrorAwaitingFirstLog
		})
	if status.Source != core.CoreLogSourceJournal || status.LastAt != nil {
		t.Fatalf("armed status after configuration appeared = %+v", status)
	}
	if err := os.WriteFile(trigger, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	status = waitForCoreLogStatus(t, collector, core.EngineSingBox, "active after the first line",
		func(status CoreLogSourceStatus) bool { return status.Status == core.CoreLogStatusActive })
	if status.Error != "" || status.Source != core.CoreLogSourceJournal {
		t.Fatalf("active status after the first line = %+v", status)
	}
	if status.LastAt == nil || status.LastAt.Location() != time.UTC {
		t.Fatalf("last delivery timestamp = %+v, want UTC", status.LastAt)
	}
}

func TestManagedSingBoxConfigReadFailuresStayFailed(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"log":{`), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: {
		Binary: "/bin/sh", ConfigPath: configPath, Service: "qagent-sing-box.service",
	}}}
	collector := NewCoreLogCollectorForExecutor(executor)
	collector.serviceState = staticCoreLogServiceState("active")
	status := collector.Status()[core.EngineSingBox]
	if status.Status != core.CoreLogStatusFailed || status.Error != "config-invalid" || status.Source != core.CoreLogSourceFile {
		t.Fatalf("invalid managed configuration status = %+v", status)
	}
	if err := os.Chmod(configPath, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := collector.RefreshImportedSingBoxSource(executor); err == nil {
		t.Fatal("unsafe managed configuration was accepted")
	}
	status = collector.Status()[core.EngineSingBox]
	if status.Status != core.CoreLogStatusFailed || status.Error != "config-unsafe" {
		t.Fatalf("unsafe managed configuration status = %+v", status)
	}
}

func TestJournalSourceReportsWaitingUntilFirstDeliveredLine(t *testing.T) {
	installCoreLogJournalFixture(t)
	trigger := filepath.Join(t.TempDir(), "journal-trigger")
	t.Setenv("QCH_TEST_JOURNAL_EMIT", "namespace")
	t.Setenv("QCH_TEST_JOURNAL_TRIGGER", trigger)
	t.Setenv("QCH_TEST_JOURNAL_LINE",
		`{"MESSAGE":"first managed line","_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"6","__CURSOR":"first-line-cursor"}`)
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{
		core.EngineSingBox: {Service: "qagent-sing-box.service"},
	})
	collector.serviceState = staticCoreLogServiceState("active")
	runCoreLogCollector(t, collector)
	status := waitForCoreLogStatus(t, collector, core.EngineSingBox, "waiting before the first line",
		func(status CoreLogSourceStatus) bool {
			return status.Status == core.CoreLogStatusWaiting && status.Error == core.CoreLogErrorAwaitingFirstLog
		})
	if status.Source != core.CoreLogSourceJournal || status.LastAt != nil {
		t.Fatalf("armed journal status = %+v", status)
	}
	if err := os.WriteFile(trigger, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	status = waitForCoreLogStatus(t, collector, core.EngineSingBox, "active after the first line",
		func(status CoreLogSourceStatus) bool { return status.Status == core.CoreLogStatusActive })
	if status.Source != core.CoreLogSourceJournal || status.LastAt == nil {
		t.Fatalf("delivered journal status = %+v", status)
	}
}

func TestHostJournalDeliveryPromotesJournalHostSource(t *testing.T) {
	installCoreLogJournalFixture(t)
	t.Setenv("QCH_TEST_JOURNAL_EMIT", "host")
	t.Setenv("QCH_TEST_JOURNAL_LINE",
		`{"MESSAGE":"host journal line","_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"6","__CURSOR":"host-line-cursor"}`)
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{
		core.EngineSingBox: {Service: "qagent-sing-box.service"},
	})
	collector.serviceState = staticCoreLogServiceState("active")
	runCoreLogCollector(t, collector)
	status := waitForCoreLogStatus(t, collector, core.EngineSingBox, "active from the host journal",
		func(status CoreLogSourceStatus) bool { return status.Status == core.CoreLogStatusActive })
	if status.Source != core.CoreLogSourceJournalHost || status.LastAt == nil {
		t.Fatalf("host journal delivery status = %+v", status)
	}
	collector.mu.Lock()
	preferred := collector.preferredKind[core.EngineSingBox]
	collector.mu.Unlock()
	if preferred != core.CoreLogSourceJournalHost {
		t.Fatalf("host journal delivery preferred source = %q", preferred)
	}
}

func TestHostJournalFailureKeepsNamespaceSourceActive(t *testing.T) {
	installCoreLogJournalFixture(t)
	t.Setenv("QCH_TEST_JOURNAL_EMIT", "namespace")
	t.Setenv("QCH_TEST_JOURNAL_FAIL", "host")
	t.Setenv("QCH_TEST_JOURNAL_LINE",
		`{"MESSAGE":"namespace line before host failure","_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"6","__CURSOR":"namespace-line-cursor"}`)
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{
		core.EngineSingBox: {Service: "qagent-sing-box.service"},
	})
	collector.serviceState = staticCoreLogServiceState("active")
	runCoreLogCollector(t, collector)
	waitForCoreLogKindStatus(t, collector, core.EngineSingBox, core.CoreLogSourceJournalHost, core.CoreLogStatusFailed)
	status := collector.Status()[core.EngineSingBox]
	if status.Status != core.CoreLogStatusActive || status.Source != core.CoreLogSourceJournal || status.LastAt == nil {
		t.Fatalf("healthy namespace source was overwritten by host failure: %+v", status)
	}
}

func TestJournalReadFailureFollowsServiceState(t *testing.T) {
	for _, test := range []struct {
		name       string
		service    string
		wantStatus string
		wantError  string
	}{
		{name: "inactive service waits", service: "inactive", wantStatus: core.CoreLogStatusWaiting, wantError: core.CoreLogErrorServiceInactive},
		{name: "running service fails", service: "active", wantStatus: core.CoreLogStatusFailed, wantError: "collector-failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			installCoreLogJournalFixture(t)
			counterRoot := t.TempDir()
			t.Setenv("QCH_TEST_JOURNAL_FAIL_ONCE", "namespace")
			t.Setenv("QCH_TEST_JOURNAL_COUNTER", counterRoot)
			t.Setenv("QCH_TEST_JOURNAL_LINE",
				`{"MESSAGE":"after journal failure","_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"6","__CURSOR":"recovered-cursor"}`)
			collector := NewCoreLogCollector(map[core.Engine]EngineSpec{
				core.EngineSingBox: {Service: "qagent-sing-box.service"},
			})
			collector.serviceState = staticCoreLogServiceState(test.service)
			runCoreLogCollector(t, collector)
			status := waitForCoreLogStatus(t, collector, core.EngineSingBox, test.wantStatus+" after a journal read failure",
				func(status CoreLogSourceStatus) bool {
					return status.Status == test.wantStatus && status.Error == test.wantError
				})
			if status.Source != core.CoreLogSourceJournal || status.LastAt != nil {
				t.Fatalf("failed read status = %+v", status)
			}
			collector.serviceState = staticCoreLogServiceState("active")
			status = waitForCoreLogStatus(t, collector, core.EngineSingBox, "active after recovery",
				func(status CoreLogSourceStatus) bool { return status.Status == core.CoreLogStatusActive })
			if status.Error != "" || status.LastAt == nil {
				t.Fatalf("recovered journal status = %+v", status)
			}
		})
	}
}

func TestSharedJournalWaitsForFirstLineAndPromotesHostSource(t *testing.T) {
	const share = "shr_0123456789abcdef"
	service := "qagent-xray@" + share + ".service"
	for _, test := range []struct {
		name       string
		namespace  bool
		emitter    string
		wantSource string
	}{
		{name: "namespace", namespace: true, emitter: "namespace", wantSource: core.CoreLogSourceJournal},
		{name: "host journal", namespace: false, emitter: "host", wantSource: core.CoreLogSourceJournalHost},
	} {
		t.Run(test.name, func(t *testing.T) {
			installCoreLogJournalFixture(t)
			trigger := filepath.Join(t.TempDir(), "journal-trigger")
			t.Setenv("QCH_TEST_JOURNAL_EMIT", test.emitter)
			t.Setenv("QCH_TEST_JOURNAL_TRIGGER", trigger)
			t.Setenv("QCH_TEST_JOURNAL_LINE", `{"MESSAGE":"shared instance line","_SYSTEMD_UNIT":"`+service+`","PRIORITY":"6","__CURSOR":"shared-cursor"}`)
			root := NewCoreLogCollector()
			root.serviceState = staticCoreLogServiceState("active")
			root.sharedJournalUpdates = [2]chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1)}
			child := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), map[core.Engine]EngineSpec{
				core.EngineXray: {Service: service},
			})
			child.parent = root
			child.instanceID = share
			child.instanceService = service
			child.sources = nil
			child.serviceState = staticCoreLogServiceState("active")
			key := string(core.EngineXray) + "/" + share
			root.instances = map[string]*coreLogInstanceRun{key: {
				collector: child, spec: EngineSpec{Service: service}, engine: core.EngineXray, shareID: share,
			}}
			updates := make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { root.runSharedJournal(ctx, test.namespace, updates); close(done) }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(15 * time.Second):
					t.Error("shared journal follower did not stop")
				}
			})
			if test.namespace {
				// The armed instance must be visible before any line arrives, and
				// must not claim a delivery timestamp.
				deadline := time.Now().Add(10 * time.Second)
				armed := false
				for time.Now().Before(deadline) && !armed {
					state := root.InstanceStatuses()[key]
					armed = state.CoreLogStatus == core.CoreLogStatusWaiting &&
						state.CoreLogError == core.CoreLogErrorAwaitingFirstLog && state.CoreLogLastAt == nil
					time.Sleep(20 * time.Millisecond)
				}
				if !armed {
					t.Fatalf("shared instance armed status = %+v", root.InstanceStatuses()[key])
				}
			}
			if err := os.WriteFile(trigger, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			state := root.InstanceStatuses()[key]
			for time.Now().Before(deadline) && state.CoreLogStatus != core.CoreLogStatusActive {
				time.Sleep(20 * time.Millisecond)
				state = root.InstanceStatuses()[key]
			}
			if state.CoreLogStatus != core.CoreLogStatusActive || state.CoreLogSource != test.wantSource || state.CoreLogLastAt == nil {
				t.Fatalf("shared instance delivered status = %+v", state)
			}
		})
	}
}

func TestFileSourcePromotesOnlyAfterDelivery(t *testing.T) {
	for _, test := range []struct {
		name       string
		manager    string
		wantSource string
		// A fallback file is not the reported source until it delivers a line:
		// the managed journal stays authoritative while it is merely armed.
		reportedWhileArmed bool
	}{
		{name: "openrc", manager: ServiceManagerOpenRC, wantSource: core.CoreLogSourceOpenRC, reportedWhileArmed: true},
		{name: "systemd fallback", manager: ServiceManagerSystemd, wantSource: core.CoreLogSourceFallback},
	} {
		t.Run(test.name, func(t *testing.T) {
			logRoot := t.TempDir()
			previous := openRCCoreLogRoot
			openRCCoreLogRoot = logRoot
			previousFallback := systemdFallbackCoreLogRoot
			systemdFallbackCoreLogRoot = logRoot
			t.Cleanup(func() {
				openRCCoreLogRoot = previous
				systemdFallbackCoreLogRoot = previousFallback
			})
			service := "qagent-sing-box"
			var path string
			if test.manager == ServiceManagerOpenRC {
				path = filepath.Join(logRoot, service+".log")
			} else {
				service = "qagent-sing-box.service"
				var err error
				path, err = systemdFallbackCoreLogPath(service)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			manager, err := NewServiceManager(test.manager)
			if err != nil {
				t.Fatal(err)
			}
			collector := NewCoreLogCollectorForServiceManager(manager, map[core.Engine]EngineSpec{
				core.EngineSingBox: {Service: service},
			})
			collector.sources = nil
			collector.serviceState = staticCoreLogServiceState("active")
			runCoreLogCollector(t, collector)
			if test.reportedWhileArmed {
				status := waitForCoreLogStatus(t, collector, core.EngineSingBox, "armed",
					func(status CoreLogSourceStatus) bool {
						return status.Status == core.CoreLogStatusWaiting && status.Error == core.CoreLogErrorAwaitingFirstLog
					})
				if status.Source != test.wantSource || status.LastAt != nil {
					t.Fatalf("armed file source status = %+v", status)
				}
			} else {
				armed := waitForCoreLogKindStatus(t, collector, core.EngineSingBox, test.wantSource, core.CoreLogStatusWaiting)
				if armed.LastAt != nil {
					t.Fatalf("armed fallback source claimed a delivery: %+v", armed)
				}
				collector.mu.Lock()
				preferred := collector.preferredKind[core.EngineSingBox]
				collector.mu.Unlock()
				if preferred == test.wantSource {
					t.Fatalf("file source %q was promoted before delivering a line", preferred)
				}
			}
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString("file transport line\n"); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			status := waitForCoreLogStatus(t, collector, core.EngineSingBox, "active",
				func(status CoreLogSourceStatus) bool { return status.Status == core.CoreLogStatusActive })
			if status.Source != test.wantSource || status.LastAt == nil {
				t.Fatalf("delivered file source status = %+v", status)
			}
		})
	}
}

func TestHeartbeatReportsCoreLogSourceAndLastDelivery(t *testing.T) {
	manager, err := NewServiceManager(ServiceManagerSystemd)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{
		core.EngineSingBox: {Service: "qagent-sing-box.service"},
	})
	collector.selectSourceStatus(core.EngineSingBox, core.CoreLogSourceFile, core.CoreLogStatusNotConfigured, core.CoreLogErrorConfigMissing)
	client := &Client{
		config:   ClientConfig{Version: "1.0.0", StatePath: statePath},
		executor: &Executor{Services: manager},
		logs:     collector,
		metrics:  NewMetricsCollector(),
		traffic:  NewTrafficManager(statePath),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	outgoing := make(chan core.WireMessage, 1)
	if err := client.queueHeartbeat(ctx, outgoing); err != nil {
		t.Fatalf("queueHeartbeat() error = %v", err)
	}
	message := <-outgoing
	unconfigured := message.Heartbeat.Runtime[core.EngineSingBox]
	if unconfigured.CoreLogStatus != core.CoreLogStatusNotConfigured ||
		unconfigured.CoreLogSource != core.CoreLogSourceFile || unconfigured.CoreLogLastAt != nil {
		t.Fatalf("not-configured heartbeat state = %+v", unconfigured)
	}
	delivered := time.Now().UTC()
	collector.mu.Lock()
	collector.markCoreLogDeliveryLocked(core.EngineSingBox, core.CoreLogSourceJournalHost)
	// Pin the delivery time so the heartbeat must carry exactly this value.
	value := collector.kindStatus[core.EngineSingBox][core.CoreLogSourceJournalHost]
	value.LastAt = &delivered
	collector.kindStatus[core.EngineSingBox][core.CoreLogSourceJournalHost] = value
	collector.status[core.EngineSingBox] = value
	collector.mu.Unlock()
	if err := client.queueHeartbeat(ctx, outgoing); err != nil {
		t.Fatalf("queueHeartbeat() error = %v", err)
	}
	message = <-outgoing
	active := message.Heartbeat.Runtime[core.EngineSingBox]
	if active.CoreLogStatus != core.CoreLogStatusActive || active.CoreLogSource != core.CoreLogSourceJournalHost {
		t.Fatalf("delivered heartbeat state = %+v", active)
	}
	if active.CoreLogLastAt == nil || !active.CoreLogLastAt.Equal(delivered) {
		t.Fatalf("heartbeat last delivery = %v, want %v", active.CoreLogLastAt, delivered)
	}
}

func TestSharedInstanceStatusReportsSourceAndLastDelivery(t *testing.T) {
	root := NewCoreLogCollector()
	child := NewCoreLogCollector()
	child.parent = root
	child.instanceID = "shr_0123456789abcdef"
	child.instanceService = "qagent-xray@shr_0123456789abcdef.service"
	key := string(core.EngineXray) + "/" + child.instanceID
	root.instances = map[string]*coreLogInstanceRun{key: {
		collector: child, spec: EngineSpec{Service: child.instanceService}, engine: core.EngineXray, shareID: child.instanceID,
	}}
	child.setSourceStatus(core.EngineXray, core.CoreLogSourceJournal, core.CoreLogStatusWaiting, core.CoreLogErrorAwaitingFirstLog)
	state := root.InstanceStatuses()[key]
	if state.CoreLogStatus != core.CoreLogStatusWaiting || state.CoreLogSource != core.CoreLogSourceJournal || state.CoreLogLastAt != nil {
		t.Fatalf("armed shared instance status = %+v", state)
	}
	child.markCoreLogDelivery(core.EngineXray, core.CoreLogSourceJournal)
	state = root.InstanceStatuses()[key]
	if state.CoreLogStatus != core.CoreLogStatusActive || state.CoreLogSource != core.CoreLogSourceJournal || state.CoreLogLastAt == nil {
		t.Fatalf("delivered shared instance status = %+v", state)
	}
	if state.CoreLogLastAt.Location() != time.UTC {
		t.Fatalf("shared instance last delivery = %+v, want UTC", state.CoreLogLastAt)
	}
}
