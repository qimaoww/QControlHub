//go:build snapshots

package webui

// 静态渲染审阅工具：以样例数据渲染各页面为完整 HTML，并把内联资源内联，
// 输出到仓库根 .preview/snapshots/，便于用浏览器直接打开做视觉对照。
// 运行：go test -tags snapshots -run TestRenderSnapshots ./internal/webui
// 常规 go test 不包含此文件（build tag）。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func sampleAgents(now time.Time) []core.Agent {
	return []core.Agent{
		{
			ID: "agt_node_alpha", Name: "shanghai-edge-01", Version: "v0.9.0", OS: "Linux", Arch: "amd64",
			Status: "online", Capabilities: []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox},
			Labels:   map[string]string{"public_ip": "203.0.113.10", "region": "cn-east"},
			LastSeen: now.Add(-18 * time.Second), EnrolledAt: now.Add(-30 * 24 * time.Hour),
			Runtime: map[core.Engine]core.RuntimeState{
				core.EngineMihomo:  {Installed: true, Version: "Mihomo Meta v1.19.29 linux amd64 with go1.26.5 Sat Jul 18 12:20:03 UTC 2026", ServiceStatus: "active"},
				core.EngineXray:    {Installed: true, Version: "Xray 26.3.27 (Xray, Penetrates Everything.) d2758a0 (go1.26.1 linux/amd64)", ServiceStatus: "active"},
				core.EngineSingBox: {Installed: false},
			},
			Metrics: core.HostMetrics{
				CollectedAt: now.Add(-10 * time.Second), CPUAvailable: true, CPUPercent: 42.5,
				MemoryAvailable: true, MemoryUsedBytes: 8_589_934_592, MemoryTotalBytes: 17_179_869_184,
				DiskAvailable: true, DiskUsedBytes: 120_000_000_000, DiskTotalBytes: 480_000_000_000,
				NetworkAvailable: true, NetworkRXBPS: 1_250_000, NetworkTXBPS: 380_000, NetworkRXBytes: 400_000_000_000, NetworkTXBytes: 90_000_000_000,
				NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"203.0.113.10", "10.0.0.8"}}},
			},
		},
		{
			ID: "agt_node_beta", Name: "frankfurt-backup-02", Version: "v0.9.0", OS: "Linux", Arch: "arm64",
			Status: "offline", Capabilities: []core.Engine{core.EngineMihomo},
			LastSeen: now.Add(-3 * time.Hour), EnrolledAt: now.Add(-12 * 24 * time.Hour),
			Runtime: map[core.Engine]core.RuntimeState{
				core.EngineMihomo: {Installed: true, Version: "v1.19.28", ServiceStatus: "inactive"},
			},
		},
	}
}

func sampleConfigs(now time.Time) []core.Config {
	return []core.Config{
		{ID: "cfg_mihomo_01", Name: "edge-mihomo", Description: "上海边缘节点", Engine: core.EngineMihomo, Version: 3, UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: "cfg_xray_01", Name: "origin-xray", Engine: core.EngineXray, Version: 1, UpdatedAt: now.Add(-6 * 24 * time.Hour)},
		{ID: "cfg_singbox_01", Name: "relay-singbox", Engine: core.EngineSingBox, Version: 2, UpdatedAt: now.Add(-24 * time.Hour)},
	}
}

func sampleTasks(now time.Time) []core.Task {
	// 注意: running 任务的"已运行 N 分 M 秒"是模板在渲染时取真实时钟减
	// StartedAt 得出的实时值(data-live-task-timing 由 JS 轮询刷新),fixture
	// 取整无法钉住它——这是语义上的实时字段,快照中会自然跳动,属预期。
	start := func(offset time.Duration) *time.Time { v := now.Add(-offset); return &v }
	return []core.Task{
		{ID: "tsk_alpha00000000001", AgentID: "agt_node_alpha", Action: core.ActionDeploy, Engine: core.EngineMihomo, ConfigID: "cfg_mihomo_01", ConfigVersion: 3, Status: core.TaskSucceeded, Simulated: true, Attempt: 1, CreatedAt: now.Add(-2 * time.Hour), StartedAt: start(118 * time.Minute), FinishedAt: start(117 * time.Minute)},
		{ID: "tsk_alpha00000000002", AgentID: "agt_node_alpha", Action: core.ActionReadConfig, Engine: core.EngineXray, Status: core.TaskRunning, Attempt: 2, CreatedAt: now.Add(-3 * time.Minute), StartedAt: start(3 * time.Minute)},
		{ID: "tsk_alpha00000000003", AgentID: "agt_node_beta", Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: "stable", Status: core.TaskPending, Attempt: 0, CreatedAt: now.Add(-30 * time.Second)},
		{ID: "tsk_alpha00000000004", AgentID: "agt_node_beta", Action: core.ActionDeploy, Engine: core.EngineMihomo, ConfigID: "cfg_mihomo_01", ConfigVersion: 2, Status: core.TaskFailed, Attempt: 3, CreatedAt: now.Add(-26 * time.Hour), StartedAt: start(26 * time.Hour), FinishedAt: start(25*time.Hour + 58*time.Minute), Error: "deploy failed: rejected the configuration: listen 0.0.0.0:33238: address already in use", Output: "attempt 1: validate ok\nattempt 2: deploy started\nattempt 3: rollback performed"},
		{ID: "tsk_alpha00000000005", AgentID: "agt_node_alpha", Action: core.ActionStop, Engine: core.EngineXray, Status: core.TaskCanceled, Attempt: 1, CreatedAt: now.Add(-50 * time.Hour)},
	}
}

func samplePageSettings() core.PanelSettings {
	return core.DefaultPanelSettings()
}

func renderInline(html string) string {
	linkRE := regexp.MustCompile(`<link[^>]*href="/assets/([a-z0-9-]+\.css)[^"]*"[^>]*>`)
	html = linkRE.ReplaceAllStringFunc(html, func(match string) string {
		return "<style>\n" + desktopAppStyles + "\n</style>"
	})
	scriptRE := regexp.MustCompile(`(?s)<script([^>]*)src="/assets/([a-z0-9-]+\.js)[^"]*"([^>]*)>.*?</script>`)
	html = scriptRE.ReplaceAllStringFunc(html, func(match string) string {
		parts := scriptRE.FindStringSubmatch(match)
		name := parts[2]
		var code string
		switch name {
		case "theme.js":
			code = colorThemeScript
		case "agent-config.js":
			code = agentConfigScript
		case "metrics.js":
			code = agentMetricsScript
		case "node-workspace.js":
			code = nodeWorkspaceScript
		}
		if code == "" {
			return match
		}
		return "<script>\n" + code + "\n</script>"
	})
	return html
}

func writeSnapshot(t *testing.T, name, html string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// go test 的工作目录是包目录 internal/webui，仓库根在两级之上。
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	dir := filepath.Join(root, ".preview", "snapshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir snapshots: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(html), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
}

func TestRenderSnapshots(t *testing.T) {
	server, err := New(nil, Config{AdminToken: "snap-admin-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 截断到整分钟: clock 会把注册时间渲染到秒,带微秒的 now 会让
	// 相邻两次渲染在秒边界漂移(如 05:55:10 → :11),导致快照哈希抖动。
	// 取整后所有派生时间戳秒恒为 0,快照逐字节确定。
	now := time.Now().Truncate(time.Minute)
	agents := sampleAgents(now)
	configs := sampleConfigs(now)
	tasks := sampleTasks(now)
	settings := samplePageSettings()
	overview := core.Overview{
		Agents: 2, AgentsOnline: 1, NodeConfigs: 2, Configs: 3,
		TasksQueued: 1, TasksPending: 1, TasksRunning: 1, TasksFailed: 1,
	}
	renderApp := func(active, title string, data pageData) string {
		data.Title = title
		data.Active = active
		data.CSRF = "snap-csrf"
		data.Overview = overview
		data.Agents = agents
		data.Configs = configs
		data.Tasks = tasks
		data.Settings = settings
		data.TaskRetryReasons = map[string]string{}
		data.EnrollmentTokens = []core.EnrollmentToken{{ID: "tok_used0000000001", Name: "shanghai-bootstrap", ExpiresAt: now.Add(10 * time.Minute), MaxUses: 1, UsedCount: 1, CreatedAt: now.Add(-2 * time.Hour)}}
		var sb strings.Builder
		if err := server.templates.ExecuteTemplate(&sb, "app", data); err != nil {
			t.Fatalf("render %s: %v", active, err)
		}
		return renderInline(sb.String())
	}

	// 登录页
	{
		var sb strings.Builder
		if err := server.templates.ExecuteTemplate(&sb, "login", pageData{Title: "登录", Settings: settings}); err != nil {
			t.Fatalf("render login: %v", err)
		}
		writeSnapshot(t, "login", renderInline(sb.String()))
	}

	// 总览
	writeSnapshot(t, "dashboard", renderApp("dashboard", "总览", pageData{}))

	// 节点与独立客户端配置页
	// 为 alpha 节点注入一份含凭据的已部署配置，使节点入口和独立页面的
	// 展示型密码框都进入渲染路径，便于审阅视觉与表单语义。
	clientInput := serverconfig.Input{
		Protocol: serverconfig.ProtocolTrojan, Tag: "snap-trojan-client", Listen: "0.0.0.0", Port: 24443,
		Username: "relay-user", Credential: "snap-secret-pass", Transport: "raw", TLSEnabled: true,
		CertificatePath: "/server-only/certificate.pem", PrivateKeyPath: "/server-only/private-key.pem",
	}
	clientContent, err := serverconfig.Generate(core.EngineXray, clientInput)
	if err != nil {
		t.Fatalf("generate client config: %v", err)
	}
	clientAccess := server.clientAccessFor(agents[0], core.EngineXray, clientContent)
	if len(clientAccess.Profiles) == 0 {
		t.Fatal("snapshot client access produced no profiles")
	}
	writeSnapshot(t, "agents", renderApp("agents", "节点", pageData{
		SelectedAgentID: "agt_node_alpha",
		ClientAccess:    map[string]clientAccessData{deploymentKey("agt_node_alpha", core.EngineXray): clientAccess},
	}))
	clientAccessMap := make(map[string]clientAccessData)
	for _, engine := range agents[0].Capabilities {
		clientAccessMap[deploymentKey(agents[0].ID, engine)] = clientAccess
	}
	clientAccessPage := clientAccessPageFor(agents, clientAccessMap, "", "", "")
	writeSnapshot(t, "client-access", renderApp("client-access", "客户端配置", pageData{
		ClientAccessPage: &clientAccessPage,
	}))

	// 手动配置（live-config）
	writeSnapshot(t, "live-config", renderApp("live-config", "手动配置", pageData{
		LiveConfigPage: &liveConfigPageData{
			Agent:            agents[0],
			Engine:           core.EngineMihomo,
			Runtime:          agents[0].Runtime[core.EngineMihomo],
			InstalledEngines: []core.Engine{core.EngineMihomo, core.EngineXray},
			Config:           core.Config{ID: "cfg_node_mihomo", Name: "shanghai-edge-01", Engine: core.EngineMihomo, Version: 32, Content: defaultConfig().Content + "\n# 已从节点读取\n"},
			HasSavedConfig:   true,
			SourceLoaded:     true,
			ReturnTo:         "/configs?node=agt_node_alpha&engine=mihomo",
		},
	}))

	// 配置档案
	writeSnapshot(t, "configs", renderApp("configs", "配置档案", pageData{
		FormConfig:   configs[0],
		IsNewConfig:  false,
		DeployAgents: []core.Agent{agents[0]},
		ConfigRevisions: []core.Config{
			{ID: "cfg_mihomo_01", Name: "edge-mihomo", Engine: core.EngineMihomo, Version: 1, UpdatedAt: now.Add(-10 * 24 * time.Hour)},
			{ID: "cfg_mihomo_01", Name: "edge-mihomo", Engine: core.EngineMihomo, Version: 2, UpdatedAt: now.Add(-3 * 24 * time.Hour)},
			configs[0],
		},
	}))

	// 执行记录
	writeSnapshot(t, "tasks", renderApp("tasks", "执行记录", pageData{}))

	// 设置
	writeSnapshot(t, "settings", renderApp("settings", "系统设置", pageData{}))
}
