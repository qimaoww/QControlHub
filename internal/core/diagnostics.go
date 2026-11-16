package core

import (
	"regexp"
	"strings"
)

// DiagnosticText is safe for PostgreSQL text and inert text rendering. It
// keeps line breaks, tabs and causal context, including malformed Agent output.
func DiagnosticText(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' || r == 127 {
			return '�'
		}
		return r
	}, value)
	return RedactDiagnostic(value)
}

var diagnosticPrivatePEM = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
var diagnosticHeaders = regexp.MustCompile(`(?im)((?:["']?(?:authorization|proxy-authorization|cookie|set-cookie)["']?)\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\r\n]+)`)
var diagnosticKeyAssignment = regexp.MustCompile(`(?i)((?:["']?(?:private[-_]?key|mldsa65[-_]?seed)["']?)\s*[:=]\s*)(?:\[redacted(?: private PEM)?\]|"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;\}\]]+)`)

// Older Agents appended a raw Reality X25519 key to the Mihomo state path.
// This is a display-only compatibility guard, never a file-field classifier.
var diagnosticLegacyRealityPath = regexp.MustCompile(`(read certificate/key /var/lib/qcontrolhub-mihomo(?:-shr_[a-f0-9]+)?/)[A-Za-z0-9_-]{43}(["'\s]|$)`)

// RedactDiagnostic handles labelled keys and inline private PEM even when the
// configuration is unavailable (journal output or diagnostics from old Agents).
func RedactDiagnostic(value string) string {
	if !mayContainDiagnosticCredential(value) {
		return value
	}
	value = redactDiagnosticContainers(value)
	value = replaceDiagnostic(value, diagnosticPrivatePEM, "[redacted private PEM]")
	value = replaceDiagnostic(value, diagnosticBearer, "Bearer [redacted]")
	value = replaceDiagnostic(value, diagnosticHeaders, "${1}[redacted]")
	value = replaceDiagnostic(value, diagnosticAssignments, "${1}[redacted]")
	value = replaceDiagnostic(value, diagnosticAPIKey, "[redacted]")
	value = replaceDiagnostic(value, diagnosticUUID, "[redacted UUID]")
	value = replaceDiagnostic(value, diagnosticURLCredentials, "${1}[redacted]@")
	value = replaceDiagnostic(value, diagnosticURLSecret, "${1}[redacted]")
	value = replaceDiagnostic(value, diagnosticKeyAssignment, "${1}[redacted]")
	return replaceDiagnostic(value, diagnosticLegacyRealityPath, "${1}[redacted]${2}")
}

// Every credential expression contains one of these ASCII markers, apart
// from bare UUIDs and URL userinfo. Fold the two additional Unicode equivalents
// of ASCII letters as well, so the shortcut cannot bypass (?i) redaction.
func mayContainDiagnosticCredential(value string) bool {
	folded := strings.ToLower(value)
	folded = strings.ReplaceAll(folded, "ſ", "s")
	for _, marker := range []string{"pass", "key", "psk", "secret", "token", "credential", "authorization", "cookie", "uuid", "decryption", "mldsa65", "short", "bearer", "webhook"} {
		if strings.Contains(folded, marker) {
			return true
		}
	}
	if strings.Contains(value, "://") && strings.Contains(value, "@") {
		return true
	}
	hexRun := 0
	for i := 0; i < len(folded); i++ {
		c := folded[i]
		if c == '-' && hexRun >= 8 {
			return true // A possible UUID prefix; the full expression decides.
		}
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			hexRun++
		} else {
			hexRun = 0
		}
	}
	return false
}

// ReplaceAllString allocates two message-sized buffers even without a match.
// Candidate lines still run every original expression, including Unicode case
// folding, and allocate replacement buffers only for actual redaction.
func replaceDiagnostic(value string, pattern *regexp.Regexp, replacement string) string {
	if !pattern.MatchString(value) {
		return value
	}
	return pattern.ReplaceAllString(value, replacement)
}
