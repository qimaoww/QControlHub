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
	"regexp"
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
	// Align the volatile budget with the journald Storage=volatile cap by
	// rotating to a single .old copy once the live file grows past this size.
	coreLogFileRotateBytes = 8 << 20
	coreLogFileMaxLine     = 256 << 10
	coreLogRevalidateBytes = 256 << 10
	coreLogRevalidateEvery = time.Second
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
	mu            sync.Mutex
	filePublishMu sync.Mutex
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
	transitions   map[core.Engine]*coreLogSourceTransition
	nextFileEpoch uint64
	sourceReady   map[core.Engine]chan struct{}
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

func (collector *CoreLogCollector) startFileSource(source coreLogFileSource) {
	collector.mu.Lock()
	if collector.runStopped {
		collector.mu.Unlock()
		return
	}
	if source.kind == "file" && collector.transitions[source.engine] != nil {
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
	run := &coreLogFileRun{cancel: cancel, bindingID: bindingID, done: make(chan struct{}), source: source}
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
			close(run.done)
			collector.runWait.Done()
		}()
		collector.runFileSource(fileContext, source, run)
	}()
}

func coreLogFileSourceKey(source coreLogFileSource) string {
	if source.kind == "file" {
		return string(source.engine) + "\x00file"
	}
	return string(source.engine) + "\x00" + source.kind + "\x00" + source.path
}

func coreLogFileBindingID(source coreLogFileSource) string {
	return source.path + "\x00" + source.configDigest + "\x00" + source.ownership.SourceDigest + "\x00" + strconv.FormatUint(source.epoch, 10)
}

// runFileSource keeps one tail reader alive per managed OpenRC log file with
// the same retry cadence as the journal readers.
func (collector *CoreLogCollector) runFileSource(ctx context.Context, source coreLogFileSource, run *coreLogFileRun) {
	for ctx.Err() == nil {
		err := collector.followFile(ctx, source, run)
		source.initial = nil
		if ctx.Err() == nil {
			slog.Warn("managed core log file reader stopped", "path", source.path, "error", err)
			collector.setFileSourceStatus(source, "failed", coreLogErrorCode(err))
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
	resume := ""
	for ctx.Err() == nil {
		nextResume, err := collector.follow(ctx, source, resume)
		if nextResume != "" {
			resume = nextResume
		}
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

func (collector *CoreLogCollector) follow(ctx context.Context, source coreLogJournalSource, resume string) (string, error) {
	if resume == "" {
		var err error
		resume, err = captureJournalPosition(ctx, source)
		if err != nil {
			return "", err
		}
	}
	command := exec.CommandContext(ctx, journalctlPath, journalFollowArguments(source.arguments, resume)...)
	configureCommand(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return resume, err
	}
	output := &boundedOutput{limit: 8 << 10}
	command.Stderr = output
	if err := command.Start(); err != nil {
		return resume, err
	}
	// The separately captured tail position makes this readiness declaration
	// safe: records written after that cursor (or the bounded timestamp used for
	// an empty journal) remain readable while journalctl completes its startup.
	for _, engine := range source.unitEngines {
		collector.setSourceStatus(engine, "journal", "active", "")
	}
	lastResume := resume
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 16<<10), 256<<10)
	for scanner.Scan() {
		entry, cursor, ok := decodeJournalCoreLog(scanner.Bytes(), source.unitEngines)
		if validJournalCursor(cursor) {
			lastResume = "--after-cursor=" + cursor
		}
		if ok {
			collector.appendJournal(entry, cursor)
		}
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return lastResume, ctx.Err()
	}
	if scanErr != nil {
		return lastResume, scanErr
	}
	if waitErr != nil {
		message := strings.TrimSpace(output.String())
		if message != "" {
			return lastResume, errors.New(message)
		}
		return lastResume, waitErr
	}
	return lastResume, io.EOF
}

func captureJournalPosition(ctx context.Context, source coreLogJournalSource) (string, error) {
	// Record the bounded fallback before probing the journal tail. A fresh
	// namespace may not contain any entry and therefore cannot return a cursor;
	// --since still covers every record created while the follower is starting.
	capturedAt := time.Now().UTC()
	command := exec.CommandContext(ctx, journalctlPath, journalCursorArguments(source.arguments)...)
	configureCommand(command)
	stdout := &boundedOutput{limit: 16 << 10}
	stderr := &boundedOutput{limit: 8 << 10}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return "", errors.New(message)
		}
		return "", err
	}
	if stdout.Truncated() || stderr.Truncated() {
		return "", errors.New("journal cursor response exceeded the output limit")
	}
	lines := strings.Split(stdout.String(), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, "-- cursor:") {
			continue
		}
		cursor := strings.TrimSpace(strings.TrimPrefix(line, "-- cursor:"))
		if validJournalCursor(cursor) {
			return "--after-cursor=" + cursor, nil
		}
		return "", errors.New("journal cursor response was invalid")
	}
	seconds := capturedAt.Unix()
	microseconds := capturedAt.Nanosecond() / int(time.Microsecond)
	return fmt.Sprintf("--since=@%d.%06d", seconds, microseconds), nil
}

func journalCursorArguments(arguments []string) []string {
	result := make([]string, 0, len(arguments)+4)
	for _, argument := range arguments {
		if argument == "--follow" || strings.HasPrefix(argument, "--lines") ||
			strings.HasPrefix(argument, "--output") || strings.HasPrefix(argument, "--after-cursor=") ||
			strings.HasPrefix(argument, "--since=") || strings.HasPrefix(argument, "--unit=") {
			continue
		}
		result = append(result, argument)
	}
	return append(result, "--no-pager", "--quiet", "--lines=0", "--show-cursor")
}

func journalFollowArguments(arguments []string, resume string) []string {
	result := make([]string, 0, len(arguments)+1)
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "--lines") || strings.HasPrefix(argument, "--after-cursor=") ||
			strings.HasPrefix(argument, "--since=") {
			continue
		}
		result = append(result, argument)
	}
	return append(result, resume)
}

func validJournalCursor(cursor string) bool {
	if cursor == "" || len(cursor) > 4096 {
		return false
	}
	for _, character := range cursor {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

// followFile tails one supervise-daemon log file. Like journalctl --follow
// --lines=0, it starts at the current end of the file and only streams lines
// appended after the collector started.
func (collector *CoreLogCollector) followFile(ctx context.Context, source coreLogFileSource, run *coreLogFileRun) error {
	var file *os.File
	var trusted *validatedCoreLogFile
	missingBeforeOpen := false
	for file == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		opened, err := openValidatedCoreLogFileContext(ctx, source)
		if err == nil {
			trusted = opened
			file = opened.file
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missingBeforeOpen = true
		collector.setFileSourceStatus(source, "waiting", "source-missing")
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer func() { _ = file.Close() }()
	if source.beforeInitialCursor != nil {
		source.beforeInitialCursor()
	}
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
	openedIdentity, err := validateOpenedCoreLogFile(source, file, trusted)
	if err != nil {
		return err
	}
	collector.filePublishMu.Lock()
	if err := ctx.Err(); err != nil {
		collector.filePublishMu.Unlock()
		return err
	}
	openedIdentity, err = validateOpenedCoreLogFile(source, file, trusted)
	if err != nil {
		collector.filePublishMu.Unlock()
		return err
	}
	setCoreLogFileRunProgress(run, source.path, openedIdentity, offset)
	collector.setFileSourceStatus(source, "active", "")
	collector.filePublishMu.Unlock()
	// A single Fstat gates every block before any byte can enter partial state or
	// the shared queue. A 64 KiB block keeps that fail-closed check bounded
	// without imposing one metadata syscall per small log line.
	buffer := make([]byte, 64<<10)
	var partial []byte
	bytesSinceValidation := int64(0)
	nextValidation := time.Now().Add(coreLogRevalidateEvery)
	for ctx.Err() == nil {
		read, readErr := file.Read(buffer)
		if read > 0 {
			if err := collector.publishFileBlock(ctx, file, source, run, trusted,
				buffer[:read], &partial, &offset); err != nil {
				return err
			}
			bytesSinceValidation += int64(read)
		}
		if readErr == nil && bytesSinceValidation < coreLogRevalidateBytes && time.Now().Before(nextValidation) {
			continue
		}
		atEOF := errors.Is(readErr, io.EOF)
		if readErr != nil && !atEOF {
			return readErr
		}
		if err := validateCoreLogSourceBinding(ctx, source); err != nil {
			return err
		}
		bytesSinceValidation = 0
		nextValidation = time.Now().Add(coreLogRevalidateEvery)
		info, statErr := validateOpenedCoreLogFile(source, file, trusted)
		if statErr != nil {
			return statErr
		}
		probe, pathErr := openValidatedCoreLogFileContext(ctx, source)
		if pathErr != nil && !errors.Is(pathErr, os.ErrNotExist) {
			return pathErr
		}
		var pathInfo os.FileInfo
		if pathErr == nil {
			pathInfo = probe.identity
		}
		if pathErr == nil && !os.SameFile(info, pathInfo) {
			// The file was replaced under us (external rename rotation or a
			// service reinstall). Everything in the new inode is unread, so
			// reopen it and stream from its start.
			collector.filePublishMu.Lock()
			if err := ctx.Err(); err != nil {
				collector.filePublishMu.Unlock()
				probe.file.Close()
				return err
			}
			pathInfo, err = validateOpenedCoreLogFile(source, probe.file, probe)
			if err != nil {
				collector.filePublishMu.Unlock()
				probe.file.Close()
				return err
			}
			if _, err := probe.file.Seek(0, io.SeekStart); err != nil {
				collector.filePublishMu.Unlock()
				probe.file.Close()
				return err
			}
			pathInfo, err = validateOpenedCoreLogFile(source, probe.file, probe)
			if err != nil {
				collector.filePublishMu.Unlock()
				probe.file.Close()
				return err
			}
			file.Close()
			trusted = probe
			file = probe.file
			offset = 0
			partial = nil
			setCoreLogFileRunProgress(run, source.path, pathInfo, 0)
			collector.filePublishMu.Unlock()
			continue
		}
		if probe != nil {
			probe.file.Close()
		}
		if info.Size() < offset {
			// The file was truncated (rotation); re-read from the start so the
			// freshly written prefix is not missed.
			collector.filePublishMu.Lock()
			if err := ctx.Err(); err != nil {
				collector.filePublishMu.Unlock()
				return err
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				collector.filePublishMu.Unlock()
				return err
			}
			offset = 0
			partial = nil
			setCoreLogFileRunProgress(run, source.path, info, 0)
			collector.filePublishMu.Unlock()
			continue
		}
		if !atEOF {
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

func (collector *CoreLogCollector) publishFileBlock(ctx context.Context, file *os.File, source coreLogFileSource,
	run *coreLogFileRun, trusted *validatedCoreLogFile, chunk []byte, partial *[]byte, offset *int64) error {
	collector.filePublishMu.Lock()
	defer collector.filePublishMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	openedInfo, err := validateOpenedCoreLogFile(source, file, trusted)
	if err != nil {
		return err
	}
	*offset += int64(len(chunk))
	for {
		index := bytes.IndexByte(chunk, '\n')
		if index < 0 {
			*partial = append(*partial, chunk...)
			if len(*partial) > coreLogFileMaxLine {
				collector.appendFileEntry(source, *partial)
				*partial = nil
			}
			break
		}
		line := append(*partial, chunk[:index]...)
		*partial = nil
		chunk = chunk[index+1:]
		collector.appendFileEntry(source, line)
	}
	setCoreLogFileRunProgress(run, source.path, openedInfo, *offset-int64(len(*partial)))
	return nil
}

func setCoreLogFileRunProgress(run *coreLogFileRun, path string, info os.FileInfo, offset int64) {
	device, inode, ok := coreLogFileIdentity(info)
	if run == nil || !ok || offset < 0 {
		return
	}
	run.progress = coreLogImportWindow{path: path, exists: true, device: device, inode: inode, offset: offset}
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
	message := sanitizeCoreLogMessage(string(line))
	if message == "" {
		return
	}
	level := "info"
	if source.engine == core.EngineSingBox {
		level = singBoxLogLevel(message)
	}
	collector.append(core.CoreLogEntry{Engine: source.engine, Level: level, Message: message, LoggedAt: time.Now().UTC()})
}

// ansiControlPattern matches ANSI escape sequences (CSI) such as the color
// codes sing-box emits (`ESC[32m`, `ESC[0m`). They are display-only bytes and
// must not leak into the core-log panel as visible `[36m`-style text.
var ansiControlPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// sanitizeCoreLogMessage repairs encoding, drops NUL and ANSI control bytes,
// and bounds the resulting line before it is enqueued for display.
func sanitizeCoreLogMessage(raw string) string {
	message := strings.TrimSpace(strings.ToValidUTF8(raw, "�"))
	message = strings.ReplaceAll(message, "\x00", "�")
	message = ansiControlPattern.ReplaceAllString(message, "")
	// Remove any surviving ESC introducer so a lone control byte cannot render.
	message = strings.ReplaceAll(message, "\x1b", "")
	if message == "" {
		return ""
	}
	if len(message) > core.MaxCoreLogMessageBytes {
		message = message[:core.MaxCoreLogMessageBytes]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return message
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
	return collector.refreshImportedSingBoxSource(executor, nil)
}

func (collector *CoreLogCollector) CompleteImportedSingBoxSource(executor *Executor, succeeded bool) error {
	return collector.refreshImportedSingBoxSource(executor, &succeeded)
}

func (collector *CoreLogCollector) refreshImportedSingBoxSource(executor *Executor, succeeded *bool) error {
	if executor == nil {
		return nil
	}
	executor.specsMu.RLock()
	spec, enabled := executor.Specs[core.EngineSingBox]
	executor.specsMu.RUnlock()
	if !enabled {
		if succeeded != nil {
			collector.failImportedSingBoxSource("transition-state-invalid")
			return errors.New("managed sing-box configuration disappeared during source transition")
		}
		collector.replaceImportedSingBoxSource(nil)
		return nil
	}
	info, err := os.Lstat(spec.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, binaryErr := os.Lstat(spec.Binary); errors.Is(binaryErr, os.ErrNotExist) {
				collector.replaceImportedSingBoxSource(nil)
				return nil
			}
		}
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
	collector.mu.Lock()
	transition := collector.transitions[core.EngineSingBox]
	collector.mu.Unlock()
	if succeeded == nil && transition != nil {
		return errors.New("managed sing-box log source transition is still in progress")
	}
	if succeeded != nil {
		if transition == nil {
			collector.failImportedSingBoxSource("transition-state-invalid")
			return errors.New("managed sing-box log source transition is unavailable")
		}
		expectedDigest := transition.previousDigest
		if *succeeded {
			expectedDigest = transition.targetDigest
		}
		if expectedDigest == "" || configDigest != expectedDigest {
			collector.failImportedSingBoxSource("transition-state-invalid")
			return errors.New("managed sing-box configuration does not match the completed source transition")
		}
	}
	output, destination, err := singBoxLogOutput(string(contents))
	if err != nil {
		collector.failImportedSingBoxSource("config-invalid")
		return err
	}
	if destination != singBoxLogDestinationFile {
		if transition != nil {
			collector.completeImportedSingBoxSource(nil)
		} else {
			collector.replaceImportedSingBoxSource(nil)
		}
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
	reused := false
	collector.mu.Lock()
	if transition != nil {
		window, ok := transition.windows[path]
		if !ok {
			collector.mu.Unlock()
			collector.failImportedSingBoxSource("transition-source-invalid")
			return errors.New("managed sing-box final log source was not captured before transition")
		}
		copy := window
		source.initial = &copy
		source.epoch = transition.epoch
	} else {
		source.epoch, reused = collector.importedSourceEpochLocked(source)
	}
	collector.mu.Unlock()
	if transition != nil {
		collector.selectSourceStatus(core.EngineSingBox, "file", "waiting", "source-transition")
		collector.completeImportedSingBoxSource(&source)
	} else {
		if reused {
			collector.replaceImportedSingBoxSource(&source)
			collector.selectSourceKind(core.EngineSingBox, "file", CoreLogSourceStatus{Status: "waiting", Error: "source-missing"})
		} else {
			collector.selectSourceStatus(core.EngineSingBox, "file", "waiting", "source-missing")
			collector.replaceImportedSingBoxSource(&source)
		}
	}
	return nil
}

func (collector *CoreLogCollector) PrepareImportedSingBoxSource(ctx context.Context, executor *Executor, content string) error {
	output, destination, err := singBoxLogOutput(content)
	if err != nil {
		return err
	}
	if destination != singBoxLogDestinationFile {
		if err := collector.waitForConsoleSource(ctx, core.EngineSingBox); err != nil {
			return err
		}
	}
	targetPath := ""
	if destination == singBoxLogDestinationFile {
		targetPath, err = importedSingBoxLogPath(output)
		if err != nil {
			return err
		}
	}
	previousDigest := ""
	if executor != nil {
		executor.specsMu.RLock()
		spec, enabled := executor.Specs[core.EngineSingBox]
		executor.specsMu.RUnlock()
		if enabled {
			var exists bool
			previousDigest, exists, err = protectedCoreMigrationFileDigest(spec.ConfigPath, core.MaxConfigBytes)
			if err != nil {
				return err
			}
			if !exists {
				previousDigest = ""
			}
		}
	}

	collector.filePublishMu.Lock()
	collector.mu.Lock()
	if collector.transitions[core.EngineSingBox] != nil {
		collector.mu.Unlock()
		collector.filePublishMu.Unlock()
		return errors.New("managed sing-box log source transition is already in progress")
	}
	var currentSource *coreLogFileSource
	for index := range collector.fileSources {
		source := &collector.fileSources[index]
		if source.engine == core.EngineSingBox && source.kind == "file" {
			copy := *source
			currentSource = &copy
			break
		}
	}
	key := string(core.EngineSingBox) + "\x00file"
	active := collector.activeFiles[key]
	collector.nextFileEpoch++
	transition := &coreLogSourceTransition{
		epoch: collector.nextFileEpoch, previousDigest: previousDigest,
		targetDigest: coreMigrationConfigDigest(content), windows: make(map[string]coreLogImportWindow),
	}
	collector.transitions[core.EngineSingBox] = transition
	if active != nil {
		active.cancel()
	}
	collector.mu.Unlock()

	paths := make(map[string]struct{})
	if currentSource != nil {
		paths[currentSource.path] = struct{}{}
	}
	if targetPath != "" {
		paths[targetPath] = struct{}{}
	}
	for path := range paths {
		var progress *coreLogImportWindow
		if active != nil && active.source.path == path && active.progress.path == path {
			copy := active.progress
			progress = &copy
		}
		window, captureErr := captureCoreLogImportWindow(path, progress)
		if captureErr != nil {
			collector.mu.Lock()
			if collector.transitions[core.EngineSingBox] == transition {
				delete(collector.transitions, core.EngineSingBox)
			}
			collector.mu.Unlock()
			collector.filePublishMu.Unlock()
			collector.waitAndRestoreFileSource(active, currentSource)
			return captureErr
		}
		transition.windows[path] = window
	}
	collector.filePublishMu.Unlock()
	if err := waitCoreLogFileRun(active, 5*time.Second); err != nil {
		collector.mu.Lock()
		if collector.transitions[core.EngineSingBox] == transition {
			delete(collector.transitions, core.EngineSingBox)
		}
		collector.mu.Unlock()
		collector.waitAndRestoreFileSource(active, currentSource)
		return err
	}
	collector.selectSourceStatus(core.EngineSingBox, "file", "waiting", "source-transition")
	return nil
}

func captureCoreLogImportWindow(path string, progress *coreLogImportWindow) (coreLogImportWindow, error) {
	window := coreLogImportWindow{path: path}
	probe := coreLogFileSource{path: path, root: importedSingBoxLogRoot, engine: core.EngineSingBox, kind: "window"}
	file, err := openValidatedCoreLogFile(probe)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return window, nil
		}
		return coreLogImportWindow{}, err
	}
	info, statErr := file.Stat()
	file.Close()
	if statErr != nil {
		return coreLogImportWindow{}, statErr
	}
	device, inode, ok := coreLogFileIdentity(info)
	if !ok {
		return coreLogImportWindow{}, errors.New("managed sing-box log source has no stable file identity")
	}
	window.exists, window.device, window.inode, window.offset = true, device, inode, info.Size()
	if progress != nil && progress.exists && progress.device == device && progress.inode == inode && progress.offset >= 0 {
		if info.Size() >= progress.offset {
			window.offset = progress.offset
		} else {
			// A truncate between the last published block and transition
			// ownership resets the safe replay point to the new file prefix.
			window.offset = 0
		}
	}
	return window, nil
}

func waitCoreLogFileRun(run *coreLogFileRun, timeout time.Duration) error {
	if run == nil {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-run.done:
		return nil
	case <-timer.C:
		return errors.New("managed core log source did not stop for transition")
	}
}

func (collector *CoreLogCollector) waitAndRestoreFileSource(run *coreLogFileRun, source *coreLogFileSource) {
	_ = waitCoreLogFileRun(run, 5*time.Second)
	if source != nil {
		collector.startFileSource(*source)
	}
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
	collector.replaceImportedSingBoxSourceLocked(source, false)
}

func (collector *CoreLogCollector) completeImportedSingBoxSource(source *coreLogFileSource) {
	collector.replaceImportedSingBoxSourceLocked(source, true)
}

func (collector *CoreLogCollector) replaceImportedSingBoxSourceLocked(source *coreLogFileSource, completeTransition bool) {
	collector.mu.Lock()
	if completeTransition {
		delete(collector.transitions, core.EngineSingBox)
	}
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
	}
	collector.mu.Unlock()
	if source == nil {
		_ = waitCoreLogFileRun(active, 5*time.Second)
		return
	}
	if source != nil && running {
		collector.startFileSource(*source)
	}
}

func (collector *CoreLogCollector) importedSourceEpochLocked(source coreLogFileSource) (uint64, bool) {
	for _, existing := range collector.fileSources {
		if existing.engine == source.engine && existing.kind == source.kind && existing.path == source.path &&
			existing.configDigest == source.configDigest && existing.ownership == source.ownership {
			return existing.epoch, true
		}
	}
	collector.nextFileEpoch++
	return collector.nextFileEpoch, false
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
	delete(collector.transitions, core.EngineSingBox)
	collector.mu.Unlock()
	collector.replaceImportedSingBoxSource(nil)
	collector.selectSourceStatus(core.EngineSingBox, "file", "failed", code)
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
	validated, err := openValidatedCoreLogFileContext(context.Background(), source)
	if err != nil {
		return nil, err
	}
	return validated.file, nil
}

type validatedCoreLogFile struct {
	file           *os.File
	identity       os.FileInfo
	metadata       fileMetadata
	rootUID        int
	rootOwnerKnown bool
}

func openValidatedCoreLogFileContext(ctx context.Context, source coreLogFileSource) (*validatedCoreLogFile, error) {
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
	if expected.Mode()&os.ModeSymlink != 0 || !expected.Mode().IsRegular() || !coreLogFileHasSingleLink(expected) || expected.Mode().Perm()&0o022 != 0 ||
		(source.kind == "file" && rootOwnerKnown && ownerKnown && uid != rootUID) {
		return nil, errors.New("core log source is not a protected regular file")
	}
	file, err := openCoreLogFileNoSymlinks(source.root, source.path, rootUID, rootOwnerKnown)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(expected, opened) || !opened.Mode().IsRegular() || !coreLogFileHasSingleLink(opened) ||
		metadataFromFileInfo(opened) != metadataFromFileInfo(expected) {
		file.Close()
		return nil, errors.New("core log source changed while it was being opened")
	}
	return &validatedCoreLogFile{
		file: file, identity: opened, metadata: metadataFromFileInfo(opened),
		rootUID: rootUID, rootOwnerKnown: rootOwnerKnown,
	}, nil
}

func validateOpenedCoreLogFile(source coreLogFileSource, file *os.File, trusted *validatedCoreLogFile) (os.FileInfo, error) {
	if trusted == nil || file == nil || trusted.file != file || trusted.identity == nil {
		return nil, errors.New("managed core log file has no trusted opened identity")
	}
	current, err := file.Stat()
	if err != nil {
		return nil, err
	}
	uid, _, ownerKnown := fileOwnership(current)
	if !os.SameFile(trusted.identity, current) || !current.Mode().IsRegular() ||
		!coreLogFileHasSingleLink(current) || current.Mode().Perm()&0o022 != 0 ||
		metadataFromFileInfo(current) != trusted.metadata ||
		(source.kind == "file" && trusted.rootOwnerKnown && (!ownerKnown || uid != trusted.rootUID)) {
		return nil, errors.New("managed core log file identity or safety metadata drifted")
	}
	return current, nil
}

func coreLogFileHasSingleLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
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
	cursor := stringField(record["__CURSOR"])
	engine, ok := coreLogEngineForUnit(stringField(record["_SYSTEMD_UNIT"]), unitEngines)
	if !ok {
		return core.CoreLogEntry{}, cursor, false
	}
	messageValue, ok := journalMessageField(record["MESSAGE"])
	if !ok {
		return core.CoreLogEntry{}, cursor, false
	}
	message := sanitizeCoreLogMessage(messageValue)
	if message == "" {
		return core.CoreLogEntry{}, cursor, false
	}
	loggedAt := time.Now().UTC()
	if microseconds, err := strconv.ParseInt(stringField(record["__REALTIME_TIMESTAMP"]), 10, 64); err == nil && microseconds > 0 {
		loggedAt = time.UnixMicro(microseconds).UTC()
	}
	priority, err := strconv.Atoi(stringField(record["PRIORITY"]))
	if err != nil {
		priority = 6
	}
	return core.CoreLogEntry{Engine: engine, Level: coreLogLevelForPriority(priority), Message: message, LoggedAt: loggedAt}, cursor, true
}

func stringField(value any) string {
	result, _ := value.(string)
	return result
}

// journalMessageField accepts both normal JSON strings and the byte-array
// representation journald uses when MESSAGE contains control bytes (for
// example sing-box ANSI color escapes). Every element must be an integral byte;
// malformed or oversized arrays fail closed before allocation or enqueue.
func journalMessageField(value any) (string, bool) {
	if result, ok := value.(string); ok {
		return result, true
	}
	values, ok := value.([]any)
	if !ok || len(values) > coreLogFileMaxLine {
		return "", false
	}
	bytes := make([]byte, len(values))
	for index, value := range values {
		number, ok := value.(float64)
		if !ok || number < 0 || number > 255 || number != float64(byte(number)) {
			return "", false
		}
		bytes[index] = byte(number)
	}
	return string(bytes), true
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
