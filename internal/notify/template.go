package notify

import (
	"fmt"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var secretText = regexp.MustCompile(`(?i)(?:bearer\s+\S+|(?:token|password|passwd|secret|authorization|api[_-]?key)\s*[:=]\s*[^\s,;]+|https?://[^\s<>]+|\b[0-9]{6,}:[A-Za-z0-9_-]{20,})`)

func Redact(value string) string {
	return Clip(secretText.ReplaceAllString(fmt.Sprint(core.RedactDiagnosticValue(value)), "[已脱敏]"), 2000)
}

// Clip counts bytes and never cuts a UTF-8 sequence.
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 3 {
		return ""
	}
	n -= 3
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
func Render(t Template, e Event, panel string) (string, string, error) {
	if err := ValidateTemplate(t); err != nil {
		return "", "", err
	}
	v := map[string]string{"panel": panel, "type": e.Type, "time": e.Time.Format(time.RFC3339), "agent_id": e.AgentID, "agent": e.Agent, "engine": e.Engine, "task_id": e.TaskID, "action": e.Action, "error": Redact(e.Error), "version": e.Version, "message": Redact(e.Message), "period": e.Period, "direction": e.Direction, "status": e.Status, "current": e.Current, "threshold": e.Threshold}
	if e.Port > 0 {
		v["port"] = fmt.Sprint(e.Port)
	}
	if e.LimitBytes > 0 {
		v["used_bytes"] = fmt.Sprint(e.UsedBytes)
		v["limit_bytes"] = fmt.Sprint(e.LimitBytes)
		v["percent"] = fmt.Sprintf("%.2f%%", float64(e.UsedBytes)/float64(e.LimitBytes)*100)
	}
	render := func(s string) string {
		return placeholder.ReplaceAllStringFunc(s, func(key string) string {
			value := v[strings.TrimSuffix(strings.TrimPrefix(key, "{{"), "}}")]
			if value == "" {
				return "—"
			}
			return value
		})
	}
	return Clip(render(t.Title), 400), Clip(render(t.Body), 12000), nil
}
func Sample(event string) Event {
	return Event{Type: event, Time: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), AgentID: "agt_example", TaskID: "tsk_example", Action: "validate", Agent: "示例节点", Engine: "xray", Port: 443, UsedBytes: 80 << 30, LimitBytes: 100 << 30, Current: "12 MB/s", Threshold: "10 MB/s", Period: "2026-01-01 / 2026-02-01", Direction: "上传", Version: "v1.2.3", Error: "示例错误（已脱敏）", Message: "脱敏示例：" + EventNames[event]}
}
