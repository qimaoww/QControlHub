package webui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestTemplatesParseRenderAndEscapeDynamicContent(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatalf("New() template parse error = %v", err)
	}
	for _, name := range []string{"login", "app", "agents-page", "agent-config-page", "settings-page"} {
		if server.templates.Lookup(name) == nil {
			t.Fatalf("parsed templates do not contain %q", name)
		}
	}

	const untrusted = `<script>alert("xss")</script>`
	var login bytes.Buffer
	if err := server.templates.ExecuteTemplate(&login, "login", pageData{Error: untrusted}); err != nil {
		t.Fatalf("execute login template: %v", err)
	}
	if strings.Contains(login.String(), untrusted) || !strings.Contains(login.String(), "&lt;script&gt;") {
		t.Fatalf("login template did not HTML-escape dynamic content: %s", login.String())
	}
	for _, expected := range []string{`class="login-shell compact-login"`, `class="brand login-card-brand"`, "<h1>登录</h1>", "<meta name=\"description\" content=\"登录 ·", "/assets/app.css?v=" + appCSSCacheVersion} {
		if !strings.Contains(login.String(), expected) {
			t.Errorf("compact login does not contain %q", expected)
		}
	}
	for _, removed := range []string{`class="login-story"`, "让每一次远程变更", "签名任务", "欢迎回来", "管理入口", "管理员登录", "HttpOnly 会话"} {
		if strings.Contains(login.String(), removed) {
			t.Errorf("compact login still contains %q", removed)
		}
	}

	var app bytes.Buffer
	data := pageData{
		Title:       untrusted,
		Active:      "configs",
		CSRF:        "csrf-token",
		IsNewConfig: true,
		FormConfig: core.Config{
			Name:    "test config",
			Engine:  core.EngineMihomo,
			Content: "mixed-port: 7890\n",
		},
	}
	if err := server.templates.ExecuteTemplate(&app, "app", data); err != nil {
		t.Fatalf("execute app template: %v", err)
	}
	if strings.Contains(app.String(), untrusted) || !strings.Contains(app.String(), "&lt;script&gt;") {
		t.Fatal("app template did not HTML-escape dynamic content")
	}
}

func TestConfigRevisionHistoryRendersPreviewAndRestore(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	current := core.Config{ID: "cfg_current", Name: "current", Engine: core.EngineXray, Content: "{}", Version: 3}
	preview := core.Config{ID: current.ID, Name: "older", Engine: current.Engine, Content: `{"log":{"loglevel":"warning"}}`, Version: 1, UpdatedAt: time.Now()}
	var output bytes.Buffer
	err = server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "配置档案", Active: "configs", CSRF: "csrf-token", FormConfig: current, Role: core.RoleAdmin,
		ConfigRevisions: []core.Config{current, preview}, RevisionPreview: preview, HasRevisionPreview: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"配置修订历史", "v1 配置正文", "以此版本创建新版本", "/ui/configs/cfg_current/revisions/1/restore", "/configs/archive?id=cfg_current", `name="expected_version" value="3"`, `data-confirm-action="deploy"`, "部署并重启"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("configuration revision UI does not contain %q", expected)
		}
	}
}

func TestConfigWorkspaceDoesNotRenderNodeFileReadControls(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "配置档案", Active: "configs", CSRF: "csrf-token", IsNewConfig: true, Role: core.RoleAdmin,
		FormConfig: core.Config{Name: "edge · Xray 当前配置", Engine: core.EngineXray, Content: `{"inbounds":[],"outbounds":[]}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`id="config-editor"`, `name="content"`, `&#34;inbounds&#34;`, "新建配置档案", "创建配置档案",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("configuration current-file workspace does not contain %q", expected)
		}
	}
	for _, unexpected := range []string{`action="/ui/configs/read-current"`, `class="node-config-source config-file-source`, "读取节点当前配置文件"} {
		if strings.Contains(output.String(), unexpected) {
			t.Errorf("configuration workspace unexpectedly contains %q", unexpected)
		}
	}
}

func TestLiveConfigTemplateRendersRawNodeFileWithoutStructuredEditor(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "手动配置", Active: "live-config",
		LiveConfigPage: &liveConfigPageData{
			Agent:  core.Agent{ID: "agt_edge", Name: "edge", Status: "online", OS: "linux", Arch: "amd64"},
			Engine: core.EngineXray, Runtime: core.RuntimeState{Installed: true, Version: "v26.7.28"},
			InstalledEngines: []core.Engine{core.EngineXray},
			Config:           core.Config{Name: "edge · Xray 当前配置", Engine: core.EngineXray, Content: `{"inbounds":[],"outbounds":[]}`},
			HasSavedConfig:   true, SourceLoaded: true, ReturnTo: "/configs?node=agt_edge&engine=xray",
		},
	}); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"edge · Xray", "config.json", `data-code-editor`, `data-line-numbers`, `data-code-bytes`, `data-code-position`, `data-code-reset`, `data-code-max-bytes="2097152"`, `aria-label="Xray 节点配置源码"`, `class="code-editor-input"`, "校验修改", "保存并部署", "v26.7.28", `name="mode" value="source"`, `action="/ui/agents/agt_edge/config/xray/save"`} {
		if !strings.Contains(html, expected) {
			t.Errorf("live configuration target page does not contain %q", expected)
		}
	}
	for _, unexpected := range []string{"新配置", "创建配置档案", "入站协议", "客户端接入", "新增入站", "字段编辑"} {
		if strings.Contains(html, unexpected) {
			t.Errorf("live configuration target page unexpectedly contains %q", unexpected)
		}
	}
}

func TestLiveConfigAutomaticallyReadsCurrentFileWithoutButton(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "手动配置", Active: "live-config", CSRF: "csrf-token",
		LiveConfigPage: &liveConfigPageData{
			Agent: core.Agent{ID: "agt_edge", Name: "edge", Status: "online"}, Engine: core.EngineXray,
			Runtime: core.RuntimeState{Installed: true}, ReturnTo: "/configs?node=agt_edge&engine=xray",
		},
	}); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"正在读取配置", `data-auto-read-current`, `name="action" value="read-config"`, `name="automatic_read" value="1"`, `name="return_to" value="/configs?node=agt_edge&amp;engine=xray"`} {
		if !strings.Contains(html, expected) {
			t.Errorf("live configuration auto-read state does not contain %q", expected)
		}
	}
	for _, unexpected := range []string{">读取</button>", ">重新读取</button>", "入站协议", "客户端接入"} {
		if strings.Contains(html, unexpected) {
			t.Errorf("live configuration auto-read state unexpectedly contains %q", unexpected)
		}
	}
}

func TestLiveConfigAgentsRequireOnlineInstalledCore(t *testing.T) {
	t.Parallel()
	agents := []core.Agent{
		{ID: "offline", Status: "offline", Capabilities: []core.Engine{core.EngineMihomo}, Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}}},
		{ID: "missing", Status: "online", Capabilities: []core.Engine{core.EngineXray}, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {}}},
		{ID: "ready", Status: "online", Capabilities: []core.Engine{core.EngineXray, core.EngineMihomo}, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true}, core.EngineMihomo: {Installed: true}}},
	}
	agent, engine, found := selectLiveConfigTarget(agents, "", "xray")
	if !found || agent.ID != "ready" || engine != core.EngineXray {
		t.Fatalf("selectLiveConfigTarget() = %s/%s/%t, want ready/xray/true", agent.ID, engine, found)
	}
	if _, _, found := selectLiveConfigTarget(agents, "missing", ""); found {
		t.Fatal("selectLiveConfigTarget() selected an agent without an installed core")
	}
}

func TestAgentConfigRevisionHistoryRendersPreviewAndRestore(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := configschema.CatalogFor(core.EngineXray)
	if err != nil {
		t.Fatal(err)
	}
	protocol, ok := serverconfig.FindProtocol(core.EngineXray, serverconfig.ProtocolVMess)
	if !ok {
		t.Fatal("VMess protocol is missing")
	}
	current := core.Config{ID: "cfg_node", AgentID: "agt_node", Name: "node xray", Engine: core.EngineXray, Content: `{"inbounds":[],"outbounds":[]}`, Version: 4}
	preview := current
	preview.Version = 2
	preview.Content = `{"log":{"loglevel":"warning"},"inbounds":[],"outbounds":[]}`
	preview.UpdatedAt = time.Now()
	var output bytes.Buffer
	err = server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "节点配置", Active: "agent-config", CSRF: "csrf-token",
		AgentConfigPage: &agentConfigPageData{
			Agent:  core.Agent{ID: current.AgentID, Name: "edge", Capabilities: []core.Engine{core.EngineXray}},
			Config: current, HasSavedConfig: true, Catalog: catalog, Selected: catalog.Fields[0],
			Server: serverBuilderData{Selected: protocol}, Revisions: []core.Config{current, preview},
			RevisionPreview: preview, HasRevisionPreview: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"节点配置历史", "v2 节点配置正文", "以此版本创建新版本",
		"/ui/configs/cfg_node/revisions/2/restore",
		`/agents/agt_node/config/xray?protocol=vmess`, `revision=2#revisions`,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("node revision UI does not contain %q", expected)
		}
	}
}

func TestAgentConfigSourceDraftRequiresValidatedIncrementalSubmission(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := configschema.CatalogFor(core.EngineMihomo)
	if err != nil {
		t.Fatal(err)
	}
	protocol, ok := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolSS2022)
	if !ok {
		t.Fatal("Mihomo SS2022 protocol is missing")
	}
	config := core.Config{
		ID: "cfg_node", AgentID: "agt_node", Name: "node current file", Engine: core.EngineMihomo, Version: 8,
		Content: "future-option: keep\nlisteners:\n  - name: managed\n    type: shadowsocks\n    port: 8388\n",
	}
	var output bytes.Buffer
	err = server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "节点配置", Active: "agent-config", CSRF: "csrf-token",
		AgentConfigPage: &agentConfigPageData{
			Agent: core.Agent{
				ID: config.AgentID, Name: "edge", Status: "online", Capabilities: []core.Engine{core.EngineMihomo},
				Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true, ServiceStatus: "active"}},
			},
			Config: config, HasSavedConfig: true, Catalog: catalog, Selected: catalog.Fields[0],
			Server: serverBuilderData{
				Selected: protocol, Input: serverconfig.Input{Protocol: protocol.Key}, Editing: true, Mutation: "modify", OriginalTag: "managed",
				Inbounds: []serverconfig.Input{{Protocol: protocol.Key, Tag: "managed", Listen: "0.0.0.0", Port: 8388}, {Protocol: protocol.Key, Tag: "secondary", Listen: "127.0.0.1", Port: 8389}},
			},
			SourceDraft: true, SourceTaskID: "tsk_read_current",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{
		`class="node-config-source config-command-bar loaded"`, "已读取",
		`name="source_task" value="tsk_read_current"`, `class="advanced-studio" id="advanced"`,
		`class="source-studio"`,
		`class="config-hierarchy-menu"`, "切换入站 / 协议",
		`class="config-command-selectors"`, `data-builder-workbench`, `data-builder-step="listen"`, "参数菜单", "＋ 新增", "secondary",
		"regenerate=1&source_task=tsk_read_current", "inbound=managed&source_task=tsk_read_current",
		"新增入站", "修改 · managed", "删除 · managed", "保存并校验", "保存源码并校验",
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("node current configuration editor does not contain %q", expected)
		}
	}
	for _, redundant := range []string{"节点当前文件已载入，并通过真实", "其他入站、未知字段和全局设置保持不变", "正在编辑当前入站", "一次编辑一个分组", "来自节点当前文件，可直接修改", "只修改所选入站"} {
		if strings.Contains(html, redundant) {
			t.Errorf("node current configuration editor still contains redundant copy %q", redundant)
		}
	}
	if strings.Contains(html, `name="intent" value="save"`) {
		t.Fatal("node configuration editor still permits an unvalidated save")
	}
	if strings.Contains(html, `id="advanced" open`) || strings.Contains(html, `class="source-studio" open`) {
		t.Fatal("secondary source editors should remain collapsed until requested")
	}
	if strings.Contains(html, `data-auto-read-current`) || strings.Contains(html, `name="action" value="read-config"`) {
		t.Fatal("loaded node configuration still renders a current-file read control")
	}
}

func TestAgentConfigAutomaticallyReadsCurrentFileWithoutButton(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := configschema.CatalogFor(core.EngineXray)
	if err != nil {
		t.Fatal(err)
	}
	protocol, _ := serverconfig.FindProtocol(core.EngineXray, serverconfig.ProtocolVMess)
	var output bytes.Buffer
	err = server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "节点配置", Active: "agent-config", CSRF: "csrf-token",
		AgentConfigPage: &agentConfigPageData{
			Agent:  core.Agent{ID: "agt_node", Name: "edge", Status: "online", Capabilities: []core.Engine{core.EngineXray}, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true}}},
			Config: defaultAgentConfig(core.Agent{ID: "agt_node", Name: "edge"}, core.EngineXray), Catalog: catalog, Selected: catalog.Fields[0], Server: serverBuilderData{Selected: protocol},
			ReturnTo: "/agents/agt_node/config/xray?inbound=second&protocol=vmess",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{
		"读取中", `data-auto-read-current`, `name="action" value="read-config"`,
		`name="automatic_read" value="1"`,
		`name="return_to" value="/agents/agt_node/config/xray?inbound=second&amp;protocol=vmess"`,
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("automatic current configuration loader does not contain %q", expected)
		}
	}
	if strings.Contains(html, "正在读取并校验节点当前文件") {
		t.Fatal("automatic current configuration loader still duplicates its status")
	}
	if strings.Contains(html, ">读取并校验当前文件</button>") || strings.Contains(html, ">重新读取并校验</button>") {
		t.Fatal("automatic current configuration loader still renders a read button")
	}
}

func TestAgentConfigReturnToPreservesEditingContextOnly(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet,
		"/agents/agt_node/config/mihomo?protocol=ss2022&inbound=second&client_host=edge.example.com&client_sni=tls.example.com&regenerate=1&revision=3&notice=done&error=bad&task=tsk_focus&source_task=tsk_source", nil)
	got := agentConfigReturnTo(request)
	for _, expected := range []string{"protocol=ss2022", "inbound=second", "regenerate=1", "revision=3"} {
		if !strings.Contains(got, expected) {
			t.Errorf("agent config return URL %q does not contain %q", got, expected)
		}
	}
	for _, transient := range []string{"notice=", "error=", "task=", "source_task=", "client_host=", "client_sni="} {
		if strings.Contains(got, transient) {
			t.Errorf("agent config return URL %q retained %q", got, transient)
		}
	}
}

func TestCurrentConfigSourceDestinationKeepsContextAndChoosesEditor(t *testing.T) {
	t.Parallel()
	if got := currentConfigSourceDestination("/configs?node=agt_edge&engine=xray", "tsk_recent"); got != "/configs?engine=xray&node=agt_edge&source_task=tsk_recent#config-editor" {
		t.Fatalf("live configuration destination = %q", got)
	}
	got := currentConfigSourceDestination("/agents/agt_edge/config/mihomo?protocol=ss2022&inbound=managed", "tsk_recent")
	for _, expected := range []string{"/agents/agt_edge/config/mihomo?", "protocol=ss2022", "inbound=managed", "source_task=tsk_recent"} {
		if !strings.Contains(got, expected) {
			t.Errorf("agent configuration destination %q does not contain %q", got, expected)
		}
	}
	if strings.Contains(got, "#advanced") {
		t.Fatalf("node preset destination should stay at the primary editor: %q", got)
	}
}

func TestTaskCoreVersionKeepsUserFacingValidationMessage(t *testing.T) {
	t.Parallel()
	for name, form := range map[string]string{
		"unknown source": "release_channel=unknown",
		"invalid custom": "release_channel=custom&custom_version=not-a-version",
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/ui/tasks", strings.NewReader(form))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if _, err := taskCoreVersion(request); err == nil || strings.Contains(err.Error(), "invalid input") {
				t.Fatalf("taskCoreVersion error = %v", err)
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "/ui/tasks", strings.NewReader("release_channel=custom&custom_version=v1.19.29"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	if version, err := taskCoreVersion(request); err != nil || version != "1.19.29" {
		t.Fatalf("taskCoreVersion() = %q, %v", version, err)
	}
}

func TestTaskPageRendersFiltersFocusRefreshAndMobileLabels(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	focused := core.Task{
		ID: "tsk_focused", AgentID: "agt_edge", Action: core.ActionValidate,
		Engine: core.EngineMihomo, Status: core.TaskSucceeded, Attempt: 1, Output: "validation ok", Error: "remote warning", CreatedAt: time.Now(),
	}
	pending := core.Task{
		ID: "tsk_pending", AgentID: "agt_edge", Action: core.ActionStart,
		Engine: core.EngineMihomo, Status: core.TaskPending, CreatedAt: time.Now(),
	}
	emptyTerminal := core.Task{
		ID: "tsk_empty", AgentID: "agt_edge", Action: core.ActionStatus,
		Engine: core.EngineMihomo, Status: core.TaskSucceeded, Simulated: true, Attempt: 1, CreatedAt: time.Now(),
	}
	missingAgent := core.Task{
		ID: "tsk_missing_agent", AgentID: "agt_deleted", Action: core.ActionRestart,
		Engine: core.EngineMihomo, Status: core.TaskFailed, Attempt: 1, CreatedAt: time.Now(),
	}
	missingConfig := core.Task{
		ID: "tsk_missing_config", AgentID: "agt_edge", ConfigID: "cfg_deleted", Action: core.ActionValidate,
		Engine: core.EngineMihomo, Status: core.TaskCanceled, CreatedAt: time.Now(),
	}
	retryable := core.Task{
		ID: "tsk_retryable", AgentID: "agt_edge", ConfigID: "cfg_live", Action: core.ActionValidate,
		Engine: core.EngineMihomo, Status: core.TaskFailed, Attempt: 1, CreatedAt: time.Now(),
	}
	retryableService := core.Task{
		ID: "tsk_retryable_service", AgentID: "agt_edge", Action: core.ActionStatus,
		Engine: core.EngineMihomo, Status: core.TaskFailed, Attempt: 1, CreatedAt: time.Now(),
	}
	agents := []core.Agent{{ID: "agt_edge", Name: "edge-node"}}
	configs := []core.Config{{ID: "cfg_live", AgentID: "agt_edge", Engine: core.EngineMihomo}}
	tasks := []core.Task{focused, pending, emptyTerminal, missingAgent, missingConfig, retryable, retryableService}
	data := pageData{
		Title: "执行记录", Active: "tasks", CSRF: "csrf-token", Notice: "操作正在执行",
		Agents: agents, Configs: configs, Tasks: tasks,
		FocusTaskID: focused.ID, TaskAgentFilter: "agt_edge", TaskStatusFilter: core.TaskPending,
		TaskActionFilter: core.ActionStart, TaskLimit: 500, TaskRetryReasons: taskRetryReasons(tasks, agents, map[string]bool{"cfg_live": true}),
	}
	var output bytes.Buffer
	if err := server.templates.ExecuteTemplate(&output, "app", data); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`data-task-page`, `data-task-refresh-status`, `class="button small task-refresh-link" href="/tasks?agent_id=agt_edge`,
		`data-task-feedback="tsk_focused"`, `data-task-feedback-message`, `data-task-feedback-link`, `aria-busy="true"`, `data-task-active-count`,
		`value="agt_edge" selected`, `value="pending" selected`, `value="start" selected`, `value="500" selected`,
		`id="task-tsk_focused" class="audit-event task-event focused"`, `<details open>`, `class="event-result"`,
		`class="task-lifecycle"`, `data-copy-target="#task-error-tsk_focused"`, `id="task-error-tsk_focused"`,
		`data-copy-target="#task-output-tsk_focused"`, `id="task-output-tsk_focused"`, `>复制</button>`,
		`/ui/tasks/tsk_pending/cancel`, `data-confirm="确定取消这个准备中的任务`, `data-confirm-label="取消任务"`, `尚未开始`,
		`data-task-id="tsk_pending"`, `data-live-task-label`, `data-live-task-attempt`, `data-live-task-timing`, `data-live-task-result`,
		`data-task-simulated="1"`, `>模拟完成</span>`,
		`源节点已移除，无法重试`, `源配置已删除，无法重试`, `/ui/tasks/tsk_retryable/retry`,
		`/ui/tasks/tsk_retryable_service/retry`, `>重试任务</button>`,
		`class="task-diagnostic"`, `节点操作执行失败`, `>已取消</a>`,
		`data-task-load-more`, `data-task-visible-count`, `data-task-load-more-button`, `再显示 20 条`,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("task page does not contain %q", expected)
		}
	}
	for _, unexpected := range []string{
		`/ui/tasks/tsk_missing_agent/retry`, `/ui/tasks/tsk_missing_config/retry`,
	} {
		if strings.Contains(output.String(), unexpected) {
			t.Errorf("task page unexpectedly contains unavailable retry action %q", unexpected)
		}
	}

	olderFocused := focused
	olderFocused.ID = "tsk_focused_older"
	olderFocused.CreatedAt = focused.CreatedAt.Add(-time.Minute)
	var dashboard bytes.Buffer
	if err := server.templates.ExecuteTemplate(&dashboard, "app", pageData{Title: "总览", Active: "dashboard", Overview: core.Overview{NodeConfigs: 3, TasksPending: 5, TasksQueued: 3, TasksRunning: 2}, Tasks: []core.Task{focused, olderFocused}}); err != nil {
		t.Fatal(err)
	}
	directLink := `/tasks?task=tsk_focused#task-tsk_focused`
	if !strings.Contains(dashboard.String(), directLink) {
		t.Errorf("dashboard task link does not target the task row: %s", directLink)
	}
	if !strings.Contains(dashboard.String(), "连续 2 次") {
		t.Error("dashboard did not collapse the repeated task activity")
	}
	for _, expected := range []string{"等待节点接入", "等待节点连接", "运行总览", `aria-label="运行概览快捷入口"`, `href="/agents" aria-label="查看在线节点"`, `href="/configs" aria-label="打开节点实际配置"`, `href="/tasks?status=pending" aria-label="查看活动任务"`, `href="/tasks?status=failed"`, "在线节点", "节点配置", "活动任务", "失败任务"} {
		if !strings.Contains(dashboard.String(), expected) {
			t.Errorf("empty dashboard does not contain %q", expected)
		}
	}
	for _, redundant := range []string{"45 秒内收到心跳", "0 个独立档案", "3 个准备中 · 2 个执行中", "查看诊断和恢复建议"} {
		if strings.Contains(dashboard.String(), redundant) {
			t.Errorf("dashboard still contains redundant copy %q", redundant)
		}
	}
}

func TestAgentConfigTemplateRendersCatalogAndEscapesNodeData(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := configschema.CatalogFor(core.EngineMihomo)
	if err != nil {
		t.Fatal(err)
	}
	const untrusted = `<img src=x onerror=alert(1)>`
	config := core.Config{AgentID: "agt_0123456789abcdef", Name: untrusted, Engine: core.EngineMihomo, Content: "mixed-port: 7890\n", Version: 2}
	var output bytes.Buffer
	err = server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "节点配置", Active: "agent-config", CSRF: "csrf",
		AgentConfigPage: &agentConfigPageData{
			Agent:  core.Agent{ID: config.AgentID, Name: untrusted, Capabilities: []core.Engine{core.EngineMihomo}},
			Config: config, HasSavedConfig: true, Catalog: catalog, Selected: catalog.Fields[4],
			Fields: []agentConfigFieldView{{Field: catalog.Fields[4], Present: true, Selected: true}}, Fragment: "7890", FieldPresent: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), untrusted) || !strings.Contains(output.String(), "&lt;img") {
		t.Fatal("agent configuration template did not escape dynamic node data")
	}
	for _, expected := range []string{`class="protocol-browser config-selector"`, "全局字段与源码", "官方文档", "保存并部署", "data-confirm-submit", "mixed-port", "/assets/agent-config.js?v=" + agentConfigJSCacheVersion} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("agent configuration page does not contain %q", expected)
		}
	}
	for _, forbidden := range []string{"客户端接入", "生成客户端接入参数", `name="client_host"`, `name="client_sni"`} {
		if strings.Contains(output.String(), forbidden) {
			t.Errorf("agent configuration page still contains %q", forbidden)
		}
	}
}

func TestSubmittedServerBuilderKeepsValuesWhenValidationFails(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("TLS form", func(t *testing.T) {
		selected, ok := serverconfig.FindProtocol(core.EngineXray, serverconfig.ProtocolVMess)
		if !ok {
			t.Fatal("VMess protocol is missing")
		}
		form := url.Values{
			"protocol":            {selected.Key},
			"version":             {"0"},
			"tag":                 {"submitted-vmess"},
			"listen":              {"0.0.0.0"},
			"port":                {"70000"},
			"username":            {"submitted-user"},
			"credential":          {"123e4567-e89b-42d3-a456-426614174000"},
			"transport":           {"websocket"},
			"transport_path":      {"/submitted-websocket"},
			"tls_enabled":         {"1"},
			"certificate_path":    {"/submitted/tls/server.crt"},
			"private_key_path":    {"/submitted/tls/server.key"},
			"reality_enabled":     {"0"},
			"reality_private_key": {""},
			"reality_public_key":  {""},
			"reality_short_id":    {""},
			"reality_server_name": {""},
		}
		request := submittedServerRequest(t, form)
		builder := submittedServerBuilder(request, core.EngineXray, selected)
		if _, err := serverconfig.Generate(core.EngineXray, builder.Input); err == nil {
			t.Fatal("out-of-range submitted port unexpectedly generated a configuration")
		}
		output := renderSubmittedServerBuilder(t, server, core.EngineXray, builder, "监听端口必须在 1 到 65535 之间")
		for _, expected := range []string{
			"监听端口必须在 1 到 65535 之间", `value="70000"`, `value="submitted-vmess"`,
			`value="123e4567-e89b-42d3-a456-426614174000"`, `value="/submitted-websocket"`,
			`value="/submitted/tls/server.crt"`, `value="/submitted/tls/server.key"`, "仅目标内核服务组可读",
			`type="password" name="credential"`, `data-secret-visibility`,
		} {
			if !strings.Contains(output, expected) {
				t.Errorf("validation response lost %q", expected)
			}
		}
	})

	t.Run("Reality form", func(t *testing.T) {
		selected, ok := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolVLESS)
		if !ok {
			t.Fatal("VLESS protocol is missing")
		}
		form := url.Values{
			"protocol":            {selected.Key},
			"version":             {"0"},
			"tag":                 {"submitted-reality"},
			"listen":              {"0.0.0.0"},
			"port":                {"24443"},
			"username":            {"reality-user"},
			"credential":          {"123e4567-e89b-42d3-a456-426614174001"},
			"flow":                {"xtls-rprx-vision"},
			"transport":           {"raw"},
			"reality_enabled":     {"1"},
			"reality_private_key": {"submitted-private-key"},
			"reality_public_key":  {"submitted-public-key"},
			"reality_short_id":    {"0123456789abcdef"},
			"reality_server_name": {"localhost"},
			"tls_enabled":         {"0"},
			"certificate_path":    {""},
			"private_key_path":    {""},
		}
		request := submittedServerRequest(t, form)
		builder := submittedServerBuilder(request, core.EngineMihomo, selected)
		_, probeErr := serverconfig.ProbeRealityTarget(request.Context(), builder.Input.RealityServerName)
		if probeErr == nil {
			t.Fatal("invalid submitted Reality target unexpectedly passed probing")
		}
		output := renderSubmittedServerBuilder(t, server, core.EngineMihomo, builder, probeErr.Error())
		for _, expected := range []string{
			"Reality ServerName", `value="24443"`, `value="localhost"`,
			`value="123e4567-e89b-42d3-a456-426614174001"`, `value="0123456789abcdef"`,
			`value="submitted-public-key"`, `value="submitted-private-key"`, "拒绝 Cloudflare 与非公网地址",
		} {
			if !strings.Contains(output, expected) {
				t.Errorf("Reality probe response lost %q", expected)
			}
		}
	})
}

func TestClientAccessProfileUsesReportedInterfaceAndRendersOnDedicatedPage(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	input := serverconfig.Input{
		Protocol: serverconfig.ProtocolTrojan, Tag: "trojan-client", Listen: "0.0.0.0", Port: 24443,
		Username: "relay-user", Credential: "correct-horse-battery-staple", Transport: "raw", TLSEnabled: true,
		CertificatePath: "/server-only/certificate.pem", PrivateKeyPath: "/server-only/private-key.pem",
	}
	content, err := serverconfig.Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	agent := core.Agent{
		ID: "agt_0123456789abcdef", Name: "test-node", Status: "online", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineXray}, Labels: map[string]string{"tls_server_name": "tls.example.com"},
		Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, ServiceStatus: "active"}},
		Metrics: core.HostMetrics{NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.168.31.205"}}}},
	}
	access := server.clientAccessFor(agent, core.EngineXray, content)
	if access.Address != "192.168.31.205" || access.Source != "eth0" || len(access.Profiles) != 1 || access.Profiles[0].Profile.URI == "" {
		t.Fatalf("client access = %+v", access)
	}
	serviceKey := deploymentKey(agent.ID, core.EngineXray)
	var nodeOutput bytes.Buffer
	if err := server.templates.ExecuteTemplate(&nodeOutput, "app", pageData{
		Title: "节点", Active: "agents", CSRF: "csrf", SelectedAgentID: agent.ID, Agents: []core.Agent{agent}, Role: core.RoleAdmin,
		Deployments:       map[string]core.Deployment{serviceKey: {AgentID: agent.ID, Engine: core.EngineXray, ConfigVersion: 1}},
		DeploymentDetails: map[string]deploymentDetail{}, DeploymentStatuses: map[string]deploymentStatus{},
		ClientAccess: map[string]clientAccessData{serviceKey: access},
	}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"客户端配置", `href="/client-access?agent_id=agt_0123456789abcdef&amp;engine=xray"`, "eth0", "192.168.31.205", "1 个入站"} {
		if !strings.Contains(nodeOutput.String(), expected) {
			t.Errorf("node client access link does not contain %q", expected)
		}
	}
	for _, forbidden := range []string{"逐项参数", `id="client-access-share-`, access.Profiles[0].Profile.URI} {
		if strings.Contains(nodeOutput.String(), forbidden) {
			t.Errorf("node page still embeds client profile detail %q", forbidden)
		}
	}

	clientPage := clientAccessPageFor([]core.Agent{agent}, map[string]clientAccessData{serviceKey: access}, agent.ID, core.EngineXray, "")
	var output bytes.Buffer
	if err := server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "客户端配置", Active: "client-access", CSRF: "csrf", Agents: []core.Agent{agent}, ClientAccessPage: &clientPage,
	}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"客户端配置", "Client access", "eth0", "192.168.31.205", "Trojan URI", `id="client-access-share-agt_0123456789abcdef-xray-0-0" name="client_share" type="password"`, "data-copy-value", "逐项参数", `aria-label="trojan-client 密码"`,
		// 展示型密码框现在包在语义 <form>（action="#" 让 Enter 仅做同文档片段导航，
		// 不重载）内，消除 Chrome "password field not in a form" 告警。
		`<form class="secret-value-control client-share-control" action="#">`,
		`<form class="secret-value-control" action="#"><input name="client_secret" type="password" readonly`,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("dedicated client access page does not contain %q", expected)
		}
	}
	if strings.Contains(output.String(), "来自已部署配置与 Agent") {
		t.Fatal("client access page still renders obsolete explanatory copy")
	}
	for _, forbidden := range []string{"/server-only/certificate.pem", "/server-only/private-key.pem"} {
		if strings.Contains(access.Profiles[0].Profile.URI, forbidden) {
			t.Fatalf("client URI leaked %q", forbidden)
		}
	}
}

func TestRegeneratingSelectedInboundKeepsModifyContext(t *testing.T) {
	t.Parallel()
	content := `listeners:
  - name: first
    type: shadowsocks
    listen: 0.0.0.0
    port: 20001
    cipher: 2022-blake3-aes-256-gcm
    password: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
  - name: second
    type: shadowsocks
    listen: 0.0.0.0
    port: 20002
    cipher: 2022-blake3-aes-256-gcm
    password: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
`
	builder, err := newServerBuilder(core.EngineMihomo, content, serverconfig.ProtocolSS2022, "second", true)
	if err != nil {
		t.Fatal(err)
	}
	if !builder.Editing || builder.Mutation != "modify" || builder.OriginalTag != "second" {
		t.Fatalf("regenerated selected inbound context = %+v", builder)
	}
	if builder.Input.Tag == "" || builder.Input.Credential == "" {
		t.Fatalf("regenerated selected inbound is incomplete: %+v", builder.Input)
	}

	addition, err := newServerBuilder(core.EngineMihomo, content, serverconfig.ProtocolSS2022, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if addition.Editing || addition.Mutation != "add" || addition.OriginalTag != "" {
		t.Fatalf("new inbound regeneration context = %+v", addition)
	}
}

func submittedServerRequest(t *testing.T, form url.Values) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/ui/agents/agt_0123456789abcdef/config/mihomo/save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return request
}

func renderSubmittedServerBuilder(t *testing.T, server *Server, engine core.Engine, builder serverBuilderData, pageError string) string {
	t.Helper()
	catalog, err := configschema.CatalogFor(engine)
	if err != nil {
		t.Fatal(err)
	}
	agent := core.Agent{ID: "agt_0123456789abcdef", Name: "test-node", Capabilities: []core.Engine{engine}}
	config := defaultAgentConfig(agent, engine)
	var output bytes.Buffer
	if err := server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "节点配置", Active: "agent-config", CSRF: "csrf", Error: pageError,
		AgentConfigPage: &agentConfigPageData{
			Agent: agent, Config: config, Catalog: catalog, Selected: catalog.Fields[0], Server: builder,
		},
	}); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func TestAgentFleetRendersManagedCoreVersionControls(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	serviceKey := deploymentKey("agt_0123456789abcdef", core.EngineMihomo)
	var output bytes.Buffer
	err = server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "节点", Active: "agents", CSRF: "csrf-token", Role: core.RoleAdmin, SelectedAgentID: "agt_0123456789abcdef",
		Deployments: map[string]core.Deployment{serviceKey: {
			AgentID: "agt_0123456789abcdef", Engine: core.EngineMihomo, ConfigID: "cfg_node", ConfigVersion: 2, DeployedAt: time.Now(),
		}},
		DeploymentDetails: map[string]deploymentDetail{serviceKey: {
			Protocol: "Shadowsocks 2022", Endpoint: "0.0.0.0:33238", Mode: "原生传输",
		}},
		DeploymentStatuses: map[string]deploymentStatus{serviceKey: {
			SavedConfigID: "cfg_node", SavedVersion: 3, Drift: true, DriftLabel: "已保存版本尚未部署", DriftDetail: "待部署 v3",
		}},
		Agents: []core.Agent{{
			ID: "agt_0123456789abcdef", Name: "test-node", Status: "online", OS: "linux", Arch: "amd64",
			Capabilities: []core.Engine{core.EngineMihomo},
			Runtime:      map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true, Version: "v1.19.29", ServiceStatus: "failed"}},
			Metrics: core.HostMetrics{
				CollectedAt: time.Now(), CPUAvailable: true, CPUPercent: 25,
				MemoryAvailable: true, MemoryUsedBytes: 2 << 30, MemoryTotalBytes: 4 << 30,
				DiskAvailable: true, DiskUsedBytes: 8 << 30, DiskTotalBytes: 16 << 30,
				NetworkAvailable: true, NetworkRXBPS: 1024, NetworkTXBPS: 512, NetworkRXBytes: 10 << 30, NetworkTXBytes: 5 << 30,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"节点内核", "资源监控", "节点身份", "查看配置", "部署 v3", "data-deployment-sync",
		`name="action" value="deploy"`, `name="config_id" value="cfg_node"`, "管理服务", "启动服务", "停止服务",
		"现有连接会立即中断", `data-service-action="start"`, `data-service-action="stop"`, "版本来源",
		"最新稳定版", "最新开发版", "指定版本", "切换版本", "版本切换", "升级或切换版本", `data-open-version-form`, `class="runtime-drawer version-drawer"`, `name="release_channel"`, `value="install"`, "SHA-256",
		"已部署配置", "已保存配置", "v2", "v3", "已保存版本尚未部署", "待部署 v3", "Shadowsocks 2022",
		"原生传输", "0.0.0.0:33238", "25.0%", "2.0 GiB / 4.0 GiB", "1.0 KiB/s", "累计 ↓",
		`class="machine-body"`, `class="service-canvas"`, `class="service-overview"`, `class="service-deployment"`,
		`class="service-primary-action"`, `class="service-endpoint"`, `class="engine-state bad"`, `>失败</b>`,
		`class="machine-profile node-inspector"`, `class="machine-resource-summary"`, `data-agent-metrics="agt_0123456789abcdef"`,
		`id="node-agt_0123456789abcdef" name="node-workspace" data-agent-node="agt_0123456789abcdef"`,
		`data-online-count`, `data-sync-state`, `data-confirm-dialog`, "<meta name=\"description\" content=\"", "/assets/theme.js?v=" + themeJSCacheVersion,
		`data-enrollment-panel data-has-agents="1"`, "/assets/app.css?v=" + appCSSCacheVersion, "/assets/agent-config.js?v=" + agentConfigJSCacheVersion, `class="mobile-account-menu"`,
		"/assets/node-workspace.js?v=ui-desktop-3", "/assets/metrics.js?v=ui-functional-10",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("agent fleet does not contain %q", expected)
		}
	}
	if strings.Index(output.String(), `class="service-canvas"`) > strings.Index(output.String(), `class="machine-profile node-inspector"`) {
		t.Error("agent fleet should present daily core actions before secondary node detail")
	}
	if count := strings.Count(output.String(), `href="#enrollment"`); count != 1 {
		t.Errorf("agent fleet renders %d enrollment entry links, want 1", count)
	}
}

func TestOfflineAgentDisablesOperationsButKeepsFirstInstallVisible(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "节点", Active: "agents", CSRF: "csrf-token", SelectedAgentID: "agt_offline",
		Agents: []core.Agent{{
			ID: "agt_offline", Name: "offline-node", Status: "offline", OS: "linux", Arch: "amd64",
			Capabilities: []core.Engine{core.EngineMihomo}, Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"安装内核", "首次安装前需准备安全目录", `aria-label="启动 Mihomo 服务" disabled`, `data-agent-refresh`} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("offline first-install UI does not contain %q", expected)
		}
	}
}

func TestAgentFleetDisablesServiceActionsThatCannotChangeState(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	agent := core.Agent{
		ID: "agt_service_actions", Name: "service-actions", Status: "online", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo, core.EngineXray},
		Runtime: map[core.Engine]core.RuntimeState{
			core.EngineMihomo: {Installed: true, ServiceStatus: "active"},
			core.EngineXray:   {Installed: true, ServiceStatus: "inactive"},
		},
	}
	var output bytes.Buffer
	if err := server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "节点", Active: "agents", CSRF: "csrf", Agents: []core.Agent{agent}, SelectedAgentID: agent.ID,
	}); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{
		`data-service-action="start" aria-label="启动 Mihomo 服务" disabled`,
		`data-service-action="stop" aria-label="停止 Mihomo 服务"`,
		`data-service-action="start" aria-label="启动 Xray 服务"`,
		`data-service-action="restart" aria-label="重启 Xray 服务" disabled`,
		`data-service-action="stop" aria-label="停止 Xray 服务" disabled`,
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("service action state does not contain %q", expected)
		}
	}
	if strings.Contains(html, `data-service-action="stop" aria-label="停止 Mihomo 服务" disabled`) {
		t.Fatal("active Mihomo stop action was disabled")
	}
	if strings.Contains(html, `data-service-action="start" aria-label="启动 Xray 服务" disabled`) {
		t.Fatal("inactive Xray start action was disabled")
	}
}

func TestThemeAssetsAreSelfHostedAndPersistent(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		path        string
		contentType string
		contains    []string
	}{
		{path: "/assets/theme.js?v=ui-clarity-6", contentType: "text/javascript", contains: []string{"localStorage", "prefers-color-scheme", "data-theme-toggle", "data-theme-icon"}},
		{path: "/assets/app.css?v=ui-desktop-58", contentType: "text/css", contains: []string{".desktop-app", ".app-dock", ".context-sidebar", ".workspace-shell", ".workspace-main", ".workspace-shell,.workspace-main{min-height:0}", ".machine-header", ".machine-body", ".service-canvas", ".service-endpoint", ".deployment-drift", ".service-primary-action", ".node-inspector", ".machine-resource-summary", ".task-diagnostic", ".ops-stats>a", "[data-theme=light]", ".dock-tools", ".runtime-drawer", ".version-drawer", ".release-channel-options", ".audit-live", ".task-event", ".task-lifecycle", ".task-result-block", ".revision-timeline", ".audit-query{display:grid", ".config-workspace", ".config-editor-grid", ".live-config-workspace", ".live-config-inspector", ".code-editor-frame", ".code-gutter", ".protocol-browser", ".secret-value-control", ".client-profile-drawer", ".client-parameter-menu", ".client-parameters", ".confirm-dialog", ".danger-confirm", ".fleet-engines", ".engine-state.bad", ".sync-state.inactive", ".task-feedback", ".node-config-source", ".config-file-source", ".config-mutation", ".field-mutation", "task-feedback-pulse", "Cross-page layout rhythm", "Corporate Trust v30", "Control settings v31", "Mobile settings v32", "Node workspace v50", "Node configuration v36", "Node resources v37", "Node enrollment v38", "Wide configuration editor v40", "Hierarchical configuration workbench v41", "Automatic client access v43", "Copy reduction v44", "GPT Image reference v45", "GPT Image reference v46", ".builder-actions.compact", ".mobile-account-menu", ".compact-login", ".config-hierarchy-menu", ".builder-index>a.active", ".config-command-bar", ".config-command-selectors", "@media(min-width:1101px)", ".page-agent-config .builder-sections{display:grid", "repeat(auto-fit,minmax(290px,1fr))", ".service-canvas{order:1", ".task-workspace", ".settings-workspace", ".settings-savebar", ".task-load-more", "position:static", "repeat(6,minmax(0,1fr))", "[hidden]{display:none!important}", "safe-area-inset-bottom", "button:disabled", "min-height:44px", "--accent:#5755e7", "--code-canvas:#f4f5f8", "background:var(--code-canvas)", "prefers-reduced-motion", "repeat(auto-fit,minmax(min(100%,300px),1fr))", ".service-primary-action{grid-column:2;grid-row:1/3"}},
	} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", test.path, response.Code)
		}
		if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, test.contentType) {
			t.Errorf("GET %s Content-Type = %q", test.path, contentType)
		}
		for _, expected := range test.contains {
			if !strings.Contains(response.Body.String(), expected) {
				t.Errorf("GET %s does not contain %q", test.path, expected)
			}
		}
	}
}

func TestSettingsTemplateDrivesBrandAndOperationalDefaults(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	settings := core.PanelSettings{
		PanelName: "Edge Control", PanelDescription: "生产节点编排",
		EnrollmentTTLMinutes: 30, TaskPageSize: 50, TaskPollIntervalMS: 2000,
		UpdatedAt: time.Now(),
	}
	var output bytes.Buffer
	if err := server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "系统设置", Active: "settings", CSRF: "csrf-token", Settings: settings, Role: core.RoleAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{
		"Edge Control", "生产节点编排", `href="/settings" title="设置"`, `action="/ui/settings"`,
		`name="panel_name" value="Edge Control"`, `value="30" selected`, `value="50" selected`,
		`value="2000" selected`, "面板标识", "操作默认值", "状态同步", `data-confirm-submit="确定恢复系统默认设置？`, "恢复默认值", "保存设置",
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("settings page does not contain %q", expected)
		}
	}

	output.Reset()
	if err := server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "节点", Active: "agents", Settings: settings, Role: core.RoleAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `value="30" selected`) {
		t.Fatal("enrollment form did not apply the configured token lifetime")
	}

	output.Reset()
	if err := server.templates.ExecuteTemplate(&output, "app", pageData{
		Title: "执行记录", Active: "tasks", Settings: settings, TaskLimit: settings.TaskPageSize, Role: core.RoleAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`data-task-poll-ms="2000"`, `value="50" selected`} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("task page did not apply setting %q", expected)
		}
	}
}

func TestPanelSettingsFormValidation(t *testing.T) {
	t.Parallel()
	valid := url.Values{
		"panel_name":             {"  Edge Control  "},
		"panel_description":      {" production "},
		"enrollment_ttl_minutes": {"30"},
		"task_page_size":         {"50"},
		"task_poll_interval_ms":  {"1000"},
	}
	request := httptest.NewRequest(http.MethodPost, "/ui/settings", strings.NewReader(valid.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	settings, err := panelSettingsFromForm(request)
	if err != nil {
		t.Fatal(err)
	}
	if settings.PanelName != "Edge Control" || settings.PanelDescription != "production" || settings.TaskPollIntervalMS != 1000 {
		t.Fatalf("parsed settings = %+v", settings)
	}

	invalid := valid
	invalid.Set("task_page_size", "75")
	request = httptest.NewRequest(http.MethodPost, "/ui/settings", strings.NewReader(invalid.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := panelSettingsFromForm(request); err == nil {
		t.Fatal("unsupported task page size was accepted")
	}
}

func TestMetricsScriptIsSelfHostedAndContainsAuthenticatedPollingPath(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/metrics.js?v=ui-functional-10", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "/ui/agents/metrics") {
		t.Fatalf("metrics script response = %d %q", response.Code, response.Body.String())
	}
	for _, expected := range []string{"data-agent-status-label", "data-core-version", "data-agent-refresh", "last_seen_label", "刷新失败，保留上次数据", "response.status === 401", "会话已过期，正在返回登录页", "window.location.assign", "sessionExpired", "response.ok", "runtimeStatus", "serviceActionDisabled", "data-service-action", "dataset.serviceAction", "updateChrome", "data-context-agent", "data-sync-label"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("metrics script does not contain %q", expected)
		}
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/javascript") {
		t.Fatalf("metrics script Content-Type = %q", contentType)
	}
}

func TestNodeWorkspaceScriptPersistsTheOpenNodeInTheURL(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/node-workspace.js?v=ui-desktop-3", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("node workspace script status = %d", response.Code)
	}
	for _, expected := range []string{`details.machine-workspace[name="node-workspace"]`, "searchParams.set('node'", "history.replaceState", "data-enrollment-panel", "revealEnrollment", "#enrollment", "data-open-version-form", "version-drawer", "core-version-form", "release_channel", "version.disabled", "scrollIntoView"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("node workspace script does not contain %q", expected)
		}
	}
}

func TestAgentConfigScriptOpensAdvancedEditorAndBindsDependentFields(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/agent-config.js?v=ui-functional-28", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("agent config script status = %d", response.Code)
	}
	for _, expected := range []string{"#advanced", ".advanced-config, .advanced-studio", ".source-editor, .source-studio", ".builder-actions", "panel.open = true", "scrollIntoView", `select[name="transport"]`, `input[name="tls_enabled"]`, `select[name="release_channel"]`, "crypto.getRandomValues", "data-profile-editor", ".code-file-meta b", "new Event('input'", "sing-box", "data-confirm", "data-confirm-dialog", "showModal", "event.submitter", "requestSubmit", "dataset.confirmAction", "data-task-page", "new URLSearchParams", "visibilitychange", "matchMedia('(max-width: 820px)')", "filters.open = false", "dataset.taskPollMs", "setTimeout(poll, pollInterval)", "data-task-load-more", "visibleRows", "applyVisibleRows", "rows.length > 20", "replaceChildren", "payload.timing", "payload.simulated", "模拟完成", "has_result", "task-result-refresh", "document.execCommand('copy')", "请手动复制", "data-copy-value", "dataset.copyTarget", "form.dataset.submitting", "form.addEventListener('input', persist)", `a[href*="regenerate=1"]`, "sessionStorage.removeItem(keyFor(form))", "bindSecretVisibility", "data-secret-visibility", "bindTaskFeedback", "bindAutomaticCurrentConfig", "bindBuilderMenus", "data-builder-workbench", "data-builder-step", "section.hidden", "aria-selected", "addEventListener('invalid'", "event.target.focus", "bindCodeEditors", "data-line-numbers", "setAttribute('wrap', 'off')", "new Blob", "data-auto-read-current", "/ui/tasks/", "tasks_active", "data-task-active-count", "aria-busy", "source_task", "打开当前配置", "read-config", "code-editor-baseline", "data-code-position", "data-code-reset", "JSON.parse", "maxBytes", "beforeunload", "event.key === 'Tab'"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("agent config script does not contain %q", expected)
		}
	}
	if strings.Contains(response.Body.String(), "window.location.reload();\n        return") || strings.Contains(response.Body.String(), "秒后自动刷新") {
		t.Fatal("task page script should update live status without countdown reloads")
	}
}

func TestMetricsSnapshotIncludesLiveNodeAndRuntimeState(t *testing.T) {
	t.Parallel()
	agent := core.Agent{
		ID: "agt_live", Status: "online", Version: "v2.1.0", LastSeen: time.Now(),
		Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, Version: "26.7.28", ServiceStatus: "active"}},
		Metrics: core.HostMetrics{CollectedAt: time.Now(), CPUAvailable: true, CPUPercent: 12.5},
	}
	snapshot := metricsSnapshot(agent)
	if snapshot.ID != agent.ID || snapshot.Status != "online" || snapshot.Version != agent.Version || snapshot.LastSeenLabel == "" ||
		!snapshot.Runtime[core.EngineXray].Installed || snapshot.Runtime[core.EngineXray].ServiceStatus != "active" || !snapshot.Available {
		t.Fatalf("metrics snapshot = %+v", snapshot)
	}
	if got := heartbeatLabel(time.Time{}); got != "尚未心跳" {
		t.Fatalf("zero heartbeat label = %q", got)
	}
	if got := heartbeatLabel(time.Unix(0, 0).UTC()); got != "尚未心跳" {
		t.Fatalf("epoch heartbeat label = %q", got)
	}
	if got := metricsSnapshot(core.Agent{ID: "agt_never_seen"}).LastSeenLabel; got != "尚未心跳" {
		t.Fatalf("never-seen metrics heartbeat label = %q", got)
	}
}

func TestAdvancedEditorRedirectKeepsFragmentAfterNotice(t *testing.T) {
	t.Parallel()
	destination := agentConfigURL("agt_0123456789abcdef", core.EngineMihomo, "log-level")
	result := addQuery(destination, "notice", "配置已保存")
	parsed, err := url.Parse(result)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Fragment != "advanced" || parsed.Query().Get("key") != "log-level" || parsed.Query().Get("notice") != "配置已保存" {
		t.Fatalf("advanced redirect = %q", result)
	}
}

func TestRestoredNodeRevisionRedirectUsesRestoredProtocol(t *testing.T) {
	t.Parallel()
	protocol, ok := serverconfig.FindProtocol(core.EngineXray, serverconfig.ProtocolTrojan)
	if !ok {
		t.Fatal("Xray Trojan protocol is missing")
	}
	input, err := serverconfig.NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	content, err := serverconfig.Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	destination := restoredConfigReturnTo(
		"/agents/agt_0123456789abcdef/config/xray?protocol=vmess&revision=1#revisions",
		core.Config{AgentID: "agt_0123456789abcdef", Engine: core.EngineXray, Content: content, Version: 3},
	)
	destination = addQuery(destination, "revision", "3")
	parsed, err := url.Parse(destination)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("protocol") != serverconfig.ProtocolTrojan || parsed.Query().Get("revision") != "3" || parsed.Fragment != "revisions" {
		t.Fatalf("restored node redirect = %q", destination)
	}
}

func TestLoginWorksWithoutDatabaseAndSetsHardenedCookie(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token", CookieSecure: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	handler := server.Handler()

	loginPage := httptest.NewRecorder()
	handler.ServeHTTP(loginPage, httptest.NewRequest(http.MethodGet, "/login", nil))
	if loginPage.Code != http.StatusOK {
		t.Fatalf("GET /login status = %d, want %d", loginPage.Code, http.StatusOK)
	}
	for name, want := range map[string]string{
		"Content-Security-Policy":   "frame-ancestors 'none'",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	} {
		if got := loginPage.Header().Get(name); !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", name, got, want)
		}
	}

	form := url.Values{"token": {"test-admin-token"}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.0.2.1:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("POST /login = %d Location %q, want 303 Location /", response.Code, response.Header().Get("Location"))
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("POST /login set %d cookies, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookie || cookie.Value == "" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie is not hardened as expected: %#v", cookie)
	}
}

func TestLoginAndCSRFRejectQueryOnlyCredentials(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	queryLogin := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/login?token=test-admin-token", strings.NewReader(""))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.0.2.2:12345"
	handler.ServeHTTP(queryLogin, request)
	if queryLogin.Code != http.StatusSeeOther || !strings.HasPrefix(queryLogin.Header().Get("Location"), "/login?error=") || len(queryLogin.Result().Cookies()) != 0 {
		t.Fatalf("query-only login = %d Location %q cookies=%d", queryLogin.Code, queryLogin.Header().Get("Location"), len(queryLogin.Result().Cookies()))
	}

	form := url.Values{"token": {"test-admin-token"}}
	loginRequest := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRequest.RemoteAddr = "192.0.2.3:12345"
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, loginRequest)
	cookies := loginResponse.Result().Cookies()
	if loginResponse.Code != http.StatusSeeOther || len(cookies) != 1 {
		t.Fatalf("body login = %d cookies=%d", loginResponse.Code, len(cookies))
	}
	server.sessionsMu.Lock()
	csrf := server.sessions[cookies[0].Value].CSRF
	server.sessionsMu.Unlock()
	csrfRequest := httptest.NewRequest(http.MethodPost, "/ui/tasks/tsk_0123456789abcdef/cancel?csrf="+url.QueryEscape(csrf), strings.NewReader(""))
	csrfRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	csrfRequest.AddCookie(cookies[0])
	csrfResponse := httptest.NewRecorder()
	handler.ServeHTTP(csrfResponse, csrfRequest)
	if csrfResponse.Code != http.StatusForbidden {
		t.Fatalf("query-only CSRF status = %d, want %d", csrfResponse.Code, http.StatusForbidden)
	}
}

func TestProtectedUIRedirectsWithoutSessionBeforeDatabaseAccess(t *testing.T) {
	t.Parallel()
	server, err := New(nil, Config{AdminToken: "test-admin-token"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, path := range []string{"/", "/agents", "/ui/agents/metrics", "/agents/agt_0123456789abcdef/config/mihomo", "/configs", "/configs/archive", "/tasks"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login" {
				t.Fatalf("GET %s = %d Location %q, want 303 Location /login", path, response.Code, response.Header().Get("Location"))
			}
		})
	}
	jsonRequest := httptest.NewRequest(http.MethodGet, "/ui/agents/metrics", nil)
	jsonRequest.Header.Set("Accept", "application/json")
	jsonResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(jsonResponse, jsonRequest)
	if jsonResponse.Code != http.StatusUnauthorized || jsonResponse.Header().Get("Location") != "" || !strings.Contains(jsonResponse.Header().Get("Content-Type"), "application/json") || jsonResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("JSON session expiry response = %d Location %q Content-Type %q Cache-Control %q", jsonResponse.Code, jsonResponse.Header().Get("Location"), jsonResponse.Header().Get("Content-Type"), jsonResponse.Header().Get("Cache-Control"))
	}
	if !strings.Contains(jsonResponse.Body.String(), "session expired") {
		t.Fatalf("JSON session expiry body = %q", jsonResponse.Body.String())
	}
}
