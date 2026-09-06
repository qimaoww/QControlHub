// panel-demo serves the real frontend against an isolated in-memory model.
// It never imports the Agent executor, opens a database or contacts a node.
// No production deployment target includes this command.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

type object = map[string]any

type demo struct {
	mu             sync.Mutex
	agents         []object
	configs        map[string]*core.Config
	revisions      map[string][]core.Config
	tasks          []*core.Task
	snapshots      map[string]string
	settings       object
	enrollments    []object
	policies       []object
	targets        []object
	subSettings    object
	selections     map[string][]object
	accessPolicies map[string]object
	templates      []object
	session        bool
	failNext       bool
	serial         int
}

var engines = []core.Engine{core.EngineShadowsocksRust, core.EngineMihomo, core.EngineXray, core.EngineSingBox}

func value(m object, key string) string { return fmt.Sprint(m[key]) }
func str(m object, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case core.Engine:
		return string(v)
	case core.Action:
		return string(v)
	case core.TaskStatus:
		return string(v)
	}
	return ""
}
func number(m object, key string) int {
	n, _ := m[key].(float64)
	if i, ok := m[key].(int); ok {
		return i
	}
	return int(n)
}
func boolean(m object, key string) bool { b, _ := m[key].(bool); return b }
func stamp() string                     { return time.Now().UTC().Format(time.RFC3339) }
func (d *demo) id(prefix string) string {
	d.serial++
	return fmt.Sprintf("%s_demo_%06d", prefix, d.serial)
}

func (d *demo) reset() error {
	d.agents = []object{}
	d.configs = map[string]*core.Config{}
	d.revisions = map[string][]core.Config{}
	d.tasks = []*core.Task{}
	d.snapshots = map[string]string{}
	d.policies = []object{}
	d.selections = map[string][]object{}
	d.accessPolicies = map[string]object{}
	d.templates = []object{}
	d.session, d.failNext, d.serial = true, false, 0
	d.settings = object{
		"revision": 1, "panel_name": "QControlHub · 演示", "panel_description": "完整真实面板 / 所有数据均为演示",
		"time_zone": "Asia/Shanghai", "time_display": "absolute-relative", "ui_font_scale": 100, "default_config_editor": "structured",
		"task_page_size": 50, "task_poll_interval_ms": 1000, "agent_heartbeat_interval_seconds": 15, "agent_metrics_interval_seconds": 5,
		"agent_offline_threshold_seconds": 60, "task_stale_timeout_seconds": 120, "install_task_stale_timeout_seconds": 600,
		"task_max_attempts": 3, "public_ip_probe_interval_seconds": 900, "core_log_minimum_level": "info", "core_log_retention_days": 7,
		"agent_core_log_max_mib": 4, "agent_core_log_rotate_count": 2, "metric_retention_days": 30, "audit_retention_days": 90,
		"task_retention_days": 90, "config_revision_retention": 50, "notify_task_failed": true, "notify_agent_offline": true,
		"notify_agent_online": true, "notify_traffic_quota": true, "komari_url": "https://monitor.example.invalid", "komari_api_key": "",
	}
	d.subSettings = object{"configured": true, "endpoint_hint": "substore.example.invalid / 本地演示", "base_url": "https://substore.example.invalid", "url": "https://substore.example.invalid", "api_url": "https://substore.example.invalid", "api_key_configured": true}
	d.targets = []object{{"id": "group-main", "display_name": "主力节点", "subscription_name": "demo-main", "sync_mode": "incremental", "last_synced_at": stamp(), "last_sync_status": "succeeded"}, {"id": "group-backup", "display_name": "备用节点", "subscription_name": "demo-backup", "sync_mode": "incremental"}}
	d.enrollments = []object{}
	for i, name := range []string{"DataWave HK", "Catixs HK", "Singapore Edge", "Tokyo Backup"} {
		id := fmt.Sprintf("node-demo-%d", i+1)
		runtime := object{}
		for _, engine := range engines {
			runtime[string(engine)] = object{"installed": true, "version": string(engine) + " 1.25.0", "service_status": "active", "existing_config_available": true, "existing_service_status": "active", "existing_config_path": "/etc/" + string(engine) + "/config.json"}
		}
		if i == 2 {
			runtime["xray"] = object{"installed": false, "service_status": "inactive", "existing_config_available": false}
		}
		status := "online"
		if i == 3 {
			status = "offline"
		}
		d.agents = append(d.agents, object{"id": id, "name": name, "status": status, "os": "linux", "arch": "amd64", "version": "demo-motion-v2", "capabilities": engines,
			"features": []string{"agent-self-upgrade-v1", "port-traffic-v1", "core-logs-v1", "public-ip-probe-v1", "managed-config-read-v1", "mihomo-development-source-v1"},
			"labels":   object{"region": []string{"HK", "HK", "SG", "JP"}[i], "komari_uuid": "komari-" + id}, "runtime": runtime, "last_seen": stamp(), "enrolled_at": stamp(), "enrollment_command_available": true,
			"metrics": object{"cpu_available": true, "cpu_percent": 18 + i*7, "memory_available": true, "memory_used_bytes": 640_000_000, "memory_total_bytes": 2_147_483_648,
				"disk_available": true, "disk_used_bytes": 8_000_000_000, "disk_total_bytes": 40_000_000_000, "network_available": true, "network_rx_bps": 2_600_000, "network_tx_bps": 840_000,
				"network_rx_bytes": 124_000_000_000, "network_tx_bytes": 72_000_000_000, "public_ipv4": fmt.Sprintf("192.0.2.%d", i+10), "public_ipv4_source": "agent-config",
				"public_ipv6": fmt.Sprintf("2001:db8::%d", i+10), "public_ipv6_source": "agent-config", "collected_at": stamp()},
		})
		d.enrollments = append(d.enrollments, object{"id": "enr-" + id, "name": name, "reusable": true, "used_count": 1, "max_uses": 0, "command_available": true, "created_at": stamp()})
		for n, engine := range engines {
			protocol, _ := serverconfig.FindProtocol(engine, serverconfig.ProtocolSS2022)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				return err
			}
			input.Tag, input.Port = "entry-main", 8388+n*100
			input.BlockMainlandDestination, input.BlockMainlandSource = false, false
			generated, err := serverconfig.Generate(engine, input)
			if err != nil {
				return err
			}
			content, err := serverconfig.MutateGenerated(engine, "{}", generated, "", "add")
			if err != nil {
				return err
			}
			input.Tag, input.Port = "entry-backup", input.Port+1
			generated, err = serverconfig.Generate(engine, input)
			if err != nil {
				return err
			}
			content, err = serverconfig.MutateGenerated(engine, content, generated, "", "add")
			if err != nil {
				return err
			}
			c := &core.Config{ID: d.id("cfg"), AgentID: id, Name: name + " / " + string(engine), Description: "演示配置，不连接真实节点", Engine: engine, Content: content, Version: 3, CreatedAt: time.Now(), UpdatedAt: time.Now()}
			d.configs[c.ID] = c
			for v := 1; v <= 3; v++ {
				snapshot := *c
				snapshot.Version = v
				snapshot.CreatedAt = time.Now().Add(-time.Duration(4-v) * time.Hour)
				d.revisions[c.ID] = append(d.revisions[c.ID], snapshot)
			}
			if i == 0 {
				d.templates = append(d.templates, object{"id": "tpl-" + string(engine), "name": string(engine) + " 双端口模板", "engine": engine, "content": content, "description": "演示模板", "created_at": stamp(), "updated_at": stamp()})
			}
			d.policies = append(d.policies, object{"id": d.id("quota"), "agent_id": id, "name": string(engine) + " main", "engine": engine, "port": 8388 + n*100, "protocol": "both", "cycle": "monthly", "cycle_anchor": stamp(), "quota_enabled": true, "monitoring_enabled": true, "auto_block": true, "limit_bytes": 500_000_000_000.0, "used_bytes": float64(40_000_000_000 + i*8_000_000_000), "received_bytes": 24_000_000_000.0, "sent_bytes": 16_000_000_000.0, "receive_bps": 2_600_000, "send_bps": 840_000, "last_reported_at": stamp(), "period_start": time.Now().Format("2006-01") + "-01T00:00:00Z"})
		}
	}
	for i, status := range []core.TaskStatus{core.TaskSucceeded, core.TaskSucceeded, core.TaskFailed, core.TaskCanceled} {
		t := d.newTask(object{"agent_id": str(d.agents[i], "id"), "engine": "ss-rust", "action": "deploy"})
		now := time.Now().Add(-time.Duration(i+1) * time.Minute)
		t.CreatedAt = now
		t.StartedAt = &now
		done := now.Add(time.Second)
		t.FinishedAt = &done
		t.Status = status
		if status == core.TaskFailed {
			t.Error = "演示失败：配置版本冲突，可体验查看结果与重试。"
		}
	}
	return nil
}

func (d *demo) agent(id string) object {
	for _, a := range d.agents {
		if str(a, "id") == id {
			return a
		}
	}
	return nil
}
func (d *demo) config(agentID string, engine core.Engine) *core.Config {
	for _, c := range d.configs {
		if c.AgentID == agentID && c.Engine == engine {
			return c
		}
	}
	return nil
}
func (d *demo) configList(agentID string) []*core.Config {
	list := []*core.Config{}
	for _, c := range d.configs {
		if agentID == "" || c.AgentID == agentID {
			list = append(list, c)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}
func (d *demo) save(c *core.Config, body object) error {
	if version := number(body, "expected_version"); version != 0 && version != c.Version {
		return errors.New("演示配置版本已变更，请刷新后重试")
	}
	if v, ok := body["content"].(string); ok {
		c.Content = v
	}
	if v := str(body, "name"); v != "" {
		c.Name = v
	}
	if v, ok := body["description"].(string); ok {
		c.Description = v
	}
	c.Version++
	c.UpdatedAt = time.Now()
	d.revisions[c.ID] = append(d.revisions[c.ID], *c)
	return nil
}

func (d *demo) newTask(body object) *core.Task {
	now := time.Now()
	t := &core.Task{ID: d.id("tsk"), AgentID: str(body, "agent_id"), Engine: core.Engine(str(body, "engine")), Action: core.Action(str(body, "action")), ConfigID: str(body, "config_id"), Attempt: 1, Status: core.TaskRunning, CreatedAt: now, StartedAt: &now, Output: "[DEMO] 此操作仅更新演示内存数据，没有连接节点或执行系统命令。"}
	c := d.configs[t.ConfigID]
	if c == nil {
		c = d.config(t.AgentID, t.Engine)
	}
	if c != nil {
		t.ConfigID = c.ID
		t.ConfigVersion = c.Version
		d.snapshots[t.ID] = c.Content
	}
	t.CoreVersion = str(body, "core_version")
	t.CoreSource = str(body, "core_source")
	d.tasks = append([]*core.Task{t}, d.tasks...)
	return t
}

func (d *demo) settle() {
	for _, t := range d.tasks {
		if t.Status != core.TaskRunning || time.Since(t.CreatedAt) < 1100*time.Millisecond {
			continue
		}
		now := time.Now()
		t.Status = core.TaskSucceeded
		t.FinishedAt = &now
		if a := d.agent(t.AgentID); a != nil {
			if t.Action == "upgrade-agent" {
				a["version"] = "demo-motion-v2-updated"
			}
			if runtime, ok := a["runtime"].(object); ok {
				if service, ok := runtime[string(t.Engine)].(object); ok {
					if t.Action == "install" {
						service["installed"] = true
						service["version"] = string(t.Engine) + " demo-updated"
					}
					if t.Action == "stop" {
						service["service_status"] = "inactive"
					} else if t.Action == "start" || t.Action == "restart" || t.Action == "deploy" {
						service["service_status"] = "active"
					}
				}
			}
		}
	}
}

func (d *demo) clients() []object {
	entries := []object{}
	for _, c := range d.configList("") {
		a := d.agent(c.AgentID)
		if a == nil {
			continue
		}
		address := str(a, "client_address")
		if address == "" {
			address = str(a["metrics"].(object), "public_ipv4")
		}
		profiles := []object{}
		for _, inbound := range serverconfig.ParseAll(c.Engine, c.Content) {
			profile, err := serverconfig.BuildClientProfileNamed(inbound, address, inbound.RealityServerName, str(a, "name"))
			if err != nil {
				continue
			}
			profiles = append(profiles, object{"tag": inbound.Tag, "protocol": inbound.Protocol, "port": inbound.Port, "profile": profile})
		}
		entries = append(entries, object{"agent_id": c.AgentID, "agent_name": a["name"], "agent_status": a["status"], "engine": c.Engine, "address": address, "source": "agent-config", "client_name": a["client_name"], "profiles": profiles, "config_id": c.ID, "config_version": c.Version,
			"address_options": []object{{"family": "ipv4", "address": address, "source": "agent-config", "profiles": profiles}, {"family": "ipv6", "address": str(a["metrics"].(object), "public_ipv6"), "source": "agent-config", "profiles": profiles}}})
	}
	return entries
}

func (d *demo) access() []object {
	rows := []object{}
	for _, c := range d.configList("") {
		a := d.agent(c.AgentID)
		if a == nil {
			continue
		}
		for _, inbound := range serverconfig.ParseAll(c.Engine, c.Content) {
			row := object{"agent_id": c.AgentID, "agent_name": a["name"], "agent_status": a["status"], "engine": c.Engine, "tag": inbound.Tag, "port": inbound.Port, "kind": inbound.Protocol, "config_version": c.Version, "block_mainland_destination": inbound.BlockMainlandDestination, "block_mainland_source": inbound.BlockMainlandSource}
			if policy := d.accessPolicies[c.ID+"/"+inbound.Tag]; policy != nil {
				row["block_mainland_destination"] = policy["block_mainland_destination"]
				row["block_mainland_source"] = policy["block_mainland_source"]
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func (d *demo) substore(targetID string) object {
	if targetID == "" && len(d.targets) > 0 {
		targetID = str(d.targets[0], "id")
	}
	profiles := []object{}
	for _, entry := range d.clients() {
		for _, p := range entry["profiles"].([]object) {
			row := object{"agent_id": entry["agent_id"], "agent_name": entry["agent_name"], "agent_status": entry["agent_status"], "engine": entry["engine"], "profile_tag": p["tag"], "protocol": p["protocol"], "port": p["port"], "available": true, "selected": true, "default_name": value(entry, "agent_name") + " / " + value(entry, "engine"), "custom_name": "", "address_mode": "auto", "addresses": []object{{"family": "ipv4", "address": entry["address"]}, {"family": "ipv6", "address": "2001:db8::10"}}}
			for _, selection := range d.selections[targetID] {
				if value(selection, "agent_id") == value(row, "agent_id") && value(selection, "engine") == value(row, "engine") && value(selection, "profile_tag") == value(row, "profile_tag") {
					for key, v := range selection {
						row[key] = v
					}
				}
			}
			profiles = append(profiles, row)
		}
	}
	return object{"settings": d.subSettings, "targets": d.targets, "target_id": targetID, "profiles": profiles}
}

func bodyAs[T any](m object) T {
	var result T
	data, _ := json.Marshal(m)
	_ = json.Unmarshal(data, &result)
	return result
}

func (d *demo) api(method, path string, request *http.Request, body object) (any, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	query := request.URL.Query()
	if path == "/auth/session" {
		if !d.session {
			return nil, errUnauthorized
		}
		return object{"role": "admin", "username": "admin", "csrf_token": "demo-local-only"}, nil
	}
	if path == "/auth/login" {
		d.session = true
		return object{"role": "admin", "username": "admin", "csrf_token": "demo-local-only"}, nil
	}
	if path == "/auth/logout" {
		d.session = false
		return object{}, nil
	}
	if !d.session {
		return nil, errUnauthorized
	}
	if path == "/overview" {
		online := 0
		for _, a := range d.agents {
			if a["status"] == "online" {
				online++
			}
		}
		return object{"agents": len(d.agents), "agents_online": online, "node_configs": len(d.configs), "configs": len(d.configs), "tasks": len(d.tasks), "tasks_queued": 0, "tasks_running": 0}, nil
	}
	if path == "/settings" {
		if method != "GET" {
			for k, v := range body {
				d.settings[k] = v
			}
			d.settings["revision"] = number(d.settings, "revision") + 1
		}
		return d.settings, nil
	}
	if path == "/settings/deployment" {
		return object{"control_plane_version": "demo-motion-v2", "agent_package_version": "demo-motion-v2", "config_encryption_configured": true, "database_tls_verified": true, "secure_transport": true, "webhook_signing_configured": true, "trusted_proxy_count": 1}, nil
	}
	if path == "/settings/check-update" {
		return object{"comparable": true, "update_available": false, "current_control_plane": "demo-motion-v2", "latest_version": "demo-motion-v2", "release_url": "#settings-deployment"}, nil
	}
	if path == "/agents" {
		for _, a := range d.agents {
			if a["status"] == "online" {
				a["last_seen"] = stamp()
				a["metrics"].(object)["collected_at"] = stamp()
			}
		}
		return d.agents, nil
	}
	if path == "/enrollment-tokens" {
		if method == "GET" {
			return d.enrollments, nil
		}
		id := d.id("enr")
		d.enrollments = append(d.enrollments, object{"id": id, "name": str(body, "name"), "command_available": true, "reusable": true, "created_at": stamp()})
		return object{"token": "DEMO-NOT-A-REAL-TOKEN", "name": str(body, "name")}, nil
	}
	if len(parts) >= 2 && parts[0] == "enrollment-tokens" {
		if method == "DELETE" {
			for i, e := range d.enrollments {
				if str(e, "id") == parts[1] {
					d.enrollments = append(d.enrollments[:i], d.enrollments[i+1:]...)
					break
				}
			}
			return object{}, nil
		}
		return object{"token": "DEMO-NOT-A-REAL-TOKEN", "name": "演示节点"}, nil
	}
	if len(parts) >= 2 && parts[0] == "agents" {
		a := d.agent(parts[1])
		if a == nil {
			return nil, errNotFound
		}
		if len(parts) == 2 && method == "DELETE" {
			for i, a := range d.agents {
				if str(a, "id") == parts[1] {
					d.agents = append(d.agents[:i], d.agents[i+1:]...)
					break
				}
			}
			return object{}, nil
		}
		if len(parts) == 3 {
			switch parts[2] {
			case "configs":
				return d.configList(parts[1]), nil
			case "region":
				return object{"country_code": str(a["labels"].(object), "region"), "country": "演示地区", "ip": str(a["metrics"].(object), "public_ipv4")}, nil
			case "enrollment-command":
				return object{"name": a["name"], "token": "DEMO-NOT-A-REAL-TOKEN"}, nil
			case "komari":
				if method != "GET" {
					a["labels"].(object)["komari_uuid"] = str(body, "uuid")
				}
				return object{"uuid": a["labels"].(object)["komari_uuid"], "server": object{"name": a["name"], "billing_cycle": 30, "traffic_limit": 1_000_000_000_000.0, "traffic_limit_type": "sum", "traffic_used": 128_000_000_000.0, "traffic_used_available": true, "expired_at": time.Now().Add(15 * 24 * time.Hour).Format(time.RFC3339)}}, nil
			case "client-address":
				if method == "DELETE" {
					delete(a, "client_address")
					delete(a, "client_name")
				} else {
					a["client_address"] = body["address"]
					a["client_name"] = body["name"]
				}
				return object{"address": a["client_address"], "name": a["client_name"]}, nil
			}
		}
		if len(parts) >= 4 && parts[2] == "configs" {
			return d.configAPI(a, core.Engine(parts[3]), parts[4:], method, query.Get("inbound"), body)
		}
	}
	if path == "/configs" {
		if method == "GET" {
			return d.configList(""), nil
		}
		c := &core.Config{ID: d.id("cfg"), Engine: core.Engine(str(body, "engine")), Version: 0, CreatedAt: time.Now()}
		d.configs[c.ID] = c
		return c, d.save(c, body)
	}
	if len(parts) >= 2 && parts[0] == "configs" {
		c := d.configs[parts[1]]
		if c == nil {
			return nil, errNotFound
		}
		if len(parts) == 2 {
			if method == "DELETE" {
				delete(d.configs, c.ID)
				return object{}, nil
			}
			if method != "GET" {
				return c, d.save(c, body)
			}
			return c, nil
		}
		if parts[2] == "revisions" {
			if len(parts) == 3 {
				rows := append([]core.Config{}, d.revisions[c.ID]...)
				sort.Slice(rows, func(i, j int) bool { return rows[i].Version > rows[j].Version })
				return rows, nil
			}
			v, _ := strconv.Atoi(parts[3])
			for _, r := range d.revisions[c.ID] {
				if r.Version == v {
					if len(parts) > 4 && parts[4] == "restore" {
						body["content"] = r.Content
						return c, d.save(c, body)
					}
					return r, nil
				}
			}
		}
	}
	if path == "/client-access" {
		return d.clients(), nil
	}
	if path == "/access-controls" {
		if method == "GET" {
			return d.access(), nil
		}
		c := d.config(str(body, "agent_id"), core.Engine(str(body, "engine")))
		if c == nil {
			return nil, errNotFound
		}
		if err := d.save(c, body); err != nil {
			return nil, err
		}
		d.accessPolicies[c.ID+"/"+str(body, "tag")] = body
		return object{"config": c, "task": d.newTask(object{"agent_id": c.AgentID, "engine": c.Engine, "action": str(body, "intent"), "config_id": c.ID})}, nil
	}
	if path == "/deployments" {
		rows := []object{}
		for _, c := range d.configList("") {
			rows = append(rows, object{"agent_id": c.AgentID, "engine": c.Engine, "config_id": c.ID, "config_version": c.Version, "deployed_at": stamp(), "content": c.Content})
		}
		return rows, nil
	}
	if path == "/tasks" {
		if method != "GET" {
			return d.newTask(body), nil
		}
		rows := []*core.Task{}
		for _, t := range d.tasks {
			if query.Get("agent_id") != "" && t.AgentID != query.Get("agent_id") {
				continue
			}
			if query.Get("status") != "" && string(t.Status) != query.Get("status") {
				continue
			}
			if query.Get("action") != "" && string(t.Action) != query.Get("action") {
				continue
			}
			rows = append(rows, t)
		}
		return rows, nil
	}
	if len(parts) >= 2 && parts[0] == "tasks" {
		for _, t := range d.tasks {
			if t.ID != parts[1] {
				continue
			}
			if len(parts) == 2 {
				if method == "DELETE" {
					t.Status = core.TaskCanceled
				}
				return t, nil
			}
			if parts[2] == "config-snapshot" {
				return object{"content": d.snapshots[t.ID], "engine": t.Engine, "config_id": t.ConfigID}, nil
			}
			if parts[2] == "retry" {
				return d.newTask(object{"agent_id": t.AgentID, "engine": string(t.Engine), "action": string(t.Action), "config_id": t.ConfigID}), nil
			}
		}
		return nil, errNotFound
	}
	if path == "/traffic-policies" {
		if method == "GET" {
			return d.policies, nil
		}
		body["id"] = d.id("quota")
		body["monitoring_enabled"] = true
		body["quota_enabled"] = true
		body["used_bytes"] = 0
		body["last_reported_at"] = stamp()
		d.policies = append(d.policies, body)
		return body, nil
	}
	if len(parts) >= 2 && parts[0] == "traffic-policies" {
		for i, p := range d.policies {
			if str(p, "id") != parts[1] {
				continue
			}
			if len(parts) > 2 && parts[2] == "reset" {
				p["used_bytes"] = 0
				p["blocked"] = false
			} else if method == "DELETE" {
				if len(parts) > 2 {
					d.policies = append(d.policies[:i], d.policies[i+1:]...)
				} else {
					p["quota_enabled"] = false
				}
			} else {
				for k, v := range body {
					p[k] = v
				}
				p["quota_enabled"] = true
			}
			return p, nil
		}
		return nil, errNotFound
	}
	if path == "/traffic-endpoints" {
		rows := []object{}
		for _, c := range d.configList("") {
			for _, p := range serverconfig.DiscoverTrafficPorts(c.Engine, c.Content) {
				rows = append(rows, object{"agent_id": c.AgentID, "engine": c.Engine, "port": p.Port, "protocol": p.Protocol, "name": p.Name})
			}
		}
		return rows, nil
	}
	if path == "/traffic-usage" {
		month := query.Get("month")
		if month == "" {
			month = time.Now().Format("2006-01")
		}
		days := []object{}
		for i := 1; i <= 28; i++ {
			rx := float64((i%7 + 2) * 1_000_000_000)
			tx := rx * .7
			days = append(days, object{"day": fmt.Sprintf("%s-%02d", month, i), "received_bytes": rx, "sent_bytes": tx, "used_bytes": rx + tx, "peak_receive_bps": 8_000_000, "peak_send_bps": 3_000_000})
		}
		return object{"month": month, "timezone": "UTC", "days": days}, nil
	}
	if len(parts) > 0 && parts[0] == "metrics" {
		rows := []object{}
		for i := 0; i < 48; i++ {
			rows = append(rows, object{"collected_at": time.Now().Add(time.Duration(i-48) * time.Minute), "rx_rate_bps": 800_000 + (i%9)*80_000, "tx_rate_bps": 300_000 + (i%5)*20_000, "cpu_percent": 12 + i%17})
		}
		return rows, nil
	}
	if path == "/core-logs" {
		rows := []object{}
		for i := 0; i < 32 && len(d.agents) > 0; i++ {
			a := d.agents[i%len(d.agents)]
			if query.Get("agent_id") != "" && query.Get("agent_id") != str(a, "id") {
				continue
			}
			rows = append(rows, object{"id": fmt.Sprintf("log-demo-%d", i), "agent_id": a["id"], "engine": engines[i%len(engines)], "logged_at": time.Now().Add(-time.Duration(i) * 17 * time.Second), "level": []string{"info", "info", "warning", "error"}[i%4], "message": []string{"[DEMO] managed listener ready", "[DEMO] outbound connection accepted", "[DEMO] retrying simulated connection", "[DEMO] simulated connection timeout"}[i%4]})
		}
		return rows, nil
	}
	if parts[0] == "templates" {
		if len(parts) == 1 {
			if method != "GET" {
				body["id"] = d.id("tpl")
				d.templates = append(d.templates, body)
			}
			return d.templates, nil
		}
		for i, t := range d.templates {
			if str(t, "id") != parts[1] {
				continue
			}
			if method == "DELETE" {
				d.templates = append(d.templates[:i], d.templates[i+1:]...)
				return object{}, nil
			}
			if len(parts) > 2 && parts[2] == "apply" {
				c := d.config(str(body, "agent_id"), core.Engine(value(t, "engine")))
				if c == nil {
					return nil, errNotFound
				}
				return c, d.save(c, object{"content": t["content"]})
			}
			return t, nil
		}
	}
	if parts[0] == "substore-sync" {
		return d.substoreAPI(parts[1:], method, query.Get("target_id"), body)
	}
	return nil, fmt.Errorf("此演示入口尚未实现：%s %s（不会转发到真实服务）", method, path)
}

func (d *demo) configAPI(a object, engine core.Engine, tail []string, method, inbound string, body object) (any, error) {
	c := d.config(str(a, "id"), engine)
	catalog, err := configschema.CatalogFor(engine)
	if err != nil {
		return nil, err
	}
	if len(tail) == 0 {
		if c == nil {
			c = &core.Config{ID: d.id("cfg"), AgentID: str(a, "id"), Engine: engine, CreatedAt: time.Now()}
			d.configs[c.ID] = c
		}
		if method != "GET" {
			return c, d.save(c, body)
		}
		return c, nil
	}
	if tail[0] == "workspace" {
		inputs := []serverconfig.Input{}
		present := map[string]bool{}
		if c != nil {
			inputs = serverconfig.ParseAll(engine, c.Content)
			present, _ = configschema.RootKeys(engine, c.Content)
		}
		return object{"agent": a, "config": c, "catalog": catalog, "protocols": serverconfig.Protocols(engine), "inbounds": inputs, "present_fields": present, "reality_presets": serverconfig.RealityServerNamePresets()}, nil
	}
	if tail[0] == "plans" {
		p, ok := serverconfig.FindProtocol(engine, str(body, "protocol"))
		if !ok {
			return nil, errors.New("未知预设协议")
		}
		if input, ok := body["input"].(map[string]any); ok {
			return serverconfig.RegeneratePlan(p, bodyAs[serverconfig.Input](input))
		}
		return serverconfig.NewPlan(p)
	}
	if c == nil {
		return nil, errNotFound
	}
	if tail[0] == "fields" && len(tail) > 1 {
		key := tail[1]
		if method == "GET" {
			fragment, present, err := configschema.Fragment(engine, c.Content, key)
			inherited := ""
			if inbound != "" {
				fragment, present, inherited, err = serverconfig.SSRustInboundField(c.Content, inbound, key)
			}
			return object{"fragment": fragment, "present": present, "inherited_fragment": inherited, "key": key}, err
		}
		var content string
		if inbound != "" {
			content, err = serverconfig.MergeSSRustInboundField(c.Content, inbound, key, str(body, "fragment"), str(body, "mutation") == "delete")
		} else {
			content, err = configschema.MergeFragment(engine, c.Content, key, str(body, "fragment"), str(body, "mutation") == "delete")
		}
		if err != nil {
			return nil, err
		}
		body["content"] = content
	} else if tail[0] == "server-inbounds" {
		inputBody, ok := body["input"].(map[string]any)
		if !ok {
			return nil, errors.New("请提供入站参数")
		}
		input := bodyAs[serverconfig.Input](inputBody)
		generated := ""
		if str(body, "operation") != "delete" {
			generated, err = serverconfig.Generate(engine, input)
			if err != nil {
				return nil, err
			}
		}
		var content string
		if engine == core.EngineShadowsocksRust && boolean(body, "preserve_ss_rust_globals") {
			content, err = serverconfig.MutateSSRustPort(c.Content, generated, str(body, "original_tag"), str(body, "operation"))
		} else {
			content, err = serverconfig.MutateGenerated(engine, c.Content, generated, str(body, "original_tag"), str(body, "operation"))
		}
		if err != nil {
			return nil, err
		}
		body["content"] = content
	} else {
		return nil, errNotFound
	}
	if err = d.save(c, body); err != nil {
		return nil, err
	}
	return object{"config": c, "task": d.newTask(object{"agent_id": c.AgentID, "engine": string(engine), "config_id": c.ID, "action": str(body, "intent")})}, nil
}

func (d *demo) substoreAPI(tail []string, method, targetID string, body object) (any, error) {
	if len(tail) == 0 {
		return d.substore(targetID), nil
	}
	switch tail[0] {
	case "settings":
		for k, v := range body {
			d.subSettings[k] = v
		}
		d.subSettings["configured"] = true
		return d.subSettings, nil
	case "test":
		return object{"ok": true, "message": "演示连接成功，未连接外部服务"}, nil
	case "remote-targets":
		return []object{{"subscription_name": "demo-main", "node_count": 16, "imported": true}, {"subscription_name": "demo-backup", "node_count": 8, "imported": true}, {"subscription_name": "demo-new-remote", "node_count": 4, "imported": false}}, nil
	case "selections":
		d.selections[str(body, "target_id")] = bodyAs[struct {
			Selections []object `json:"selections"`
		}](body).Selections
		return object{}, nil
	case "run":
		for _, t := range d.targets {
			if str(t, "id") == str(body, "target_id") {
				t["last_synced_at"] = stamp()
				t["last_sync_status"] = "succeeded"
			}
		}
		return object{"node_count": 16, "synced_nodes": 16, "message": "演示同步完成"}, nil
	case "targets":
		if len(tail) == 1 || tail[1] == "import" {
			body["id"] = d.id("group")
			if str(body, "subscription_name") == "" {
				body["subscription_name"] = "demo-new"
			}
			d.targets = append(d.targets, body)
			return body, nil
		}
		for i, t := range d.targets {
			if str(t, "id") != tail[1] {
				continue
			}
			if method == "DELETE" {
				d.targets = append(d.targets[:i], d.targets[i+1:]...)
				return object{}, nil
			}
			for k, v := range body {
				t[k] = v
			}
			return t, nil
		}
	}
	return nil, errNotFound
}

var errNotFound = errors.New("演示数据不存在")
var errUnauthorized = errors.New("请使用演示账号登录")

func writeJSON(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

func loopbackAuthority(authority string) bool {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		// An explicit forwarded port is optional for the standard HTTP(S) ports.
		host = authority
		if strings.ContainsAny(host, ":/\\@?#") {
			return false
		}
	} else {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func localPreviewRequest(r *http.Request) bool {
	if !loopbackAuthority(r.Host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	requestOrigin := r.Header.Get("Origin")
	if requestOrigin == "" {
		return true
	}
	u, err := url.Parse(requestOrigin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !loopbackAuthority(u.Host) {
		return false
	}
	// Browser previews may map remote 127.0.0.1:45561 to localhost:2802.
	// Some tunnels preserve Host; others rewrite it but retain the browser's
	// same-origin metadata. Never trust arbitrary X-Forwarded-Host values.
	return strings.EqualFold(u.Host, r.Host) || r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

func main() {
	port := flag.Int("port", 0, "loopback preview port (0 chooses a free port)")
	flag.Parse()
	root, err := filepath.Abs("frontend")
	if err != nil {
		log.Fatal(err)
	}
	d := &demo{}
	if err = d.reset(); err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'self'; base-uri 'none'; form-action 'self'")
		if !localPreviewRequest(r) {
			http.Error(w, "Local demo requests only", 403)
			return
		}
		path := r.URL.Path
		if path == "/" || path == "/panel-demo.html" || path == "/motion-demo.html" {
			http.ServeFile(w, r, filepath.Join(root, "panel-demo.html"))
			return
		}
		if strings.HasPrefix(path, "/assets/") {
			asset := strings.TrimPrefix(path, "/assets/")
			allowed := asset == "app.js" || asset == "app.css" || (strings.HasPrefix(asset, "modules/") && strings.Count(asset, "/") == 1 && strings.HasSuffix(asset, ".js") && !strings.Contains(asset, ".."))
			if !allowed {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, filepath.Join(root, asset))
			return
		}
		if strings.HasPrefix(path, "/api/v1/region-flags/") {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 48"><rect width="64" height="48" rx="5" fill="#5755e7"/><text x="32" y="30" text-anchor="middle" font-family="sans-serif" font-size="16" fill="white">DEMO</text></svg>`))
			return
		}
		if path == "/install-agent.sh" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("#!/bin/sh\nprintf '%s\\n' 'DEMO ONLY: no installation or node operation is available.'\nexit 1\n"))
			return
		}
		if !strings.HasPrefix(path, "/api/v1/") && path != "/_demo/control" {
			http.NotFound(w, r)
			return
		}
		body := object{}
		if r.Method != "GET" && r.Method != "HEAD" {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3<<20)).Decode(&body); err != nil && r.ContentLength > 0 {
				writeJSON(w, 400, object{"error": "演示请求格式不正确"})
				return
			}
		}
		// Artificial response time is part of the preview, not a real task.
		select {
		case <-time.After(120 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if path == "/_demo/control" {
			if r.Method != "POST" {
				http.Error(w, "POST required", 405)
				return
			}
			if str(body, "action") == "reset" {
				if err := d.reset(); err != nil {
					writeJSON(w, 500, object{"error": err.Error()})
					return
				}
			} else {
				d.failNext = true
			}
			writeJSON(w, 200, object{"ok": true})
			return
		}
		path = strings.TrimPrefix(path, "/api/v1")
		if d.failNext && r.Method != "GET" && !strings.HasPrefix(path, "/auth/") && !strings.HasSuffix(path, "/plans") {
			d.failNext = false
			writeJSON(w, 422, object{"error": "演示失败：用于体验错误反馈，内容没有提交到真实服务。"})
			return
		}
		d.settle()
		result, err := d.api(r.Method, path, r, body)
		if err != nil {
			status := 422
			if errors.Is(err, errNotFound) {
				status = 404
			}
			if errors.Is(err, errUnauthorized) {
				status = 401
			}
			writeJSON(w, status, object{"error": err.Error()})
			return
		}
		writeJSON(w, 200, result)
	})
	fmt.Fprintln(os.Stdout, "Full panel demo:", origin+"/panel-demo.html#dashboard")
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.Serve(listener))
}
