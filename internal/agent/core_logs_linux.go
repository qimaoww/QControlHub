//go:build linux

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	coreLogQueueLimit = 2048
	journalctlPath    = "/usr/bin/journalctl"
)

type CoreLogCollector struct {
	mu      sync.Mutex
	queued  []core.CoreLogEntry
	pending *core.CoreLogBatch
	dropped uint64
}

func NewCoreLogCollector() *CoreLogCollector { return &CoreLogCollector{} }

func (collector *CoreLogCollector) Run(ctx context.Context) {
	if err := validatePrivilegedExecutable(journalctlPath); err != nil {
		slog.Warn("managed core log streaming is unavailable", "error", err)
		return
	}
	for ctx.Err() == nil {
		err := collector.follow(ctx)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("managed core journal reader stopped", "error", err)
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (collector *CoreLogCollector) follow(ctx context.Context) error {
	command := exec.CommandContext(ctx, journalctlPath,
		"--namespace=qagent-cores", "--follow", "--lines=0", "--output=json",
		"--output-fields=MESSAGE,_SYSTEMD_UNIT,PRIORITY,__REALTIME_TIMESTAMP")
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
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 16<<10), 256<<10)
	for scanner.Scan() {
		entry, ok := decodeJournalCoreLog(scanner.Bytes())
		if ok {
			collector.append(entry)
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

func (collector *CoreLogCollector) append(entry core.CoreLogEntry) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
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

func decodeJournalCoreLog(value []byte) (core.CoreLogEntry, bool) {
	var record map[string]any
	if json.Unmarshal(value, &record) != nil {
		return core.CoreLogEntry{}, false
	}
	engine, ok := coreLogEngineForUnit(stringField(record["_SYSTEMD_UNIT"]))
	if !ok {
		return core.CoreLogEntry{}, false
	}
	message := strings.TrimSpace(strings.ToValidUTF8(stringField(record["MESSAGE"]), "�"))
	message = strings.ReplaceAll(message, "\x00", "�")
	if message == "" {
		return core.CoreLogEntry{}, false
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
	return core.CoreLogEntry{Engine: engine, Level: coreLogLevelForPriority(priority), Message: message, LoggedAt: loggedAt}, true
}

func stringField(value any) string {
	result, _ := value.(string)
	return result
}

func coreLogEngineForUnit(unit string) (core.Engine, bool) {
	switch unit {
	case "qagent-mihomo.service":
		return core.EngineMihomo, true
	case "qagent-xray.service":
		return core.EngineXray, true
	case "qagent-sing-box.service":
		return core.EngineSingBox, true
	case "qagent-shadowsocks-rust.service":
		return core.EngineShadowsocksRust, true
	default:
		return "", false
	}
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
