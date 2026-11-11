package store

import (
	"encoding/json"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/geoip"
)

type preferenceRule struct {
	valid  func(json.RawMessage) bool
	schema map[string]any
}

func preferenceString(raw json.RawMessage) (string, bool) {
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}
func preferenceText(max int) preferenceRule {
	return preferenceRule{func(raw json.RawMessage) bool {
		value, ok := preferenceString(raw)
		return ok && utf8.RuneCountInString(value) <= max && !strings.ContainsRune(value, 0)
	}, map[string]any{"type": "string", "maxLength": max}}
}
func preferenceEnum(values ...string) preferenceRule {
	return preferenceRule{func(raw json.RawMessage) bool {
		value, ok := preferenceString(raw)
		if !ok {
			return false
		}
		for _, allowed := range values {
			if value == allowed {
				return true
			}
		}
		return false
	}, map[string]any{"type": "string", "enum": values}}
}
func preferencePattern(pattern string, max int) preferenceRule {
	compiled := regexp.MustCompile(pattern)
	return preferenceRule{func(raw json.RawMessage) bool {
		value, ok := preferenceString(raw)
		return ok && len(value) <= max && compiled.MatchString(value)
	}, map[string]any{"type": "string", "pattern": pattern, "maxLength": max}}
}
func preferenceCalendar(layout string, empty bool) preferenceRule {
	format := "date"
	pattern := `^\d{4}-\d{2}-\d{2}$`
	if layout == "2006-01" {
		format = ""
		pattern = `^\d{4}-\d{2}$`
	}
	if empty {
		pattern = "^(?:" + strings.Trim(pattern, "^$") + "|)$"
	}
	schema := map[string]any{"type": "string", "pattern": pattern, "maxLength": len(layout)}
	// Empty strings represent the latest-date selection.
	if format != "" && !empty {
		schema["format"] = format
	}
	return preferenceRule{func(raw json.RawMessage) bool {
		value, ok := preferenceString(raw)
		if !ok {
			return false
		}
		if empty && value == "" {
			return true
		}
		_, err := time.Parse(layout, value)
		return err == nil
	}, schema}
}

var preferenceBool = preferenceRule{func(raw json.RawMessage) bool { return string(raw) == "true" || string(raw) == "false" }, map[string]any{"type": "boolean"}}
var preferenceAgent = preferencePattern(`^(?:agt_[0-9a-f]{16}|)$`, 20)
var preferenceInstance = preferencePattern(`^(?:main|shr_[0-9a-f]{16}|)$`, 20)
var preferenceTarget = preferencePattern(`^(?:sst_[0-9a-f]{16}|sst_default|)$`, 20)
var preferenceDate = preferenceCalendar("2006-01-02", true)
var preferenceCount = preferencePattern(`^(?:[1-9][0-9]?|[1-4][0-9]{2}|500)$`, 3)
var preferenceIP = preferenceRule{func(raw json.RawMessage) bool {
	value, ok := preferenceString(raw)
	if !ok {
		return false
	}
	if value == "" {
		return true
	}
	address, err := netip.ParseAddr(value)
	return err == nil && address.Zone() == ""
}, map[string]any{"type": "string", "maxLength": 45, "description": "IPv4 or IPv6 address, or an empty filter"}}
var preferenceCountry = preferenceRule{func(raw json.RawMessage) bool {
	value, ok := preferenceString(raw)
	return ok && (value == "" || geoip.ValidRegionCode(value))
}, map[string]any{"type": "string", "maxLength": 2, "description": "Supported country code, or an empty filter"}}

var uiPreferenceRules = map[string]map[string]preferenceRule{
	"dashboard":     {"traffic_month": preferenceCalendar("2006-01", false)},
	"node-region":   {"region_search": preferenceText(200)},
	"notifications": {"template_event": preferenceEnum("task.failed", "agent.offline", "agent.online", "traffic.quota", "traffic.warning", "speed.high", "speed.low", "speed.recovered", "panel.update")},
	"shell":         {"collapsed": preferenceBool},
	"nodes":         {"search": preferenceText(200)},
	"certificates":  {"search": preferenceText(200)},
	"users":         {"query": preferenceText(200)},
	"traffic":       {"search": preferenceText(200), "scope": preferenceEnum("current", "all"), "agent": preferenceAgent, "engine": preferenceEnum("", "mihomo", "xray", "sing-box", "ss-rust"), "status": preferenceEnum("", "active", "metering", "blocked", "waiting", "stopped", "offline", "error"), "view": preferenceEnum("cards", "list"), "advanced": preferenceBool, "manage": preferenceBool},
	"tasks":         {"status": preferenceEnum("", "pending", "running", "succeeded", "failed", "canceled"), "agent_id": preferenceAgent, "action": preferenceEnum("", "validate", "deploy", "install", "uninstall", "start", "stop", "restart", "status", "upgrade-agent", "ip-quality", "enable-bbr", "disable-bbr", "configure-tcp", "panel-failure"), "limit": preferenceCount},
	"logs":          {"agent_id": preferenceAgent, "instance_id": preferenceInstance, "engine": preferenceEnum("", "mihomo", "xray", "sing-box", "ss-rust"), "level": preferenceEnum("", "info", "warning", "error"), "q": preferenceText(120), "limit": preferenceEnum("100", "200", "500", "1000", "2000"), "automatic": preferenceBool},
	"connections":   {"agent_id": preferenceAgent, "date": preferenceCalendar("2006-01-02", false), "time_mode": preferenceEnum("today", "month", "absolute"), "period": preferenceEnum("day", "month"), "client_ip": preferenceIP, "country_code": preferenceCountry, "engine": preferenceEnum("", "mihomo", "xray", "sing-box", "ss-rust"), "include_non_public": preferenceBool},
	"client-access": {"search": preferenceText(200), "agent": preferenceAgent, "format": preferenceEnum("url", "mihomo")},
	"substore":      {"search": preferenceText(200), "agent": preferenceAgent, "target": preferenceTarget},
	"api":           {"tab": preferenceEnum("keys", "docs"), "api-filter": preferenceText(200), "api-group": preferenceEnum("", "配置", "节点与共享", "任务与部署", "证书", "流量与日志", "客户端与订阅", "设置与用户", "认证与接入")},
	"ip-quality":    {"agent": preferenceAgent, "date": preferenceDate},
	"tcp":           {"agent": preferenceAgent},
	// Live configuration deliberately remembers ONLY the node.
	"live-config": {"agent": preferenceAgent},
}

// UIPreferenceSchema is generated from the same field definitions used for
// validation, so the published contract cannot silently accept extra fields.
func UIPreferenceSchema() map[string]any {
	pages := map[string]any{}
	for page, fields := range uiPreferenceRules {
		properties := map[string]any{}
		for key, rule := range fields {
			properties[key] = rule.schema
		}
		pages[page] = map[string]any{"type": "object", "properties": properties, "additionalProperties": false, "minProperties": 1, "maxProperties": len(fields)}
	}
	return map[string]any{"type": "object", "properties": pages, "additionalProperties": false, "minProperties": 1, "maxProperties": len(pages)}
}
