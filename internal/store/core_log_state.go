package store

import (
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// validCoreLogSource reports whether an Agent-reported transport is one this
// build understands. The empty string stays valid so a legacy Agent that only
// reports a status does not lose every other source observation.
func validCoreLogSource(source string) bool {
	switch source {
	case "",
		core.CoreLogSourceJournal,
		core.CoreLogSourceJournalHost,
		core.CoreLogSourceOpenRC,
		core.CoreLogSourceFallback,
		core.CoreLogSourceFile:
		return true
	}
	return false
}

// normalizeCoreLogLastAt keeps a clock reading that cannot describe a line the
// Agent already delivered out of the directory. A future timestamp is clamped
// to the receive time, matching how StoreCoreLogs treats a future log
// timestamp, and a zero time means the source has no evidence of delivery.
func normalizeCoreLogLastAt(lastAt *time.Time, receivedAt time.Time) *time.Time {
	if lastAt == nil {
		return nil
	}
	value := lastAt.UTC()
	if value.IsZero() {
		return nil
	}
	if value.After(receivedAt.Add(5 * time.Minute)) {
		value = receivedAt
	}
	return &value
}

// coreLogErrorForStatus keeps the persisted reason consistent with the
// reported state: a source that is not failed must not retain stale failure
// text from an earlier heartbeat.
func coreLogErrorForStatus(status, reason string) string {
	if status == core.CoreLogStatusFailed {
		return reason
	}
	return ""
}

// normalizeCoreLogSourceStatus maps an unknown or empty stored state onto
// pending, while preserving the directory's synthetic states. Every state the
// Agent contract defines, including not-configured, passes through unchanged.
func normalizeCoreLogSourceStatus(status string) string {
	if core.ValidCoreLogStatus(status) {
		return status
	}
	switch status {
	case "offline", "unsupported", "status-unavailable", "not-installed":
		return status
	}
	return core.CoreLogStatusPending
}

// normalizeCoreLogSource drops a transport this control plane cannot attribute
// instead of presenting an unknown value as a source.
func normalizeCoreLogSource(source string) string {
	if validCoreLogSource(source) {
		return source
	}
	return ""
}

// parseCoreLogLastAtText parses the last-line timestamp stored inside the main
// instance runtime snapshot. A malformed value is ignored rather than failing
// the whole directory read.
func parseCoreLogLastAtText(raw *string) *time.Time {
	if raw == nil {
		return nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return normalizeCoreLogLastAt(&parsed, time.Now().UTC())
}
