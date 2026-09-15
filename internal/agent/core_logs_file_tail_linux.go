//go:build linux

package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"syscall"
	"time"
)

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
		rotateBytes, _ := collector.rotationPolicy()
		if info.Size() >= rotateBytes {
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
