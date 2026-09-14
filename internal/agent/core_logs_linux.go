//go:build linux

package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	coreLogQueueLimit         = 2048
	defaultCoreLogMaxBytes    = 16 << 20
	defaultCoreLogRotateCount = 1
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
	mu                 sync.Mutex
	filePublishMu      sync.Mutex
	queued             []core.CoreLogEntry
	pending            *core.CoreLogBatch
	dropped            uint64
	sources            []coreLogJournalSource
	fileSources        []coreLogFileSource
	seenCursors        map[string]struct{}
	cursorOrder        []string
	runContext         context.Context
	runWait            sync.WaitGroup
	runStopped         bool
	activeFiles        map[string]*coreLogFileRun
	status             map[core.Engine]CoreLogSourceStatus
	statusKind         map[core.Engine]string
	kindStatus         map[core.Engine]map[string]CoreLogSourceStatus
	preferredKind      map[core.Engine]string
	consoleKind        map[core.Engine]string
	transitions        map[core.Engine]*coreLogSourceTransition
	nextFileEpoch      uint64
	sourceReady        map[core.Engine]chan struct{}
	coreLogMaxBytes    int64
	coreLogRotateCount int
}

type coreLogFileRun struct {
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
}

type coreLogFileSource struct {
	path         string
	root         string
	engine       core.Engine
	kind         string
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
		statusKind: make(map[core.Engine]string), kindStatus: make(map[core.Engine]map[string]CoreLogSourceStatus),
		preferredKind: make(map[core.Engine]string), consoleKind: make(map[core.Engine]string),
		transitions: make(map[core.Engine]*coreLogSourceTransition), sourceReady: make(map[core.Engine]chan struct{}),
		coreLogMaxBytes: defaultCoreLogMaxBytes, coreLogRotateCount: defaultCoreLogRotateCount,
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
	for _, source := range sources {
		collector.removeExcessArchives(source)
		if changed {
			for index := 1; index <= 5; index++ {
				_ = os.Remove(coreLogArchivePath(source.path, index))
			}
			maxFileBytes, _ := collector.rotationPolicy()
			if validated, err := openValidatedCoreLogFileContext(context.Background(), source); err == nil {
				if validated.identity.Size() > maxFileBytes {
					if err := validated.file.Truncate(0); err != nil {
						slog.Warn("apply managed core log capacity", "path", source.path, "error", err)
					}
				}
				_ = validated.file.Close()
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
	bytes := collector.coreLogMaxBytes / int64((count+1)*sources)
	if bytes < 1 {
		bytes = 1
	}
	return bytes, count
}

func NewCoreLogCollectorForExecutor(executor *Executor) *CoreLogCollector {
	collector := NewCoreLogCollectorForServiceManager(executor.serviceManager(), executor.Specs, executor.ExistingSpecs)
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
	value := CoreLogSourceStatus{Status: status, Error: code}
	collector.kindStatus[engine][kind] = value
	armed := status == "active" || (kind == "openrc" && status == "waiting")
	if armed && kind == collector.consoleKind[engine] && collector.sourceReady[engine] != nil {
		close(collector.sourceReady[engine])
		collector.sourceReady[engine] = nil
	}
	if preferred := collector.preferredKind[engine]; preferred != "" && preferred != kind {
		return
	}
	collector.status[engine] = value
	collector.statusKind[engine] = kind
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
	collector.setSourceStatusLocked(source.engine, source.kind, status, code)
}

func (collector *CoreLogCollector) selectSourceKind(engine core.Engine, kind string, fallback CoreLogSourceStatus) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.preferredKind[engine] = kind
	if current, ok := collector.kindStatus[engine][kind]; ok {
		collector.status[engine] = current
	} else {
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
			for _, source := range collector.sources {
				for _, engine := range source.unitEngines {
					collector.setSourceStatus(engine, "journal", "failed", "collector-unavailable")
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
	<-ctx.Done()
	collector.mu.Lock()
	collector.runStopped = true
	collector.mu.Unlock()
	collector.runWait.Wait()
}
