//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
			return verifyCompletedCoreMigrationOwnership(ctx, existing, manager)
		}
	}
	if err := verify(verifyContext, ownership.Existing, managed, e.serviceManager()); err != nil {
		return completedCoreMigration{}, fmt.Errorf("core migration service ownership drifted: %w", err)
	}
	return ownership, nil
}
