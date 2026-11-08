package notify

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Configuration is one user's current settings, never a settings archive.
type Configuration struct {
	Revision  int64               `json:"revision"`
	Channels  []Channel           `json:"channels"`
	Rules     []Rule              `json:"rules"`
	Templates map[string]Template `json:"templates"`
}
type Channel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	// Secrets are omitted in API reads; absent keys retain saved values and empty keys clear them.
	Values     map[string]string `json:"values"`
	Secrets    map[string]string `json:"secrets,omitempty"`
	Configured []string          `json:"configured,omitempty"`
}
type Rule struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Event     string   `json:"event"`
	Enabled   bool     `json:"enabled"`
	AgentID   string   `json:"agent_id,omitempty"`
	PolicyID  string   `json:"policy_id,omitempty"`
	Channels  []string `json:"channels"`
	Threshold float64  `json:"threshold"`
	Recovery  float64  `json:"recovery"`
	Duration  int      `json:"duration_seconds"`
	Cooldown  int      `json:"cooldown_seconds"`
	Direction string   `json:"direction,omitempty"`
	Interval  int      `json:"interval_seconds"`
}
type Template struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	Format string `json:"format,omitempty"`
}

var EventNames = map[string]string{
	TypeTaskFailed: "任务失败", TypeAgentOffline: "节点离线", TypeAgentOnline: "节点恢复", TypeTrafficQuota: "流量超额",
	"traffic.warning": "流量预警", "speed.high": "持续高速", "speed.low": "持续低速", "speed.recovered": "速度恢复", "panel.update": "面板更新",
}
var Variables = []string{"panel", "type", "time", "time_display", "agent_id", "agent", "engine", "task_id", "action", "port", "used_bytes", "limit_bytes", "used", "limit", "percent", "period", "direction", "current", "threshold", "status", "error", "version", "message"}
var VariableDescriptions = map[string]string{
	"panel": "面板名称", "type": "事件标识", "time": "事件时间（RFC3339）", "agent_id": "节点 ID", "agent": "节点名称", "engine": "内核", "task_id": "任务 ID", "action": "任务操作", "port": "端口", "used_bytes": "已用字节数", "limit_bytes": "限额字节数", "percent": "配额百分比", "period": "统计周期", "direction": "收发方向", "current": "当前值（含单位）", "threshold": "阈值（含单位）", "status": "状态", "error": "脱敏错误", "version": "完整更新版本", "message": "事件摘要",
	"used": "易读用量（KiB、MiB、GiB，1024 进制）", "limit": "易读限额（1024 进制）", "time_display": "面板时区时间；浏览器自动模式使用 UTC",
}
var placeholder = regexp.MustCompile(`\{\{([a-z_]+)\}\}`)
var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var ChannelFields = map[string][]string{
	"webhook": {"url", "authorization", "secret"}, "telegram": {"token", "chat_id", "message_thread_id", "direct_messages_topic_id"},
	"smtp": {"host", "port", "tls", "username", "password", "from", "to"}, "bark": {"url", "device_key"}, "ntfy": {"url", "topic", "token"}, "gotify": {"url", "token"},
}

func SecretField(key string) bool {
	return key == "url" || key == "token" || key == "password" || key == "secret" || key == "authorization" || key == "device_key"
}
func DefaultTemplate(event string) Template {
	body := "{{message}}"
	switch event {
	case TypeTaskFailed:
		body = "**节点**　{{agent}}\n**内核**　{{engine}} · {{action}}\n**任务**　`{{task_id}}`\n\n**错误**\n{{error}}"
	case TypeAgentOffline, TypeAgentOnline:
		body = "**节点**　{{agent}}"
	case TypeTrafficQuota, "traffic.warning":
		body = "**节点**　{{agent}}\n**端口**　{{engine}} / {{port}}\n\n**用量**　{{used}} / {{limit}}\n**比例**　{{percent}}\n**周期**　{{period}}\n**方向**　{{direction}}"
	case "speed.high", "speed.low", "speed.recovered":
		body = "**节点**　{{agent}}\n**方向**　{{direction}}\n\n**当前速率**　{{current}}\n**阈值**　{{threshold}}"
	case "panel.update":
		body = "**可用版本**　`{{version}}`"
	}
	return Template{Title: "{{panel}} · " + EventNames[event], Body: body + "\n\n{{time_display}}", Format: "markdown"}
}
func ValidateTemplate(t Template) error {
	if t.Format != "" && t.Format != "text" && t.Format != "markdown" {
		return errors.New("模板格式无效")
	}
	for _, v := range []string{t.Title, t.Body} {
		if !utf8.ValidString(v) || len(v) > 12000 {
			return errors.New("模板必须是有效 UTF-8，且不超过 12000 字节")
		}
		rest := placeholder.ReplaceAllStringFunc(v, func(s string) string {
			key := s[2 : len(s)-2]
			for _, a := range Variables {
				if a == key {
					return ""
				}
			}
			return s
		})
		if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
			return errors.New("模板含未知变量或语法错误，请使用 {{变量名}}")
		}
	}
	if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Body) == "" || len(t.Title) > 400 {
		return errors.New("标题和正文不能为空，标题不超过 400 字节")
	}
	return nil
}
func (c Configuration) Validate() error {
	if len(c.Channels) > 20 || len(c.Rules) > 50 || len(c.Templates) > len(EventNames) {
		return errors.New("最多 20 个渠道、50 条规则")
	}
	seen := map[string]bool{}
	for _, ch := range c.Channels {
		if !identifier.MatchString(ch.ID) || seen[ch.ID] || strings.TrimSpace(ch.Name) == "" || !utf8.ValidString(ch.Name) || utf8.RuneCountInString(ch.Name) > 100 {
			return errors.New("渠道标识或名称无效")
		}
		seen[ch.ID] = true
		fields, ok := ChannelFields[ch.Type]
		if !ok {
			return errors.New("不支持的通知渠道")
		}
		for k, v := range ch.Values {
			if SecretField(k) || !contains(fields, k) || len(v) > 500 || strings.ContainsAny(v, "\r\n") {
				return errors.New("渠道字段无效")
			}
		}
		for k, v := range ch.Secrets {
			if !SecretField(k) || !contains(fields, k) || len(v) > 2000 || strings.ContainsAny(v, "\r\n") || v == "[redacted]" || v == "********" || v == "••••••••" {
				return errors.New("渠道凭据无效")
			}
		}
		required := map[string][]string{"webhook": {"url"}, "telegram": {"token", "chat_id"}, "smtp": {"host", "port", "tls", "from", "to"}, "bark": {"url", "device_key"}, "ntfy": {"url", "topic"}, "gotify": {"url", "token"}}
		for _, k := range required[ch.Type] {
			if ch.Enabled && ch.Get(k) == "" {
				return fmt.Errorf("%s 缺少 %s", ch.Name, k)
			}
		}
		if raw := ch.Get("url"); raw != "" {
			u, err := url.Parse(raw)
			if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
				return errors.New("通知地址须为无用户信息的 HTTP(S) URL")
			}
		}
		if ch.Type == "telegram" {
			for _, k := range []string{"message_thread_id", "direct_messages_topic_id"} {
				if v := ch.Get(k); v != "" && !regexp.MustCompile(`^[1-9][0-9]{0,15}$`).MatchString(v) {
					return errors.New("会话主题 ID 无效")
				}
			}
		}
		if ch.Type == "ntfy" && (ch.Enabled || ch.Get("topic") != "") && !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(ch.Get("topic")) {
			return errors.New("ntfy 主题无效")
		}
		if ch.Type == "smtp" && ch.Enabled {
			port, err := strconv.Atoi(ch.Get("port"))
			if err != nil || port < 1 || port > 65535 {
				return errors.New("SMTP 端口须为 1–65535")
			}
			for _, k := range []string{"from", "to"} {
				a, e := mail.ParseAddress(ch.Get(k))
				if e != nil || a.Address != ch.Get(k) {
					return errors.New("SMTP 收发地址无效")
				}
			}
			if ch.Get("tls") != "starttls" && ch.Get("tls") != "tls" {
				return errors.New("SMTP 必须使用 TLS 或 STARTTLS")
			}
		}
	}
	ids := map[string]bool{}
	for _, r := range c.Rules {
		if !identifier.MatchString(r.ID) || ids[r.ID] || !utf8.ValidString(r.Name) || utf8.RuneCountInString(r.Name) > 100 || strings.TrimSpace(r.Name) == "" || EventNames[r.Event] == "" || r.Event == "speed.recovered" {
			return errors.New("规则标识、名称或事件无效")
		}
		ids[r.ID] = true
		if (r.Enabled && len(r.Channels) == 0) || len(r.Channels) > 20 {
			return errors.New("规则须选择接收渠道")
		}
		used := map[string]bool{}
		for _, id := range r.Channels {
			if !seen[id] || used[id] {
				return errors.New("规则引用了无效或重复渠道")
			}
			used[id] = true
		}
		if r.Duration < 10 || r.Duration > 86400 || r.Cooldown < 60 || r.Cooldown > 604800 {
			return errors.New("持续时间须为 10–86400 秒，冷却须为 60–604800 秒")
		}
		if strings.HasPrefix(r.Event, "speed.") {
			if r.AgentID == "" || r.PolicyID != "" || r.Threshold <= 0 || r.Threshold > 1e15 || r.Recovery < 0 || r.Recovery > 1e15 || !contains([]string{"upload", "download", "total"}, r.Direction) {
				return errors.New("速度规则须明确选择节点、方向及 B/s 阈值")
			}
			if (r.Event == "speed.high" && r.Recovery >= r.Threshold) || (r.Event == "speed.low" && r.Recovery <= r.Threshold) {
				return errors.New("恢复阈值须提供迟滞范围")
			}
		}
		if r.Event == "traffic.warning" && (r.PolicyID == "" || r.Threshold <= 0 || r.Threshold > 100) {
			return errors.New("流量预警须选择端口统计对象，阈值为 0–100%")
		}
		if r.Event == "panel.update" && (r.Interval < 3600 || r.Interval > 604800) {
			return errors.New("更新检查周期须为 1 小时至 7 天")
		}
	}
	for event, t := range c.Templates {
		if EventNames[event] == "" {
			return errors.New("未知模板事件")
		}
		if err := ValidateTemplate(t); err != nil {
			return err
		}
	}
	return nil
}
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func (c Channel) Get(k string) string {
	if SecretField(k) {
		return c.Secrets[k]
	}
	return c.Values[k]
}
func (c Configuration) Redacted() Configuration {
	out := c
	out.Channels = append([]Channel{}, c.Channels...)
	for i, ch := range out.Channels {
		out.Channels[i].Secrets = nil
		out.Channels[i].Configured = []string{}
		for k, v := range ch.Secrets {
			if v != "" {
				out.Channels[i].Configured = append(out.Channels[i].Configured, k)
			}
		}
	}
	return out
}
