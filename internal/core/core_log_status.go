package core

// Core log source states reported by an Agent. Each state describes exactly
// one (node, engine, instance) source, so a healthy source is never hidden by
// a broken sibling.
//
//   - active:         the current source delivered at least one line.
//   - waiting:        the source is armed (or the service is not running yet)
//     and no line has arrived since it was armed.
//   - not-configured: the core is installed but has no managed configuration,
//     so there is nothing to collect yet.
//   - failed:         the Agent cannot read the source.
//   - pending:        the control plane has not received a status yet.
const (
	CoreLogStatusActive        = "active"
	CoreLogStatusWaiting       = "waiting"
	CoreLogStatusFailed        = "failed"
	CoreLogStatusPending       = "pending"
	CoreLogStatusNotConfigured = "not-configured"
)

// Core log transports. The reported source names the transport that produced
// the status, so a partially broken node can point at one source without
// invalidating the others.
const (
	CoreLogSourceJournal     = "journal"
	CoreLogSourceJournalHost = "journal-host"
	CoreLogSourceOpenRC      = "openrc"
	CoreLogSourceFallback    = "systemd-fallback"
	CoreLogSourceFile        = "file"
)

// Agent-reported reasons for a source that is not delivering lines yet. They
// are distinct because "nothing to collect" must never look like a failure.
const (
	CoreLogErrorConfigMissing    = "config-missing"
	CoreLogErrorServiceInactive  = "service-inactive"
	CoreLogErrorSourceMissing    = "source-missing"
	CoreLogErrorAwaitingFirstLog = "awaiting-first-line"
)

// ValidCoreLogStatus excludes the empty string; callers use it to validate a
// reported status before persisting it.
func ValidCoreLogStatus(status string) bool {
	switch status {
	case CoreLogStatusActive, CoreLogStatusWaiting, CoreLogStatusFailed, CoreLogStatusPending, CoreLogStatusNotConfigured:
		return true
	}
	return false
}
