//go:build linux

package agent

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// rotateFile copies the live file beside itself and truncates it, keeping the
// configured number of bounded snapshots.
// supervise-daemon keeps writing with O_APPEND,
// so truncation is seamless for the writer and bounds the volatile log
// footprint like the journald RuntimeMaxUse cap does on systemd. A failed
// snapshot only costs the archived copy; truncation still proceeds so a
// persistently failing copy can never let the live file grow unbounded.
func (collector *CoreLogCollector) rotateFile(source coreLogFileSource) {
	_, rotateCount := collector.rotationPolicy()
	if rotateCount == 0 {
		if err := os.Truncate(source.path, 0); err != nil {
			slog.Warn("managed core log rotation failed", "path", source.path, "error", err)
		}
		return
	}
	contents, err := os.ReadFile(source.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("managed core log rotation snapshot failed", "path", source.path, "error", err)
	}
	if len(contents) > 0 && rotateCount > 0 {
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
