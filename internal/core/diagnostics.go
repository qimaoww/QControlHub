package core

import "regexp"

var diagnosticPrivatePEM = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
var diagnosticKeyAssignment = regexp.MustCompile(`(?i)((?:["']?(?:private[-_]?key|mldsa65[-_]?seed)["']?)\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;\}\]]+)`)

// Older Agents appended a raw Reality X25519 key to the Mihomo state path.
// This is a display-only compatibility guard, never a file-field classifier.
var diagnosticLegacyRealityPath = regexp.MustCompile(`(read certificate/key /var/lib/qcontrolhub-mihomo(?:-shr_[a-f0-9]+)?/)[A-Za-z0-9_-]{43}(["'\s]|$)`)

// RedactDiagnostic handles labelled keys and inline private PEM even when the
// configuration is unavailable (journal output or diagnostics from old Agents).
func RedactDiagnostic(value string) string {
	value = replaceDiagnostic(value, diagnosticPrivatePEM, "[redacted private PEM]")
	value = replaceDiagnostic(value, diagnosticAssignments, "${1}[redacted]")
	value = replaceDiagnostic(value, diagnosticBearer, "Bearer [redacted]")
	value = replaceDiagnostic(value, diagnosticAPIKey, "[redacted]")
	value = replaceDiagnostic(value, diagnosticUUID, "[redacted UUID]")
	value = replaceDiagnostic(value, diagnosticURLCredentials, "${1}[redacted]@")
	value = replaceDiagnostic(value, diagnosticURLSecret, "${1}[redacted]")
	value = replaceDiagnostic(value, diagnosticKeyAssignment, "${1}[redacted]")
	return replaceDiagnostic(value, diagnosticLegacyRealityPath, "${1}[redacted]${2}")
}

// ReplaceAllString allocates two message-sized buffers even without a match.
// Most log lines contain no credentials. Still run every original expression,
// including its Unicode case folding, and allocate only for actual redaction.
func replaceDiagnostic(value string, pattern *regexp.Regexp, replacement string) string {
	if !pattern.MatchString(value) {
		return value
	}
	return pattern.ReplaceAllString(value, replacement)
}
