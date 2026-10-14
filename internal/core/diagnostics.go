package core

import "regexp"

var diagnosticPrivatePEM = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
var diagnosticKeyAssignment = regexp.MustCompile(`(?i)((?:["']?(?:private[-_]?key|mldsa65[-_]?seed)["']?)\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;\}\]]+)`)

// Older Agents appended a raw Reality X25519 key to the Mihomo state path.
// This is a display-only compatibility guard, never a file-field classifier.
var diagnosticLegacyRealityPath = regexp.MustCompile(`(/var/lib/qcontrolhub-mihomo(?:-shr_[a-f0-9]+)?/)[A-Za-z0-9_-]{43}(["'\s]|$)`)

// RedactDiagnostic handles labelled keys and inline private PEM even when the
// configuration is unavailable (journal output or diagnostics from old Agents).
func RedactDiagnostic(value string) string {
	value = diagnosticPrivatePEM.ReplaceAllString(value, "[redacted private PEM]")
	value = diagnosticKeyAssignment.ReplaceAllString(value, "${1}[redacted]")
	return diagnosticLegacyRealityPath.ReplaceAllString(value, "${1}[redacted]${2}")
}
