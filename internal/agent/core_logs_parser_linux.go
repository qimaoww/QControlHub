//go:build linux

package agent

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// ansiControlPattern matches ANSI escape sequences (CSI) such as the color
// codes sing-box emits (`ESC[32m`, `ESC[0m`). They are display-only bytes and
// must not leak into the core-log panel as visible `[36m`-style text.
var ansiControlPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// sanitizeCoreLogMessage repairs encoding, drops NUL and ANSI control bytes,
// and bounds the resulting line before it is enqueued for display.
func sanitizeCoreLogMessage(raw string) string {
	message := strings.TrimSpace(strings.ToValidUTF8(raw, "�"))
	message = strings.ReplaceAll(message, "\x00", "�")
	message = ansiControlPattern.ReplaceAllString(message, "")
	// Remove any surviving ESC introducer so a lone control byte cannot render.
	message = strings.ReplaceAll(message, "\x1b", "")
	if message == "" {
		return ""
	}
	if len(message) > core.MaxCoreLogMessageBytes {
		message = message[:core.MaxCoreLogMessageBytes]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return message
}

func singBoxLogLevel(message string) string {
	upper := strings.ToUpper(message)
	for _, candidate := range []struct{ token, level string }{
		{" PANIC ", "critical"}, {" FATAL ", "critical"}, {" ERROR ", "error"},
		{" WARN ", "warning"}, {" DEBUG ", "debug"}, {" TRACE ", "debug"}, {" INFO ", "info"},
	} {
		if strings.Contains(upper, candidate.token) || strings.HasPrefix(upper, strings.TrimSpace(candidate.token)+" ") {
			return candidate.level
		}
	}
	return "info"
}

// Rust's tracing/log4rs console output does not carry a syslog priority.
// Match only the level prefix (optionally after its timestamp), never words
// inside a connection message, hostname, or error description.
var ssRustLogLevelPattern = regexp.MustCompile(`^(?:\d{4}-\d{2}-\d{2}[T ][0-9:.]+(?:Z|[+-][0-9:]+)?\s+)?(ERROR|WARN|INFO|DEBUG|TRACE)\s+`)

func ssRustLogLevel(message, fallback string) string {
	match := ssRustLogLevelPattern.FindStringSubmatch(message)
	if match == nil {
		return fallback
	}
	switch match[1] {
	case "ERROR":
		return "error"
	case "WARN":
		return "warning"
	case "DEBUG", "TRACE":
		return "debug"
	default:
		return "info"
	}
}
