//go:build linux

package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	coreLogQueueLimit = 2048
	// Until the panel sends its policy, start at the smallest valid allowance
	// with no snapshots. An Agent restart must never temporarily restore the
	// product default over a smaller panel-selected limit.
	defaultCoreLogMaxBytes    = 1 << 20
	defaultCoreLogRotateCount = 0
	coreLogFileMaxLine        = 256 << 10
	coreLogRevalidateBytes    = 256 << 10
	coreLogRevalidateEvery    = time.Second
)

// Managed OpenRC services log through supervise-daemon output_log files below
// this root, one file per service, named after the service itself. It is a
// variable so tests can stage the tree in a temporary directory.
var (
	journalctlPath         = "/usr/bin/journalctl"
	openRCCoreLogRoot      = "/var/log/qagent"
	importedSingBoxLogRoot = "/var/lib/qcontrolhub-sing-box"
)

type CoreLogCollector struct {
	sharedJournalUpdates [2]chan struct{}

	executor *Executor
	manager  *ServiceManager
	// serviceState is a test seam for the managed service probe that keeps a
	// stopped core from being reported as a read failure.
	serviceState        func(service string) (string, error)
	parent              *CoreLogCollector
	instanceID          string
	instanceService     string
	instancesMu         sync.Mutex
	discoveryMu         sync.Mutex
	instances           map[string]*coreLogInstanceRun
	instanceFailures    map[string]core.SharedInstanceStatus
	fileBudgetInstances int

	mu                   sync.Mutex
	filePublishMu        sync.Mutex
	queued               []core.CoreLogEntry
	pending              *core.CoreLogBatch
	dropped              uint64
	sources              []coreLogJournalSource
	fileSources          []coreLogFileSource
	seenCursors          map[string]struct{}
	cursorOrder          []string
	runContext           context.Context
	runWait              sync.WaitGroup
	runStopped           bool
	activeFiles          map[string]*coreLogFileRun
	status               map[core.Engine]CoreLogSourceStatus
	statusKind           map[core.Engine]string
	kindStatus           map[core.Engine]map[string]CoreLogSourceStatus
	preferredKind        map[core.Engine]string
	consoleKind          map[core.Engine]string
	transitions          map[core.Engine]*coreLogSourceTransition
	nextFileEpoch        uint64
	sourceReady          map[core.Engine]chan struct{}
	coreLogMaxBytes      int64
	coreLogRotateCount   int
	fileBudgetDivisor    int
	configuredPaths      map[string]bool
	missingSingBoxConfig bool
}

type coreLogFileRun struct {
	truncated atomic.Bool
	cancel    context.CancelFunc
	bindingID string
	done      chan struct{}
	source    coreLogFileSource
	progress  coreLogImportWindow
}

type coreLogImportWindow struct {
	path   string
	exists bool
	device uint64
	inode  uint64
	offset int64
}

type coreLogSourceTransition struct {
	epoch          uint64
	previousDigest string
	targetDigest   string
	windows        map[string]coreLogImportWindow
}

type coreLogJournalSource struct {
	arguments   []string
	unitEngines map[string]core.Engine
	// kind is the transport this source reports: the managed journal namespace
	// or the host journal.
	kind string
	// report delivers this source's status for one unit. A nil report means the
	// source belongs to the collector itself; shared journals report through
	// their instance collectors instead.
	report        func(unit string, engine core.Engine, status, code string)
	recoverySince *time.Time
}

type coreLogFileSource struct {
	path   string
	root   string
	engine core.Engine
	kind   string
	// service owns the files this source reads; it is probed to keep a stopped
	// service out of the failed state.
	service      string
	configPath   string
	configDigest string
	markerPrefix string
	executor     *Executor
	ownership    completedCoreMigration
	initial      *coreLogImportWindow
	epoch        uint64
	// beforeInitialCursor is an internal test seam used to prove that readiness
	// is not published before an existing file's no-history cursor is fixed.
	beforeInitialCursor func()
}

type CoreLogSourceStatus struct {
	Status string
	Error  string
	// Source is the transport that produced Status. A nil LastAt means this
	// transport has not delivered a line yet, which separates a quiet source
	// from a broken one.
	Source string
	LastAt *time.Time
}

func NewCoreLogCollector(specs ...map[core.Engine]EngineSpec) *CoreLogCollector {
	return NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), specs...)
}

func NewCoreLogCollectorForServiceManager(manager *ServiceManager, specs ...map[core.Engine]EngineSpec) *CoreLogCollector {
	if len(specs) == 0 {
		specs = []map[core.Engine]EngineSpec{DefaultSpecs()}
	}
	collector := &CoreLogCollector{
		activeFiles: make(map[string]*coreLogFileRun), status: make(map[core.Engine]CoreLogSourceStatus),
		manager:    manager,
		statusKind: make(map[core.Engine]string), kindStatus: make(map[core.Engine]map[string]CoreLogSourceStatus),
		preferredKind: make(map[core.Engine]string), consoleKind: make(map[core.Engine]string),
		transitions: make(map[core.Engine]*coreLogSourceTransition), sourceReady: make(map[core.Engine]chan struct{}),
		coreLogMaxBytes: defaultCoreLogMaxBytes, coreLogRotateCount: defaultCoreLogRotateCount,
		fileBudgetDivisor: 1,
	}
	if manager != nil && manager.Kind() == ServiceManagerOpenRC {
		collector.fileSources = coreLogFileSources(specs...)
		for _, source := range collector.fileSources {
			collector.consoleKind[source.engine] = "openrc"
			collector.preferredKind[source.engine] = "openrc"
			collector.sourceReady[source.engine] = make(chan struct{})
		}
		return collector
	}
	collector.sources = coreLogJournalSources(specs...)
	collector.fileSources = systemdFallbackCoreLogFileSources(specs...)
	// A node may temporarily contain cores started under both the private
	// journal and /run fallback drop-ins. Reserve half of the node-wide budget
	// for each backend so their combined transport caches cannot exceed policy.
	collector.fileBudgetDivisor = 2
	for _, source := range collector.sources {
		for _, engine := range source.unitEngines {
			collector.consoleKind[engine] = "journal"
			collector.preferredKind[engine] = "journal"
			if collector.sourceReady[engine] == nil {
				collector.sourceReady[engine] = make(chan struct{})
			}
		}
	}
	return collector
}

// ApplyPolicy updates OpenRC file rotation immediately. systemd journal
// limits are applied by Client.applyAgentPolicy through the managed namespace.
func (collector *CoreLogCollector) ApplyPolicy(policy core.AgentPolicy) {
	collector.mu.Lock()
	changed := collector.coreLogMaxBytes != int64(policy.CoreLogMaxMiB)<<20 || collector.coreLogRotateCount != int(policy.CoreLogRotateCount)
	collector.coreLogMaxBytes = int64(policy.CoreLogMaxMiB) << 20
	collector.coreLogRotateCount = int(policy.CoreLogRotateCount)
	sources := append([]coreLogFileSource(nil), collector.fileSources...)
	collector.mu.Unlock()
	if collector.parent == nil {
		collector.instancesMu.Lock()
		collector.rebalanceInstanceBudgetsLocked()
		children := make([]*CoreLogCollector, 0, len(collector.instances))
		for _, child := range collector.instances {
			children = append(children, child.collector)
		}
		collector.instancesMu.Unlock()
		for _, child := range children {
			child.ApplyPolicy(policy)
		}
	}
	for _, source := range sources {
		collector.removeExcessArchives(source)
		if changed {
			for index := 1; index <= 5; index++ {
				_ = os.Remove(coreLogArchivePath(source.path, index))
			}
		}
		maxFileBytes, _ := collector.rotationPolicy()
		if source.kind == "systemd-fallback" {
			if validated, err := openValidatedCoreLogFileContext(context.Background(), source); err == nil {
				size := validated.identity.Size()
				_ = validated.file.Close()
				if size > maxFileBytes {
					if err := truncateSystemdFallbackCoreLog(context.Background(), source); err != nil {
						slog.Warn("apply managed core log capacity", "path", source.path, "error", err)
					}
				}
			}
		} else if validated, err := openValidatedCoreLogFileContextMode(context.Background(), source, true); err == nil {
			// Enforce the panel limit even when it equals the Agent default. An
			// older Agent may have left an oversized file without changing the
			// selected policy value.
			if validated.identity.Size() > maxFileBytes {
				if err := validated.file.Truncate(0); err != nil {
					slog.Warn("apply managed core log capacity", "path", source.path, "error", err)
				}
			}
			_ = validated.file.Close()
		}
		for index := 1; index <= int(policy.CoreLogRotateCount); index++ {
			path := coreLogArchivePath(source.path, index)
			if info, err := os.Lstat(path); err == nil && info.Size() > maxFileBytes {
				// These lines were already streamed. Remove an archive left by an
				// older policy rather than let it evade the node-wide budget.
				_ = os.Remove(path)
			}
		}
	}
}

func (collector *CoreLogCollector) rotationPolicy() (int64, int) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	count := collector.coreLogRotateCount
	sources := len(collector.fileSources)
	if sources < 1 {
		sources = 1
	}
	divisor := collector.fileBudgetDivisor
	if divisor < 1 {
		divisor = 1
	}
	bytes := collector.coreLogMaxBytes / int64(divisor*(count+1)*sources*max(1, collector.fileBudgetInstances))
	if bytes < 1 {
		bytes = 1
	}
	return bytes, count
}

func NewCoreLogCollectorForExecutor(executor *Executor) *CoreLogCollector {
	collector := NewCoreLogCollectorForServiceManager(executor.serviceManager(), executor.Specs, executor.ExistingSpecs)
	collector.sharedJournalUpdates = [2]chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1)}
	collector.executor = executor
	executor.coreLogReady = collector.prepareSharedLogSource
	if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
		slog.Warn("configure imported sing-box log source", "error", err)
	}
	return collector
}

func (collector *CoreLogCollector) Status() map[core.Engine]CoreLogSourceStatus {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	result := make(map[core.Engine]CoreLogSourceStatus, len(collector.status))
	for engine, status := range collector.status {
		if engine == core.EngineSingBox && collector.missingSingBoxConfig {
			status = CoreLogSourceStatus{Status: core.CoreLogStatusFailed, Error: core.CoreLogErrorConfigMissing, Source: core.CoreLogSourceFile}
		}
		result[engine] = status
	}
	return result
}

func (collector *CoreLogCollector) setSourceStatus(engine core.Engine, kind, status, code string) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.setSourceStatusLocked(engine, kind, status, code)
}

func (collector *CoreLogCollector) setSourceStatusLocked(engine core.Engine, kind, status, code string) {
	if collector.status == nil {
		collector.status = make(map[core.Engine]CoreLogSourceStatus)
	}
	if collector.statusKind == nil {
		collector.statusKind = make(map[core.Engine]string)
	}
	if collector.kindStatus == nil {
		collector.kindStatus = make(map[core.Engine]map[string]CoreLogSourceStatus)
	}
	if collector.kindStatus[engine] == nil {
		collector.kindStatus[engine] = make(map[string]CoreLogSourceStatus)
	}
	value := collector.kindStatus[engine][kind]
	value.Status = status
	value.Error = code
	value.Source = kind
	if status == core.CoreLogStatusActive {
		now := time.Now().UTC()
		value.LastAt = &now
	}
	collector.kindStatus[engine][kind] = value
	collector.armSourceReadyLocked(engine, kind, status)
	if preferred := collector.preferredKind[engine]; preferred != "" && preferred != kind {
		return
	}
	if status == core.CoreLogStatusFailed && collector.isConsoleSourceLocked(engine, kind) {
		if alternate, healthy, _ := collector.consoleSourceLocked(engine); healthy.Status == core.CoreLogStatusActive {
			collector.preferredKind[engine] = alternate
			collector.status[engine] = healthy
			collector.statusKind[engine] = alternate
			return
		}
	}
	collector.status[engine] = value
	collector.statusKind[engine] = kind
}

// armSourceReadyLocked releases the deployment barrier once the console
// transport is armed. Waiting (cursor fixed, file opened at EOF) is the armed
// state: a line written from now on is readable. Active additionally requires a
// delivered line and is reported separately.
func (collector *CoreLogCollector) armSourceReadyLocked(engine core.Engine, kind, status string) {
	if !collector.isConsoleSourceLocked(engine, kind) || collector.sourceReady[engine] == nil {
		return
	}
	if coreLogTransportReady(collector.kindStatus[engine][kind]) {
		close(collector.sourceReady[engine])
		collector.sourceReady[engine] = nil
	}
}

func coreLogTransportReady(status CoreLogSourceStatus) bool {
	return status.Status == core.CoreLogStatusActive || status.Status == core.CoreLogStatusWaiting &&
		(status.Error == core.CoreLogErrorAwaitingFirstLog || status.Error == core.CoreLogErrorServiceInactive || status.Error == "")
}

func (collector *CoreLogCollector) isConsoleSourceLocked(engine core.Engine, kind string) bool {
	console := collector.consoleKind[engine]
	return console != "" && (kind == console || console == core.CoreLogSourceJournal &&
		(kind == core.CoreLogSourceJournalHost || kind == core.CoreLogSourceFallback))
}

// Preserve the current working transport. An active alternative wins over an
// armed-but-quiet reader, and a missing file never satisfies console readiness.
func (collector *CoreLogCollector) consoleSourceLocked(engine core.Engine) (string, CoreLogSourceStatus, bool) {
	preferred := collector.preferredKind[engine]
	if collector.isConsoleSourceLocked(engine, preferred) {
		if value := collector.kindStatus[engine][preferred]; value.Status == core.CoreLogStatusActive {
			return preferred, value, true
		}
	}
	kinds := []string{collector.consoleKind[engine], core.CoreLogSourceJournalHost, core.CoreLogSourceFallback}
	for _, kind := range kinds {
		if value := collector.kindStatus[engine][kind]; collector.isConsoleSourceLocked(engine, kind) && value.Status == core.CoreLogStatusActive {
			return kind, value, true
		}
	}
	for _, kind := range append([]string{preferred}, kinds...) {
		if value := collector.kindStatus[engine][kind]; collector.isConsoleSourceLocked(engine, kind) && coreLogTransportReady(value) {
			return kind, value, true
		}
	}
	kind := collector.consoleKind[engine]
	return kind, collector.kindStatus[engine][kind], false
}

// markCoreLogDelivery records one collected line for the transport that
// produced it. The first line makes the source active, every line refreshes
// LastAt (UTC), and the delivering transport becomes the reported source.
func (collector *CoreLogCollector) markCoreLogDelivery(engine core.Engine, kind string) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.markCoreLogDeliveryLocked(engine, kind)
}

func (collector *CoreLogCollector) markCoreLogDeliveryLocked(engine core.Engine, kind string) {
	if collector.status == nil {
		collector.status = make(map[core.Engine]CoreLogSourceStatus)
	}
	if collector.statusKind == nil {
		collector.statusKind = make(map[core.Engine]string)
	}
	if collector.kindStatus == nil {
		collector.kindStatus = make(map[core.Engine]map[string]CoreLogSourceStatus)
	}
	if collector.kindStatus[engine] == nil {
		collector.kindStatus[engine] = make(map[string]CoreLogSourceStatus)
	}
	now := time.Now().UTC()
	value := collector.kindStatus[engine][kind]
	value.Status = core.CoreLogStatusActive
	value.Error = ""
	value.Source = kind
	value.LastAt = &now
	collector.kindStatus[engine][kind] = value
	collector.armSourceReadyLocked(engine, kind, value.Status)
	// Last delivery wins: only a transport that actually delivered a line may
	// claim the reported source, so a waiting or failed sibling can never
	// displace a healthy source.
	collector.preferredKind[engine] = kind
	collector.status[engine] = value
	collector.statusKind[engine] = kind
}

// coreLogServiceIdle reports whether the service that writes a log source is
// definitively not running. Only an explicit inactive-like state counts, so an
// unreachable service manager never hides a genuine read failure.
func (collector *CoreLogCollector) coreLogServiceIdle(service string) bool {
	if strings.TrimSpace(service) == "" {
		return false
	}
	var state string
	if probe := collector.serviceState; probe != nil {
		probed, err := probe(service)
		if err != nil {
			return false
		}
		state = probed
	} else {
		manager := collector.manager
		if manager == nil {
			return false
		}
		probeContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		probed, err := serviceStatusWithManager(probeContext, manager, service)
		if err != nil {
			return false
		}
		state = probed
	}
	switch strings.TrimSpace(state) {
	case "inactive", "failed":
		return true
	}
	return false
}

func (collector *CoreLogCollector) setFileSourceStatus(source coreLogFileSource, status, code string) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if source.kind == "file" && collector.transitions[source.engine] != nil {
		return
	}
	active := collector.activeFiles[coreLogFileSourceKey(source)]
	if active == nil || active.bindingID != coreLogFileBindingID(source) {
		return
	}
	if source.kind == "systemd-fallback" && status == "active" {
		// The fallback file only exists when systemd could not start the private
		// journal. Once observed, expose that live transport instead of leaving
		// the panel on a failed journal-reader status.
		collector.preferredKind[source.engine] = source.kind
	}
	collector.setSourceStatusLocked(source.engine, source.kind, status, code)
}

func (collector *CoreLogCollector) selectSourceKind(engine core.Engine, kind string, fallback CoreLogSourceStatus) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.preferredKind[engine] = kind
	if current, ok := collector.kindStatus[engine][kind]; ok {
		collector.status[engine] = current
	} else {
		fallback.Source = kind
		collector.status[engine] = fallback
	}
	collector.statusKind[engine] = kind
}

func (collector *CoreLogCollector) selectSourceStatus(engine core.Engine, kind, status, code string) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.preferredKind[engine] = kind
	collector.setSourceStatusLocked(engine, kind, status, code)
}

func coreLogErrorCode(err error) string {
	if err == nil {
		return "collector-stopped"
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission-denied"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "source-missing"
	}
	return "collector-failed"
}

func (collector *CoreLogCollector) Run(ctx context.Context) {
	collector.mu.Lock()
	collector.runContext = ctx
	collector.runStopped = false
	collector.mu.Unlock()
	if len(collector.sources) > 0 {
		if err := validatePrivilegedExecutable(journalctlPath); err != nil {
			slog.Warn("managed core log streaming is unavailable", "error", err)
			// A missing or unsafe journalctl is a real read failure: the reader
			// itself is unavailable, not merely quiet.
			for _, source := range collector.sources {
				for unit, engine := range source.unitEngines {
					collector.reportJournalSourceStatus(source, unit, engine, core.CoreLogStatusFailed, "collector-unavailable")
				}
			}
		} else {
			for _, source := range collector.sources {
				source := source
				collector.runWait.Add(1)
				go func() {
					defer collector.runWait.Done()
					collector.runSource(ctx, source)
				}()
			}
		}
	}
	collector.mu.Lock()
	fileSources := append([]coreLogFileSource(nil), collector.fileSources...)
	collector.mu.Unlock()
	for _, source := range fileSources {
		collector.startFileSource(source)
	}
	if collector.executor != nil {
		if collector.executor.serviceManager().Kind() == ServiceManagerSystemd {
			for index, namespace := range []bool{true, false} {
				collector.runWait.Add(1)
				go func(namespace bool, updates <-chan struct{}) {
					defer collector.runWait.Done()
					collector.runSharedJournal(ctx, namespace, updates)
				}(namespace, collector.sharedJournalUpdates[index])
			}
		}
		collector.refreshSharedLogSources(ctx)
		ticker := time.NewTicker(5 * time.Second)
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-ticker.C:
				collector.refreshSharedLogSources(ctx)
			}
		}
		ticker.Stop()
	} else {
		<-ctx.Done()
	}
	collector.instancesMu.Lock()
	collector.mu.Lock()
	collector.runStopped = true
	collector.mu.Unlock()
	collector.instancesMu.Unlock()
	collector.runWait.Wait()
}
