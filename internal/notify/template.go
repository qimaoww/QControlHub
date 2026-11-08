package notify

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
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
	if strings.HasPrefix(e.Type, "traffic.") || e.UsedBytes > 0 {
		v["used_bytes"] = fmt.Sprint(e.UsedBytes)
	}
	if e.LimitBytes > 0 {
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
	e := Event{Type: event, Time: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), Message: "脱敏示例：" + EventNames[event]}
	if event != "panel.update" {
		e.AgentID, e.Agent = "agt_example", "示例节点"
	}
	switch event {
	case TypeTaskFailed:
		e.TaskID, e.Action, e.Engine, e.Error = "tsk_example", "validate", "xray", "示例错误（已脱敏）"
	case TypeTrafficQuota, "traffic.warning":
		e.Engine, e.Port = "xray", 443
		e.UsedBytes, e.LimitBytes = 80<<30, 100<<30
		e.Current, e.Threshold, e.Period, e.Direction = "80.00%", "80.00%", "2026-01-01 / 2026-02-01", "上传 + 下载（计费口径）"
	case "speed.high", "speed.low", "speed.recovered":
		e.Current, e.Threshold, e.Direction = "12 MB/s", "10 MB/s", "上传（TX）"
		if event == "speed.low" {
			e.Current, e.Threshold = "500 B/s", "1000 B/s"
		}
		if event == "speed.recovered" {
			e.Current, e.Threshold = "5 MB/s", "8 MB/s"
		}
	case "panel.update":
		e.Version = "v1.2.3"
	}
	e.Status = event
	return e
}
