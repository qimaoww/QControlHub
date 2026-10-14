//go:build linux

package agent

import (
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// Retain the end of stderr: installer/probe failures follow verbose progress.
type ipQualityDiagnosticTail struct {
	mu       sync.Mutex
	contents []byte
}

func (w *ipQualityDiagnosticTail) Write(content []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	length := len(content)
	const limit = 16 << 10
	if len(content) >= limit {
		w.contents = append(w.contents[:0], content[len(content)-limit:]...)
	} else {
		if overflow := len(w.contents) + len(content) - limit; overflow > 0 {
			w.contents = w.contents[overflow:]
		}
		w.contents = append(w.contents, content...)
	}
	return length, nil
}

func (w *ipQualityDiagnosticTail) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.contents)
}

var ipQualityANSIPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
var ipQualityURLPattern = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
var ipQualitySecretPattern = regexp.MustCompile(`(?i)(authorization|bearer|token|password|passwd|secret|api[_-]?key)([\s:=]+)(?:bearer\s+)?[^\s,;]+`)

func sanitizeIPQualityDiagnostic(value string) string {
	value = ipQualityANSIPattern.ReplaceAllString(strings.ToValidUTF8(value, "�"), "")
	value = ipQualityURLPattern.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "[redacted URL]"
		}
		return parsed.Scheme + "://" + parsed.Hostname() + "/[redacted]"
	})
	value = ipQualitySecretPattern.ReplaceAllString(value, "$1$2[redacted]")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	const limit = 3500
	if len(value) > limit {
		// Preserve a helper's failure classification at the start as well as
		// the final package-manager error after long progress/package lists.
		value = strings.ToValidUTF8(value[:900], "�") + "\n…\n" + strings.ToValidUTF8(value[len(value)-(limit-900):], "�")
	}
	return value
}
