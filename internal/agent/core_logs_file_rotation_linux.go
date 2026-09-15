//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// rotateFile copies a bounded tail of the live file beside itself and truncates
// it, keeping the configured number of bounded snapshots.
// supervise-daemon keeps writing with O_APPEND,
// so truncation is seamless for the writer and bounds the volatile log
// footprint like the journald RuntimeMaxUse cap does on systemd. A failed
// snapshot only costs the archived copy; truncation still proceeds so a
// persistently failing copy can never let the live file grow unbounded.
func (collector *CoreLogCollector) rotateFile(source coreLogFileSource) {
	if source.kind == "systemd-fallback" {
		if err := truncateSystemdFallbackCoreLog(context.Background(), source); err != nil {
			slog.Warn("managed core log rotation failed", "path", source.path, "error", err)
		}
		return
	}
	rotateBytes, rotateCount := collector.rotationPolicy()
	if rotateCount == 0 {
		if err := os.Truncate(source.path, 0); err != nil {
			slog.Warn("managed core log rotation failed", "path", source.path, "error", err)
		}
		return
	}
	file, err := os.Open(source.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("managed core log rotation snapshot failed", "path", source.path, "error", err)
	}
	var contents []byte
	if err == nil {
		if info, statErr := file.Stat(); statErr != nil {
			slog.Warn("managed core log rotation snapshot failed", "path", source.path, "error", statErr)
		} else {
			start := info.Size() - rotateBytes
			if start < 0 {
				start = 0
			}
			if _, seekErr := file.Seek(start, io.SeekStart); seekErr != nil {
				slog.Warn("managed core log rotation snapshot failed", "path", source.path, "error", seekErr)
			} else if tail, readErr := io.ReadAll(io.LimitReader(file, rotateBytes)); readErr != nil {
				slog.Warn("managed core log rotation snapshot failed", "path", source.path, "error", readErr)
			} else {
				contents = tail
			}
		}
		_ = file.Close()
	}
	if len(contents) > 0 {
		for index := 1; index <= rotateCount; index++ {
			path := coreLogArchivePath(source.path, index)
			if info, statErr := os.Lstat(path); statErr == nil && info.Size() > rotateBytes {
				_ = os.Remove(path)
			}
		}
		for index := rotateCount; index >= 2; index-- {
			_ = os.Rename(coreLogArchivePath(source.path, index-1), coreLogArchivePath(source.path, index))
		}
		if err := os.WriteFile(coreLogArchivePath(source.path, 1), contents, 0o600); err != nil {
			slog.Warn("managed core log rotation snapshot failed", "path", source.path, "error", err)
		}
	}
	if err := os.Truncate(source.path, 0); err != nil {
		slog.Warn("managed core log rotation failed", "path", source.path, "error", err)
	}
}

func coreLogArchivePath(path string, index int) string {
	if index == 1 {
		return path + ".old"
	}
	return fmt.Sprintf("%s.old.%d", path, index)
}

func (collector *CoreLogCollector) removeExcessArchives(source coreLogFileSource) {
	_, count := collector.rotationPolicy()
	for index := count + 1; index <= 5; index++ {
		_ = os.Remove(coreLogArchivePath(source.path, index))
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
	} else if source.engine == core.EngineShadowsocksRust {
		level = ssRustLogLevel(message, level)
	}
	collector.append(core.CoreLogEntry{Engine: source.engine, Level: level, Message: message, LoggedAt: time.Now().UTC()})
}

func truncateSystemdFallbackCoreLog(ctx context.Context, source coreLogFileSource) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if source.kind != "systemd-fallback" || source.root != systemdFallbackCoreLogRoot {
		return errors.New("core log cache is not a managed systemd fallback source")
	}
	trusted := false
	for _, spec := range DefaultSpecs() {
		path, err := systemdFallbackCoreLogPath(spec.Service)
		if err == nil && source.path == path {
			trusted = true
			break
		}
	}
	if !trusted {
		return errors.New("core log cache path is not project-managed")
	}
	validated, err := openValidatedCoreLogFileContext(ctx, source)
	if err != nil {
		return err
	}
	identity := validated.identity
	_ = validated.file.Close()
	if err := os.Truncate(source.path, 0); err == nil {
		return nil
	}
	// Binary-only upgrades may still run under an older qagent unit without the
	// RuntimeDirectory write exception. Use a short-lived root unit exposing
	// only this already-validated cache file, and retain no rotated copy.
	for _, executable := range []string{retiredLogSystemdRunPath, retiredLogTruncatePath} {
		if err := validatePrivilegedExecutable(executable); err != nil {
			return fmt.Errorf("unsafe core-log cache helper %s: %w", executable, err)
		}
	}
	arguments := []string{
		"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec",
		"--property=User=root", "--property=UMask=0077", "--property=NoNewPrivileges=yes",
		"--property=CapabilityBoundingSet=", "--property=AmbientCapabilities=",
		"--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=PrivateTmp=yes",
		"--property=PrivateDevices=yes", "--property=RestrictAddressFamilies=AF_UNIX",
		"--property=ReadWritePaths=" + source.path, "--", retiredLogTruncatePath, "--size=0", "--", source.path,
	}
	if output, err := run(ctx, retiredLogSystemdRunPath, arguments...); err != nil {
		return fmt.Errorf("truncate systemd fallback core log: %w: %s", err, strings.TrimSpace(output))
	}
	after, err := os.Lstat(source.path)
	if err != nil || !os.SameFile(identity, after) {
		if err == nil {
			err = errors.New("systemd fallback core log changed during cleanup")
		}
		return err
	}
	return nil
}
