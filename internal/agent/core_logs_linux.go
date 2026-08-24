//go:build linux

package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	coreLogQueueLimit = 2048
	journalctlPath    = "/usr/bin/journalctl"
	// Align the volatile budget with the journald Storage=volatile cap by
	// rotating to a single .old copy once the live file grows past this size.
	coreLogFileRotateBytes = 8 << 20
	coreLogFileMaxLine     = 256 << 10
)

// Managed OpenRC services log through supervise-daemon output_log files below
// this root, one file per service, named after the service itself. It is a
// variable so tests can stage the tree in a temporary directory.
var (
	openRCCoreLogRoot      = "/var/log/qagent"
	importedSingBoxLogRoot = "/var/lib/qcontrolhub-sing-box"
)

type CoreLogCollector struct {
	mu            sync.Mutex
	queued        []core.CoreLogEntry
	pending       *core.CoreLogBatch
	dropped       uint64
	sources       []coreLogJournalSource
	fileSources   []coreLogFileSource
	seenCursors   map[string]struct{}
	cursorOrder   []string
	runContext    context.Context
	runWait       sync.WaitGroup
	runStopped    bool
	activeFiles   map[string]*coreLogFileRun
	status        map[core.Engine]CoreLogSourceStatus
	statusKind    map[core.Engine]string
	kindStatus    map[core.Engine]map[string]CoreLogSourceStatus
	preferredKind map[core.Engine]string
	consoleKind   map[core.Engine]string
	importWindows map[core.Engine]coreLogImportWindow
	sourceReady   map[core.Engine]chan struct{}
}

type coreLogFileRun struct {
	cancel    context.CancelFunc
	bindingID string
}

type coreLogImportWindow struct {
	path         string
	configDigest string
	exists       bool
	device       uint64
	inode        uint64
	offset       int64
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
		importWindows: make(map[core.Engine]coreLogImportWindow), sourceReady: make(map[core.Engine]chan struct{}),
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

func (collector *CoreLogCollector) startFileSource(source coreLogFileSource) {
	collector.mu.Lock()
	if collector.runStopped {
		collector.mu.Unlock()
		return
	}
	if collector.activeFiles == nil {
		collector.activeFiles = make(map[string]*coreLogFileRun)
	}
	key := coreLogFileSourceKey(source)
	bindingID := coreLogFileBindingID(source)
	if active := collector.activeFiles[key]; active != nil && active.bindingID == bindingID {
		collector.mu.Unlock()
		return
	}
	ctx := collector.runContext
	if ctx == nil || ctx.Err() != nil {
		collector.mu.Unlock()
		return
	}
	if active := collector.activeFiles[key]; active != nil {
		active.cancel()
	}
	fileContext, cancel := context.WithCancel(ctx)
	run := &coreLogFileRun{cancel: cancel, bindingID: bindingID}
	collector.activeFiles[key] = run
	collector.runWait.Add(1)
	collector.mu.Unlock()
	go func() {
		defer func() {
			collector.mu.Lock()
			if collector.activeFiles[key] == run {
				delete(collector.activeFiles, key)
			}
			collector.mu.Unlock()
			collector.runWait.Done()
		}()
		collector.runFileSource(fileContext, source)
	}()
}

func coreLogFileSourceKey(source coreLogFileSource) string {
	if source.kind == "file" {
		return string(source.engine) + "\x00file"
	}
	return string(source.engine) + "\x00" + source.kind + "\x00" + source.path
}

func coreLogFileBindingID(source coreLogFileSource) string {
	return source.path + "\x00" + source.configDigest + "\x00" + source.ownership.SourceDigest
}

// runFileSource keeps one tail reader alive per managed OpenRC log file with
// the same retry cadence as the journal readers.
func (collector *CoreLogCollector) runFileSource(ctx context.Context, source coreLogFileSource) {
	for ctx.Err() == nil {
		err := collector.followFile(ctx, source)
		source.initial = nil
		if ctx.Err() == nil {
			slog.Warn("managed core log file reader stopped", "path", source.path, "error", err)
			collector.setSourceStatus(source.engine, source.kind, "failed", coreLogErrorCode(err))
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (collector *CoreLogCollector) runSource(ctx context.Context, source coreLogJournalSource) {
	for ctx.Err() == nil {
		err := collector.follow(ctx, source)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("managed core journal reader stopped", "error", err)
		for _, engine := range source.unitEngines {
			collector.setSourceStatus(engine, "journal", "failed", coreLogErrorCode(err))
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (collector *CoreLogCollector) follow(ctx context.Context, source coreLogJournalSource) error {
	command := exec.CommandContext(ctx, journalctlPath, source.arguments...)
	configureCommand(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	output := &boundedOutput{limit: 8 << 10}
	command.Stderr = output
	if err := command.Start(); err != nil {
		return err
	}
	for _, engine := range source.unitEngines {
		collector.setSourceStatus(engine, "journal", "active", "")
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 16<<10), 256<<10)
	for scanner.Scan() {
		entry, cursor, ok := decodeJournalCoreLog(scanner.Bytes(), source.unitEngines)
		if ok {
			collector.appendJournal(entry, cursor)
		}
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		message := strings.TrimSpace(output.String())
		if message != "" {
			return errors.New(message)
		}
		return waitErr
	}
	return io.EOF
}

// followFile tails one supervise-daemon log file. Like journalctl --follow
// --lines=0, it starts at the current end of the file and only streams lines
// appended after the collector started.
func (collector *CoreLogCollector) followFile(ctx context.Context, source coreLogFileSource) error {
	var file *os.File
	missingBeforeOpen := false
	for file == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		opened, err := openValidatedCoreLogFileContext(ctx, source)
		if err == nil {
			file = opened
			collector.setSourceStatus(source.engine, source.kind, "active", "")
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missingBeforeOpen = true
		collector.setSourceStatus(source.engine, source.kind, "waiting", "source-missing")
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer file.Close()
	initialWhence := io.SeekEnd
	initialOffset := int64(0)
	if missingBeforeOpen {
		initialWhence = io.SeekStart
	}
	if source.initial != nil {
		initialWhence = io.SeekStart
		if source.initial.exists {
			info, statErr := file.Stat()
			device, inode, identityOK := coreLogFileIdentity(info)
			if statErr == nil && identityOK && device == source.initial.device && inode == source.initial.inode && info.Size() >= source.initial.offset {
				initialOffset = source.initial.offset
			}
		}
	}
	if _, err := file.Seek(initialOffset, initialWhence); err != nil {
		return err
	}
	offset := int64(0)
	if size, err := file.Seek(0, io.SeekCurrent); err == nil {
		offset = size
	}
	buffer := make([]byte, 16<<10)
	var partial []byte
	for ctx.Err() == nil {
		read, readErr := file.Read(buffer)
		if read > 0 {
			offset += int64(read)
			chunk := buffer[:read]
			for {
				index := bytes.IndexByte(chunk, '\n')
				if index < 0 {
					partial = append(partial, chunk...)
					if len(partial) > coreLogFileMaxLine {
						collector.appendFileEntry(source, partial)
						partial = nil
					}
					break
				}
				line := append(partial, chunk[:index]...)
				partial = nil
				chunk = chunk[index+1:]
				collector.appendFileEntry(source, line)
			}
		}
		if readErr == nil {
			continue
		}
		if !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if err := validateCoreLogSourceBinding(ctx, source); err != nil {
			return err
		}
		info, statErr := file.Stat()
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("managed core log file %s was replaced by a non-regular file", source.path)
		}
		probe, pathErr := openValidatedCoreLogFileContext(ctx, source)
		if pathErr != nil && !errors.Is(pathErr, os.ErrNotExist) {
			return pathErr
		}
		var pathInfo os.FileInfo
		if pathErr == nil {
			pathInfo, pathErr = probe.Stat()
			if pathErr != nil {
				probe.Close()
				return pathErr
			}
		}
		if pathErr == nil && !os.SameFile(info, pathInfo) {
			// The file was replaced under us (external rename rotation or a
			// service reinstall). Everything in the new inode is unread, so
			// reopen it and stream from its start.
			file.Close()
			file = probe
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return err
			}
			offset = 0
			partial = nil
			continue
		}
		if probe != nil {
			probe.Close()
		}
		if info.Size() < offset {
			// The file was truncated (rotation); re-read from the start so the
			// freshly written prefix is not missed.
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return err
			}
			offset = 0
			partial = nil
			continue
		}
		if source.kind == "openrc" && info.Size() >= coreLogFileRotateBytes {
			collector.rotateFile(source)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func coreLogFileIdentity(info os.FileInfo) (uint64, uint64, bool) {
	if info == nil {
		return 0, 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return uint64(stat.Dev), uint64(stat.Ino), true
}

// rotateFile copies the live file beside itself as a single .old snapshot
// and truncates the original. supervise-daemon keeps writing with O_APPEND,
// so truncation is seamless for the writer and bounds the volatile log
// footprint like the journald RuntimeMaxUse cap does on systemd. A failed
// snapshot only costs the archived copy; truncation still proceeds so a
// persistently failing copy can never let the live file grow unbounded.
func (collector *CoreLogCollector) rotateFile(source coreLogFileSource) {
	contents, err := os.ReadFile(source.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("managed core log rotation snapshot failed", "path", source.path, "error", err)
	}
	if len(contents) > 0 {
		if err := os.WriteFile(source.path+".old", contents, 0o600); err != nil {
			slog.Warn("managed core log rotation snapshot failed", "path", source.path, "error", err)
		}
	}
	if err := os.Truncate(source.path, 0); err != nil {
		slog.Warn("managed core log rotation failed", "path", source.path, "error", err)
	}
}

func (collector *CoreLogCollector) appendFileEntry(source coreLogFileSource, line []byte) {
	message := strings.TrimSpace(strings.ToValidUTF8(string(line), "�"))
	message = strings.ReplaceAll(message, "\x00", "�")
	if message == "" {
		return
	}
	if len(message) > core.MaxCoreLogMessageBytes {
		message = message[:core.MaxCoreLogMessageBytes]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	level := "info"
	if source.engine == core.EngineSingBox {
		level = singBoxLogLevel(message)
	}
	collector.append(core.CoreLogEntry{Engine: source.engine, Level: level, Message: message, LoggedAt: time.Now().UTC()})
}

func singBoxLogLevel(message string) string {
	upper := strings.ToUpper(message)
	for _, candidate := range []struct{ token, level string }{
		{" PANIC ", "critical"}, {" FATAL ", "critical"}, {" ERROR ", "error"},
		{" WARN ", "warning"}, {" DEBUG ", "debug"}, {" TRACE ", "debug"}, {" INFO ", "info"},
	} {
		if strings.Contains(upper, candidate.token) || strings.HasPrefix(upper, strings.TrimSpace(candidate.token)+" ") {
			return candidate.level
		}
	}
	return "info"
}

// managedOpenRCCoreServiceName matches the fixed OpenRC service names
// installed by bootstrap-core-services.sh (same names as systemd without the
// .service suffix).
func managedOpenRCCoreServiceName(service string) bool {
	switch service {
	case "qagent-mihomo", "qagent-xray", "qagent-sing-box", "qagent-shadowsocks-rust":
		return true
	default:
		return false
	}
}

// coreLogFileSources maps managed OpenRC services to their supervise-daemon
// output_log files. Pre-existing (non-managed) services are skipped: their
// log destination is not installed by QAgent and cannot be trusted.
func coreLogFileSources(specSets ...map[core.Engine]EngineSpec) []coreLogFileSource {
	engines := make(map[string]core.Engine)
	ambiguous := make(map[string]bool)
	for _, specs := range specSets {
		for engine, spec := range specs {
			if !managedOpenRCCoreServiceName(spec.Service) {
				continue
			}
			path := filepath.Join(openRCCoreLogRoot, spec.Service+".log")
			if ambiguous[path] {
				continue
			}
			if existing, ok := engines[path]; ok && existing != engine {
				delete(engines, path)
				ambiguous[path] = true
				continue
			}
			engines[path] = engine
		}
	}
	sources := make([]coreLogFileSource, 0, len(engines))
	for path, engine := range engines {
		sources = append(sources, coreLogFileSource{path: path, root: openRCCoreLogRoot, engine: engine, kind: "openrc"})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].path < sources[j].path })
	return sources
}

func (collector *CoreLogCollector) RefreshImportedSingBoxSource(executor *Executor) error {
	if executor == nil {
		return nil
	}
	executor.specsMu.RLock()
	spec, enabled := executor.Specs[core.EngineSingBox]
	executor.specsMu.RUnlock()
	if !enabled {
		collector.replaceImportedSingBoxSource(nil)
		return nil
	}
	info, err := os.Lstat(spec.ConfigPath)
	if err != nil {
		collector.failImportedSingBoxSource("config-unavailable")
		return err
	}
	file, _, err := openProtectedCoreMigrationFile(spec.ConfigPath, info, core.MaxConfigBytes)
	if err != nil {
		collector.failImportedSingBoxSource("config-unsafe")
		return err
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, core.MaxConfigBytes+1))
	file.Close()
	if readErr != nil || len(contents) > core.MaxConfigBytes {
		collector.failImportedSingBoxSource("config-unavailable")
		return errors.New("managed sing-box configuration is unavailable")
	}
	configDigest := coreMigrationConfigDigest(string(contents))
	output, destination, err := singBoxLogOutput(string(contents))
	if err != nil {
		collector.failImportedSingBoxSource("config-invalid")
		return err
	}
	if destination != singBoxLogDestinationFile {
		collector.mu.Lock()
		delete(collector.importWindows, core.EngineSingBox)
		collector.mu.Unlock()
		collector.replaceImportedSingBoxSource(nil)
		collector.selectConsoleSource(core.EngineSingBox)
		return nil
	}
	ownership, err := executor.completedMigrationOwnership(context.Background(), core.EngineSingBox, spec)
	if err != nil {
		collector.failImportedSingBoxSource("migration-state-invalid")
		return err
	}
	path, err := importedSingBoxLogPath(output)
	if err != nil {
		collector.failImportedSingBoxSource("source-outside-boundary")
		return err
	}
	source := coreLogFileSource{
		path: path, root: importedSingBoxLogRoot, engine: core.EngineSingBox, kind: "file",
		configPath: spec.ConfigPath, configDigest: configDigest, markerPrefix: executor.MigrationMarkerPrefix,
		executor: executor, ownership: ownership,
	}
	collector.mu.Lock()
	if window, ok := collector.importWindows[core.EngineSingBox]; ok {
		if window.path == path && window.configDigest == configDigest {
			copy := window
			source.initial = &copy
		}
		delete(collector.importWindows, core.EngineSingBox)
	}
	collector.mu.Unlock()
	collector.replaceImportedSingBoxSource(&source)
	collector.selectSourceKind(core.EngineSingBox, "file", CoreLogSourceStatus{Status: "waiting", Error: "source-missing"})
	return nil
}

func (collector *CoreLogCollector) PrepareImportedSingBoxSource(ctx context.Context, executor *Executor, content string) error {
	output, destination, err := singBoxLogOutput(content)
	if err != nil {
		return err
	}
	collector.mu.Lock()
	delete(collector.importWindows, core.EngineSingBox)
	collector.mu.Unlock()
	if destination != singBoxLogDestinationFile {
		return collector.waitForConsoleSource(ctx, core.EngineSingBox)
	}
	path, err := importedSingBoxLogPath(output)
	if err != nil {
		return err
	}
	window := coreLogImportWindow{path: path, configDigest: coreMigrationConfigDigest(content)}
	probe := coreLogFileSource{path: path, root: importedSingBoxLogRoot, engine: core.EngineSingBox, kind: "window"}
	file, err := openValidatedCoreLogFile(probe)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		info, statErr := file.Stat()
		file.Close()
		if statErr != nil {
			return statErr
		}
		device, inode, ok := coreLogFileIdentity(info)
		if !ok {
			return errors.New("managed sing-box log source has no stable file identity")
		}
		window.exists, window.device, window.inode, window.offset = true, device, inode, info.Size()
	}
	collector.mu.Lock()
	collector.importWindows[core.EngineSingBox] = window
	collector.mu.Unlock()
	return nil
}

func (collector *CoreLogCollector) waitForConsoleSource(ctx context.Context, engine core.Engine) error {
	collector.mu.Lock()
	ready := collector.sourceReady[engine]
	kind := collector.consoleKind[engine]
	if kind == "" {
		collector.mu.Unlock()
		return fmt.Errorf("managed %s log source is unsupported", engine)
	}
	if ready == nil {
		status := collector.kindStatus[engine][kind]
		collector.mu.Unlock()
		if status.Status == "failed" {
			return fmt.Errorf("managed %s log source is unavailable: %s", engine, status.Error)
		}
		return nil
	}
	collector.mu.Unlock()
	waitContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-ready:
		collector.mu.Lock()
		status := collector.kindStatus[engine][kind]
		collector.mu.Unlock()
		if status.Status == "failed" {
			return fmt.Errorf("managed %s log source is unavailable: %s", engine, status.Error)
		}
		return nil
	case <-waitContext.Done():
		collector.mu.Lock()
		status := collector.kindStatus[engine][kind]
		collector.mu.Unlock()
		if status.Status == "failed" {
			return fmt.Errorf("managed %s log source is unavailable: %s", engine, status.Error)
		}
		return fmt.Errorf("managed %s log source was not ready before service transition: %w", engine, waitContext.Err())
	}
}

func (collector *CoreLogCollector) replaceImportedSingBoxSource(source *coreLogFileSource) {
	collector.mu.Lock()
	filtered := collector.fileSources[:0]
	for _, existing := range collector.fileSources {
		if existing.engine == core.EngineSingBox && existing.kind == "file" {
			continue
		}
		filtered = append(filtered, existing)
	}
	collector.fileSources = filtered
	if source != nil {
		collector.fileSources = append(collector.fileSources, *source)
	}
	active := collector.activeFiles[string(core.EngineSingBox)+"\x00file"]
	running := collector.runContext != nil && !collector.runStopped
	if source == nil && active != nil {
		active.cancel()
		delete(collector.activeFiles, string(core.EngineSingBox)+"\x00file")
	}
	collector.mu.Unlock()
	if source != nil && running {
		collector.startFileSource(*source)
	}
}

func (collector *CoreLogCollector) selectConsoleSource(engine core.Engine) {
	collector.mu.Lock()
	kind := collector.consoleKind[engine]
	collector.mu.Unlock()
	if kind == "" {
		kind = "journal"
	}
	collector.selectSourceKind(engine, kind, CoreLogSourceStatus{Status: "waiting", Error: "collector-starting"})
}

func (collector *CoreLogCollector) failImportedSingBoxSource(code string) {
	collector.mu.Lock()
	delete(collector.importWindows, core.EngineSingBox)
	collector.mu.Unlock()
	collector.replaceImportedSingBoxSource(nil)
	collector.selectSourceKind(core.EngineSingBox, "file", CoreLogSourceStatus{Status: "failed", Error: code})
}

type singBoxLogDestination uint8

const (
	singBoxLogDestinationConsole singBoxLogDestination = iota
	singBoxLogDestinationDisabled
	singBoxLogDestinationFile
)

func singBoxLogOutput(content string) (string, singBoxLogDestination, error) {
	decoded, err := decodeExtendedJSON(content)
	if err != nil {
		return "", singBoxLogDestinationConsole, err
	}
	root, _ := decoded.(map[string]any)
	logging, _ := root["log"].(map[string]any)
	if logging == nil {
		return "", singBoxLogDestinationConsole, nil
	}
	disabled, _ := logging["disabled"].(bool)
	output, _ := logging["output"].(string)
	if disabled {
		return output, singBoxLogDestinationDisabled, nil
	}
	if output == "" || output == "stdout" || output == "stderr" {
		return output, singBoxLogDestinationConsole, nil
	}
	return output, singBoxLogDestinationFile, nil
}

func importedSingBoxLogPath(output string) (string, error) {
	if strings.ContainsAny(output, "\x00\r\n") {
		return "", errors.New("sing-box log output contains a control character")
	}
	path := filepath.Clean(output)
	if !filepath.IsAbs(path) {
		path = filepath.Join(importedSingBoxLogRoot, path)
	}
	if path == importedSingBoxLogRoot || !pathWithin(path, importedSingBoxLogRoot) {
		return "", errors.New("sing-box log output is outside the managed state directory")
	}
	return path, nil
}

func openValidatedCoreLogFile(source coreLogFileSource) (*os.File, error) {
	return openValidatedCoreLogFileContext(context.Background(), source)
}

func openValidatedCoreLogFileContext(ctx context.Context, source coreLogFileSource) (*os.File, error) {
	if source.root == "" || !filepath.IsAbs(source.path) || !pathWithin(source.path, source.root) {
		return nil, errors.New("core log source is outside its protected root")
	}
	if err := validateCoreLogSourceBinding(ctx, source); err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(source.root)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() || rootInfo.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("core log source root is unsafe")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(source.root)); err != nil {
		return nil, err
	}
	rootUID, _, rootOwnerKnown := fileOwnership(rootInfo)
	for directory := filepath.Dir(source.path); directory != filepath.Dir(source.root); directory = filepath.Dir(directory) {
		info, statErr := os.Lstat(directory)
		if statErr != nil {
			return nil, statErr
		}
		uid, _, ownerKnown := fileOwnership(info)
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o022 != 0 || (rootOwnerKnown && ownerKnown && uid != rootUID) {
			return nil, errors.New("core log source parent is unsafe")
		}
		if directory == source.root {
			break
		}
	}
	expected, err := os.Lstat(source.path)
	if err != nil {
		return nil, err
	}
	uid, _, ownerKnown := fileOwnership(expected)
	if expected.Mode()&os.ModeSymlink != 0 || !expected.Mode().IsRegular() || expected.Mode().Perm()&0o022 != 0 ||
		(source.kind == "file" && rootOwnerKnown && ownerKnown && uid != rootUID) {
		return nil, errors.New("core log source is not a protected regular file")
	}
	file, err := openCoreLogFileNoSymlinks(source.root, source.path, rootUID, rootOwnerKnown)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(expected, opened) || !opened.Mode().IsRegular() ||
		metadataFromFileInfo(opened) != metadataFromFileInfo(expected) {
		file.Close()
		return nil, errors.New("core log source changed while it was being opened")
	}
	return file, nil
}

func openCoreLogFileNoSymlinks(rootPath, path string, expectedUID int, ownerKnown bool) (*os.File, error) {
	relative, err := filepath.Rel(rootPath, path)
	if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("core log source escapes its root")
	}
	rootFD, err := syscall.Open(rootPath, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	if err := validateCoreLogDirectoryFD(rootFD, expectedUID, ownerKnown); err != nil {
		syscall.Close(rootFD)
		return nil, err
	}
	currentFD := rootFD
	parts := strings.Split(relative, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		nextFD, openErr := syscall.Openat(currentFD, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		if currentFD != rootFD {
			syscall.Close(currentFD)
		}
		if openErr != nil {
			syscall.Close(rootFD)
			return nil, openErr
		}
		if err := validateCoreLogDirectoryFD(nextFD, expectedUID, ownerKnown); err != nil {
			syscall.Close(nextFD)
			syscall.Close(rootFD)
			return nil, err
		}
		currentFD = nextFD
	}
	fileFD, err := syscall.Openat(currentFD, parts[len(parts)-1], syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if currentFD != rootFD {
		syscall.Close(currentFD)
	}
	syscall.Close(rootFD)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fileFD), path), nil
}

func validateCoreLogDirectoryFD(fd, expectedUID int, ownerKnown bool) error {
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFDIR || stat.Mode&0o022 != 0 || (ownerKnown && int(stat.Uid) != expectedUID) {
		return errors.New("core log source directory changed while it was being opened")
	}
	return nil
}

func validateCoreLogSourceBinding(ctx context.Context, source coreLogFileSource) error {
	if source.kind != "file" {
		return nil
	}
	if source.executor == nil || source.engine != core.EngineSingBox {
		return errors.New("core log source has no verified migration owner")
	}
	ownership, err := source.executor.completedMigrationOwnership(ctx, source.engine, source.ownership.Managed)
	if err != nil || ownership != source.ownership {
		return errors.New("core log source migration ownership drifted")
	}
	record, err := readCoreMigrationRecord(source.markerPrefix, source.engine)
	if err != nil || record.State != coreMigrationComplete || record.SourceDigest != source.ownership.SourceDigest {
		return errors.New("core log source is not bound to a completed migration")
	}
	digest, exists, err := protectedCoreMigrationFileDigest(source.configPath, core.MaxConfigBytes)
	if err != nil || !exists || digest != source.configDigest {
		return errors.New("core log source configuration drifted")
	}
	return nil
}

func (e *Executor) completedMigrationOwnership(parent context.Context, engine core.Engine, managed EngineSpec) (completedCoreMigration, error) {
	if e == nil || e.MigrationMarkerPrefix == "" {
		return completedCoreMigration{}, errors.New("core migration ownership is unavailable")
	}
	e.specsMu.RLock()
	currentManaged, enabled := e.Specs[engine]
	_, pending := e.ExistingSpecs[engine]
	issue := strings.TrimSpace(e.ExistingDiscoveryIssues[engine])
	ownership, completed := e.completedMigrations[engine]
	e.specsMu.RUnlock()
	if !enabled || currentManaged != managed || pending || issue != "" || !completed || ownership.Managed != managed {
		return completedCoreMigration{}, errors.New("core migration is not in a verified managed ownership state")
	}
	record, err := readCoreMigrationRecord(e.MigrationMarkerPrefix, engine)
	if err != nil || record.State != coreMigrationComplete || record.SourceDigest != ownership.SourceDigest {
		return completedCoreMigration{}, errors.New("core migration marker does not match verified ownership")
	}
	verifyContext, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	verify := e.verifyCompletedMigration
	if verify == nil {
		verify = func(ctx context.Context, existing, managed EngineSpec, manager *ServiceManager) error {
			return verifyCoreMigrationCompletionState(ctx, existing, managed, manager)
		}
	}
	if err := verify(verifyContext, ownership.Existing, managed, e.serviceManager()); err != nil {
		return completedCoreMigration{}, fmt.Errorf("core migration service ownership drifted: %w", err)
	}
	return ownership, nil
}

func coreLogJournalSources(specSets ...map[core.Engine]EngineSpec) []coreLogJournalSource {
	managed := make(map[string]core.Engine)
	generic := make(map[string]core.Engine)
	managedAmbiguous := make(map[string]bool)
	genericAmbiguous := make(map[string]bool)
	for _, specs := range specSets {
		for engine, spec := range specs {
			if managedCoreServiceName(spec.Service) {
				addCoreLogUnit(managed, managedAmbiguous, spec.Service, engine)
				continue
			}
			switch {
			case engine == core.EngineXray && spec.Service == "xray.service":
				addCoreLogUnit(generic, genericAmbiguous, spec.Service, engine)
			case engine == core.EngineSingBox && (spec.Service == "sing-box.service" || spec.Service == "singbox.service"):
				addCoreLogUnit(generic, genericAmbiguous, spec.Service, engine)
			}
		}
	}
	sources := make([]coreLogJournalSource, 0, 2)
	if len(managed) > 0 {
		sources = append(sources, newCoreLogJournalSource(managed, true))
	}
	if len(generic) > 0 {
		sources = append(sources, newCoreLogJournalSource(generic, false))
	}
	return sources
}

func addCoreLogUnit(units map[string]core.Engine, ambiguous map[string]bool, unit string, engine core.Engine) {
	if ambiguous[unit] {
		return
	}
	if existing, ok := units[unit]; ok && existing != engine {
		delete(units, unit)
		ambiguous[unit] = true
		return
	}
	units[unit] = engine
}

func newCoreLogJournalSource(units map[string]core.Engine, managedNamespace bool) coreLogJournalSource {
	arguments := []string{"--follow", "--lines=0", "--output=json",
		"--output-fields=MESSAGE,_SYSTEMD_UNIT,PRIORITY,__REALTIME_TIMESTAMP,__CURSOR"}
	if managedNamespace {
		arguments = append([]string{"--namespace=qagent-cores"}, arguments...)
	}
	names := make([]string, 0, len(units))
	copyMap := make(map[string]core.Engine, len(units))
	for unit, engine := range units {
		names = append(names, unit)
		copyMap[unit] = engine
	}
	sort.Strings(names)
	for _, unit := range names {
		arguments = append(arguments, "--unit="+unit)
	}
	return coreLogJournalSource{arguments: arguments, unitEngines: copyMap}
}

func (collector *CoreLogCollector) append(entry core.CoreLogEntry) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.appendLocked(entry)
}

func (collector *CoreLogCollector) appendJournal(entry core.CoreLogEntry, cursor string) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if cursor != "" {
		if _, duplicate := collector.seenCursors[cursor]; duplicate {
			return
		}
		if collector.seenCursors == nil {
			collector.seenCursors = make(map[string]struct{})
		}
		collector.seenCursors[cursor] = struct{}{}
		collector.cursorOrder = append(collector.cursorOrder, cursor)
		if len(collector.cursorOrder) > coreLogQueueLimit*2 {
			oldest := collector.cursorOrder[0]
			collector.cursorOrder = collector.cursorOrder[1:]
			delete(collector.seenCursors, oldest)
		}
	}
	collector.appendLocked(entry)
}

func (collector *CoreLogCollector) appendLocked(entry core.CoreLogEntry) {
	for len(collector.queued)+pendingCoreLogCount(collector.pending) >= coreLogQueueLimit && len(collector.queued) > 0 {
		collector.queued = collector.queued[1:]
		collector.dropped++
	}
	if len(collector.queued)+pendingCoreLogCount(collector.pending) >= coreLogQueueLimit {
		collector.dropped++
		return
	}
	collector.queued = append(collector.queued, entry)
}

func (collector *CoreLogCollector) NextBatch() *core.CoreLogBatch {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if collector.pending != nil {
		return cloneCoreLogBatch(collector.pending)
	}
	if collector.dropped > 0 {
		slog.Warn("managed core log lines dropped from full in-memory queue", "count", collector.dropped)
		collector.dropped = 0
	}
	if len(collector.queued) == 0 {
		return nil
	}
	batchID, err := core.NewID("log")
	if err != nil {
		return nil
	}
	count := min(len(collector.queued), core.MaxCoreLogBatchEntries)
	entries := append([]core.CoreLogEntry(nil), collector.queued[:count]...)
	collector.queued = collector.queued[count:]
	collector.pending = &core.CoreLogBatch{ID: batchID, Entries: entries}
	return cloneCoreLogBatch(collector.pending)
}

func (collector *CoreLogCollector) Acknowledge(batchID string) bool {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if collector.pending == nil || collector.pending.ID != batchID {
		return false
	}
	collector.pending = nil
	return true
}

func pendingCoreLogCount(batch *core.CoreLogBatch) int {
	if batch == nil {
		return 0
	}
	return len(batch.Entries)
}

func cloneCoreLogBatch(batch *core.CoreLogBatch) *core.CoreLogBatch {
	if batch == nil {
		return nil
	}
	return &core.CoreLogBatch{ID: batch.ID, Entries: append([]core.CoreLogEntry(nil), batch.Entries...)}
}

func decodeJournalCoreLog(value []byte, unitEngines map[string]core.Engine) (core.CoreLogEntry, string, bool) {
	var record map[string]any
	if json.Unmarshal(value, &record) != nil {
		return core.CoreLogEntry{}, "", false
	}
	engine, ok := coreLogEngineForUnit(stringField(record["_SYSTEMD_UNIT"]), unitEngines)
	if !ok {
		return core.CoreLogEntry{}, "", false
	}
	message := strings.TrimSpace(strings.ToValidUTF8(stringField(record["MESSAGE"]), "�"))
	message = strings.ReplaceAll(message, "\x00", "�")
	if message == "" {
		return core.CoreLogEntry{}, "", false
	}
	if len(message) > core.MaxCoreLogMessageBytes {
		message = message[:core.MaxCoreLogMessageBytes]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	loggedAt := time.Now().UTC()
	if microseconds, err := strconv.ParseInt(stringField(record["__REALTIME_TIMESTAMP"]), 10, 64); err == nil && microseconds > 0 {
		loggedAt = time.UnixMicro(microseconds).UTC()
	}
	priority, err := strconv.Atoi(stringField(record["PRIORITY"]))
	if err != nil {
		priority = 6
	}
	return core.CoreLogEntry{Engine: engine, Level: coreLogLevelForPriority(priority), Message: message, LoggedAt: loggedAt}, stringField(record["__CURSOR"]), true
}

func stringField(value any) string {
	result, _ := value.(string)
	return result
}

func coreLogEngineForUnit(unit string, unitEngines map[string]core.Engine) (core.Engine, bool) {
	engine, ok := unitEngines[unit]
	return engine, ok
}

func coreLogLevelForPriority(priority int) string {
	switch {
	case priority <= 2:
		return "critical"
	case priority == 3:
		return "error"
	case priority == 4:
		return "warning"
	case priority >= 7:
		return "debug"
	default:
		return "info"
	}
}
