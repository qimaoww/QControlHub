package notify

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
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
	r, err := RenderFormatted(t, e, panel, "UTC")
	return r.Title, r.Body, err
}

func RenderFormatted(t Template, e Event, panel, zone string) (Rendered, error) {
	if err := ValidateTemplate(t); err != nil {
		return Rendered{}, err
	}
	v := map[string]string{"panel": panel, "type": e.Type, "time": e.Time.Format(time.RFC3339), "agent_id": e.AgentID, "agent": e.Agent, "engine": e.Engine, "task_id": e.TaskID, "action": e.Action, "error": Redact(e.Error), "version": e.Version, "message": Redact(e.Message), "period": e.Period, "direction": e.Direction, "status": e.Status, "current": e.Current, "threshold": e.Threshold}
	location, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "browser" {
		location = time.UTC
	}
	v["time_display"] = e.Time.In(location).Format("2006-01-02 15:04:05 -07:00")
	v["current_display"], v["threshold_display"] = humanRate(e.Current), humanRate(e.Threshold)
	if e.Port > 0 {
		v["port"] = fmt.Sprint(e.Port)
	}
	if strings.HasPrefix(e.Type, "traffic.") || e.UsedBytes > 0 {
		v["used_bytes"] = fmt.Sprint(e.UsedBytes)
		v["used"] = humanBytes(e.UsedBytes)
	}
	if e.LimitBytes > 0 {
		v["limit_bytes"] = fmt.Sprint(e.LimitBytes)
		v["limit"] = humanBytes(e.LimitBytes)
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
	title, body := render(t.Title), render(t.Body)
	var entities []MessageEntity
	if t.Format == "markdown" {
		title, _ = markdownText(t.Title, render)
		body, entities = markdownText(t.Body, render)
	}
	if e.Test {
		title = "【测试】" + title
	}
	title, body = Clip(title, 400), Clip(body, 12000)
	entities = clippedEntities(entities, body)
	for i := range entities {
		entities[i].Offset += utf16Length(title + "\n\n")
	}
	if t.Format == "markdown" {
		entities = append([]MessageEntity{{Type: "bold", Length: utf16Length(title)}}, entities...)
	}
	return Rendered{Title: title, Body: body, Entities: entities}, nil
}

func humanBytes(value uint64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	n := float64(value)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"} {
		n /= 1024
		if n < 1024 || unit == "EiB" {
			return fmt.Sprintf("%.2f %s", n, unit)
		}
	}
	return ""
}

func humanRate(raw string) string {
	if !strings.HasSuffix(raw, " B/s") {
		return raw
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(raw, " B/s"), 64)
	if err != nil || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return raw
	}
	for _, unit := range []string{"B/s", "KB/s", "MB/s", "GB/s", "TB/s", "PB/s"} {
		if n < 1000 || unit == "PB/s" {
			if unit == "B/s" {
				return fmt.Sprintf("%.0f %s", n, unit)
			}
			return fmt.Sprintf("%.2f %s", n, unit)
		}
		n /= 1000
	}
	return raw
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
		if event == TypeTrafficQuota {
			e.UsedBytes, e.Current, e.Threshold = 110<<30, "110.00%", "100.00%"
		}
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
