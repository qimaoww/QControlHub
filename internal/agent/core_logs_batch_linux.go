//go:build linux

package agent

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
	level := coreLogLevelForPriority(priority)
	if engine == core.EngineShadowsocksRust {
		level = ssRustLogLevel(message, level)
	}
	return core.CoreLogEntry{Engine: engine, Level: level, Message: message, LoggedAt: loggedAt}, cursor, true
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
