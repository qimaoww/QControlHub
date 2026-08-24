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
	mu          sync.Mutex
	queued      []core.CoreLogEntry
	pending     *core.CoreLogBatch
	dropped     uint64
	sources     []coreLogJournalSource
	fileSources []coreLogFileSource
	seenCursors map[string]struct{}
	cursorOrder []string
	runContext  context.Context
	runWait     sync.WaitGroup
	runStopped  bool
	activeFiles map[string]struct{}
	status      map[core.Engine]CoreLogSourceStatus
	statusKind  map[core.Engine]string
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
	collector := &CoreLogCollector{activeFiles: make(map[string]struct{}), status: make(map[core.Engine]CoreLogSourceStatus), statusKind: make(map[core.Engine]string)}
	if manager != nil && manager.Kind() == ServiceManagerOpenRC {
		collector.fileSources = coreLogFileSources(specs...)
		return collector
	}
	collector.sources = coreLogJournalSources(specs...)
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
	if _, exists := collector.status[engine]; exists && kind == "journal" && collector.statusKind[engine] == "file" {
		return
	}
	collector.status[engine] = CoreLogSourceStatus{Status: status, Error: code}
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
	for _, source := range collector.fileSources {
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
		collector.activeFiles = make(map[string]struct{})
	}
	key := string(source.engine) + "\x00" + source.path
	if _, exists := collector.activeFiles[key]; exists {
		collector.mu.Unlock()
		return
	}
	collector.activeFiles[key] = struct{}{}
	ctx := collector.runContext
	if ctx == nil {
		collector.mu.Unlock()
		return
	}
	collector.runWait.Add(1)
	collector.mu.Unlock()
	go func() {
		defer collector.runWait.Done()
		collector.runFileSource(ctx, source)
	}()
}

// runFileSource keeps one tail reader alive per managed OpenRC log file with
// the same retry cadence as the journal readers.
func (collector *CoreLogCollector) runFileSource(ctx context.Context, source coreLogFileSource) {
	for ctx.Err() == nil {
		err := collector.followFile(ctx, source)
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
		opened, err := openValidatedCoreLogFile(source)
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
	if missingBeforeOpen {
		initialWhence = io.SeekStart
	}
	if _, err := file.Seek(0, initialWhence); err != nil {
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
		if err := validateCoreLogSourceBinding(source); err != nil {
			return err
		}
		info, statErr := file.Stat()
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("managed core log file %s was replaced by a non-regular file", source.path)
		}
		probe, pathErr := openValidatedCoreLogFile(source)
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
	if executor == nil || executor.MigrationMarkerPrefix == "" {
		return nil
	}
	spec, enabled := executor.Specs[core.EngineSingBox]
	if !enabled {
		return nil
	}
	record, err := readCoreMigrationRecord(executor.MigrationMarkerPrefix, core.EngineSingBox)
	if err != nil {
		collector.setSourceStatus(core.EngineSingBox, "file", "failed", "migration-state-invalid")
		return err
	}
	if record.State != coreMigrationComplete {
		return nil
	}
	info, err := os.Lstat(spec.ConfigPath)
	if err != nil {
		collector.setSourceStatus(core.EngineSingBox, "file", "failed", "config-unavailable")
		return err
	}
	file, _, err := openProtectedCoreMigrationFile(spec.ConfigPath, info, core.MaxConfigBytes)
	if err != nil {
		collector.setSourceStatus(core.EngineSingBox, "file", "failed", "config-unsafe")
		return err
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, core.MaxConfigBytes+1))
	file.Close()
	if readErr != nil || len(contents) > core.MaxConfigBytes || coreMigrationConfigDigest(string(contents)) != record.ConfigDigest {
		collector.setSourceStatus(core.EngineSingBox, "file", "failed", "config-drift")
		return errors.New("imported sing-box configuration no longer matches its migration record")
	}
	output, disabled, err := singBoxLogOutput(string(contents))
	if err != nil {
		collector.setSourceStatus(core.EngineSingBox, "file", "failed", "config-invalid")
		return err
	}
	if disabled || output == "" {
		return nil
	}
	path, err := importedSingBoxLogPath(output)
	if err != nil {
		collector.setSourceStatus(core.EngineSingBox, "file", "failed", "source-outside-boundary")
		return err
	}
	source := coreLogFileSource{
		path: path, root: importedSingBoxLogRoot, engine: core.EngineSingBox, kind: "file",
		configPath: spec.ConfigPath, configDigest: record.ConfigDigest, markerPrefix: executor.MigrationMarkerPrefix,
	}
	collector.mu.Lock()
	known := false
	for _, existing := range collector.fileSources {
		if existing.engine == source.engine && existing.path == source.path {
			known = true
			break
		}
	}
	if !known {
		collector.fileSources = append(collector.fileSources, source)
	}
	running := collector.runContext != nil
	collector.mu.Unlock()
	collector.setSourceStatus(core.EngineSingBox, "file", "waiting", "source-missing")
	if running {
		collector.startFileSource(source)
	}
	return nil
}

func singBoxLogOutput(content string) (string, bool, error) {
	decoded, err := decodeExtendedJSON(content)
	if err != nil {
		return "", false, err
	}
	root, _ := decoded.(map[string]any)
	logging, _ := root["log"].(map[string]any)
	if logging == nil {
		return "", false, nil
	}
	disabled, _ := logging["disabled"].(bool)
	output, _ := logging["output"].(string)
	return output, disabled, nil
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
	if source.root == "" || !filepath.IsAbs(source.path) || !pathWithin(source.path, source.root) {
		return nil, errors.New("core log source is outside its protected root")
	}
	if err := validateCoreLogSourceBinding(source); err != nil {
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

func validateCoreLogSourceBinding(source coreLogFileSource) error {
	if source.kind != "file" {
		return nil
	}
	record, err := readCoreMigrationRecord(source.markerPrefix, source.engine)
	if err != nil || record.State != coreMigrationComplete || record.ConfigDigest != source.configDigest {
		return errors.New("core log source is not bound to a completed migration")
	}
	digest, exists, err := protectedCoreMigrationFileDigest(source.configPath, core.MaxConfigBytes)
	if err != nil || !exists || digest != source.configDigest {
		return errors.New("core log source configuration drifted")
	}
	return nil
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
