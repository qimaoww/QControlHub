package webui

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/notify"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

const (
	sessionCookie    = "__Host-qcontrolhub_session"
	devSessionCookie = "qcontrolhub_session_dev"
)

type Config struct {
	AdminToken      string
	OperatorTokens  []string
	ReadonlyTokens  []string
	CookieSecure    bool
	TrustedProxies  []*net.IPNet
	DisconnectAgent func(string)
	WebhookSecret   string
}

type session struct {
	CSRF             string
	ExpiresAt        time.Time
	EnrollmentSecret string
	FlashNotice      string
	Role             core.Role
}

// staticAsset holds the raw and pre-gzipped bytes of an immutable asset so that
// compression is done once at startup rather than on every request.
type staticAsset struct {
	raw     []byte
	gzipped []byte
}

// newStaticAsset compresses content with gzip at the highest level and stores
// both forms so serveAsset can pick the right one per request.
func newStaticAsset(content string) staticAsset {
	raw := []byte(content)
	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = gz.Write(raw)
	_ = gz.Close()
	return staticAsset{raw: raw, gzipped: buf.Bytes()}
}

type Server struct {
	store           *store.Store
	cookieSecure    bool
	templates       *template.Template
	sessionsMu      sync.Mutex
	sessions        map[string]session
	loginLimiter    *authn.FailureLimiter
	trustedProxies  []*net.IPNet
	disconnectAgent func(string)
	notifier        *notify.Client
	roleTokens      map[[32]byte]core.Role
	// clientAccessMu guards clientAccessCache, a bounded digest-keyed cache
	// of parsed client connection profiles (configurations are immutable).
	clientAccessMu    sync.RWMutex
	clientAccessCache map[string]clientAccessData
	// Pre-compressed static assets (computed once in New).
	cssAsset             staticAsset
	themeJSAsset         staticAsset
	metricsJSAsset       staticAsset
	nodeWorkspaceJSAsset staticAsset
	agentConfigJSAsset   staticAsset
}

type pageData struct {
	Title              string
	Active             string
	Role               core.Role
	CSRF               string
	Notice             string
	Error              string
	Overview           core.Overview
	Agents             []core.Agent
	Configs            []core.Config
	Tasks              []core.Task
	Deployments        map[string]core.Deployment
	DeploymentDetails  map[string]deploymentDetail
	DeploymentStatuses map[string]deploymentStatus
	ClientAccess       map[string]clientAccessData
	ConfigRevisions    []core.Config
	RevisionPreview    core.Config
	HasRevisionPreview bool
	EnrollmentTokens   []core.EnrollmentToken
	EnrollmentSecret   string
	SelectedAgentID    string
	FocusTaskID        string
	TaskAgentFilter    string
	TaskStatusFilter   core.TaskStatus
	TaskActionFilter   core.Action
	TaskLimit          int
	TaskRetryReasons   map[string]string
	Settings           core.PanelSettings
	DeployAgents       []core.Agent
	FormConfig         core.Config
	IsNewConfig        bool
	AgentConfigPage    *agentConfigPageData
	LiveConfigPage     *liveConfigPageData
	ClientAccessPage   *clientAccessPageData
	MetricHistory      []core.MetricSample
	ConfigDiffs        map[string]string
	AuditLogs          []core.AuditLogEntry
	Templates          []core.ConfigTemplate
}

func New(dataStore *store.Store, config Config) (*Server, error) {
	functions := template.FuncMap{
		"ago":                  timeAgo,
		"heartbeat":            heartbeatLabel,
		"clock":                formatTime,
		"taskTiming":           taskTiming,
		"taskDiagnostic":       diagnoseTask,
		"short":                shortID,
		"statusClass":          statusClass,
		"statusName":           statusName,
		"actionName":           actionName,
		"engineName":           engineName,
		"engineVersion":        engineVersion,
		"displayEngineVersion": displayEngineVersion,
		"agentSupports":        agentSupports,
		"deploymentKey":        deploymentKey,
		"tokenUsable":          tokenUsable,
		"hasMetrics":           hasMetrics,
		"dataSize":             formatDataSize,
		"dataRate":             formatDataRate,
		"metricPct":            formatPercent,
		"usagePct":             usagePercent,
		"panelName":            panelName,
		"panelDescription":     panelDescription,
		"enrollmentTTL":        enrollmentTTL,
		"taskPageSize":         taskPageSize,
		"taskPollInterval":     taskPollInterval,
		"taskActivity":         recentTaskActivity,
		"trafficChart":         trafficChart,
		"auditActionName":      auditActionName,
		"roleAtLeast":          roleAtLeast,
		"roleName":             roleName,
	}
	parsed, err := template.New("qcontrolhub").Funcs(functions).Parse(pageTemplates + agentFleetTemplate + clientAccessTemplate + agentConfigTemplate + configWorkbenchTemplate + taskAuditTemplate + settingsTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse web templates: %w", err)
	}
	roleTokens := map[[32]byte]core.Role{sha256.Sum256([]byte(config.AdminToken)): core.RoleAdmin}
	for _, token := range config.OperatorTokens {
		if token = strings.TrimSpace(token); token != "" {
			roleTokens[sha256.Sum256([]byte(token))] = core.RoleOperator
		}
	}
	for _, token := range config.ReadonlyTokens {
		if token = strings.TrimSpace(token); token != "" {
			roleTokens[sha256.Sum256([]byte(token))] = core.RoleReadonly
		}
	}
	return &Server{
		store:                dataStore,
		cookieSecure:         config.CookieSecure,
		templates:            parsed,
		sessions:             make(map[string]session),
		loginLimiter:         authn.NewFailureLimiter(6, 5*time.Minute, 15*time.Minute),
		trustedProxies:       config.TrustedProxies,
		disconnectAgent:      config.DisconnectAgent,
		notifier:             notify.New(config.WebhookSecret, slog.Default()),
		roleTokens:           roleTokens,
		clientAccessCache:    make(map[string]clientAccessData, 64),
		cssAsset:             newStaticAsset(desktopAppStyles),
		themeJSAsset:         newStaticAsset(colorThemeScript),
		metricsJSAsset:       newStaticAsset(agentMetricsScript),
		nodeWorkspaceJSAsset: newStaticAsset(nodeWorkspaceScript),
		agentConfigJSAsset:   newStaticAsset(agentConfigScript),
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /assets/app.css", s.styles)
	mux.HandleFunc("GET /assets/theme.js", s.themeScript)
	mux.HandleFunc("GET /assets/metrics.js", s.metricsScript)
	mux.HandleFunc("GET /assets/node-workspace.js", s.nodeWorkspaceScript)
	mux.HandleFunc("GET /assets/agent-config.js", s.agentConfigScript)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.Handle("POST /logout", s.auth(http.HandlerFunc(s.logout)))
	mux.Handle("GET /{$}", s.auth(http.HandlerFunc(s.dashboard)))
	mux.Handle("GET /agents", s.auth(http.HandlerFunc(s.agents)))
	mux.Handle("GET /client-access", s.auth(http.HandlerFunc(s.clientAccess)))
	mux.Handle("GET /ui/agents/metrics", s.auth(http.HandlerFunc(s.agentMetrics)))
	mux.Handle("GET /agents/{id}/config/{engine}", s.auth(http.HandlerFunc(s.agentConfigPage)))
	mux.Handle("GET /configs", s.auth(http.HandlerFunc(s.configs)))
	mux.Handle("GET /configs/archive", s.auth(http.HandlerFunc(s.configArchives)))
	mux.Handle("GET /tasks", s.auth(http.HandlerFunc(s.tasks)))
	mux.Handle("GET /settings", s.auth(http.HandlerFunc(s.settings)))
	mux.Handle("GET /ui/tasks/{id}/status", s.auth(http.HandlerFunc(s.taskStatus)))
	mux.Handle("POST /ui/configs/save", s.auth(http.HandlerFunc(s.saveConfig)))
	mux.Handle("POST /ui/configs/{id}/delete", s.auth(http.HandlerFunc(s.deleteConfig)))
	mux.Handle("POST /ui/configs/{id}/revisions/{version}/restore", s.auth(http.HandlerFunc(s.restoreConfigRevision)))
	mux.Handle("POST /ui/tasks", s.auth(http.HandlerFunc(s.createTask)))
	mux.Handle("POST /ui/tasks/batch", s.auth(http.HandlerFunc(s.batchTasks)))
	mux.Handle("POST /ui/tasks/{id}/cancel", s.auth(http.HandlerFunc(s.cancelTask)))
	mux.Handle("POST /ui/tasks/{id}/retry", s.auth(http.HandlerFunc(s.retryTask)))
	mux.Handle("POST /ui/agents/{id}/delete", s.auth(http.HandlerFunc(s.deleteAgent)))
	mux.Handle("POST /ui/agents/{id}/config/{engine}/save", s.auth(http.HandlerFunc(s.saveAgentConfig)))
	mux.Handle("POST /ui/enrollment-tokens", s.auth(http.HandlerFunc(s.createEnrollmentToken)))
	mux.Handle("POST /ui/enrollment-tokens/{id}/revoke", s.auth(http.HandlerFunc(s.revokeEnrollmentToken)))
	mux.Handle("POST /ui/settings", s.auth(http.HandlerFunc(s.saveSettings)))
	mux.Handle("POST /ui/templates", s.auth(http.HandlerFunc(s.createTemplate)))
	mux.Handle("POST /ui/templates/{id}/delete", s.auth(http.HandlerFunc(s.deleteTemplate)))
	mux.Handle("POST /ui/templates/{id}/apply", s.auth(http.HandlerFunc(s.applyTemplate)))
	return s.securityHeaders(mux)
}

// serveAsset writes a pre-compressed static asset with a one-year immutable
// cache lifetime. It negotiates gzip encoding based on Accept-Encoding so that
// browsers without gzip support still receive the raw bytes.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, contentType string, asset staticAsset) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Vary", "Accept-Encoding")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(asset.gzipped)
	} else {
		_, _ = w.Write(asset.raw)
	}
}

func (s *Server) styles(w http.ResponseWriter, r *http.Request) {
	s.serveAsset(w, r, "text/css; charset=utf-8", s.cssAsset)
}

func (s *Server) loginPage(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.currentSession(request); ok {
		http.Redirect(w, request, "/", http.StatusSeeOther)
		return
	}
	settings, err := s.panelSettings(request.Context())
	if err != nil {
		s.renderDatabaseError(w, err)
		return
	}
	_ = s.renderTemplate(w, "login", pageData{Title: "登录", Error: request.URL.Query().Get("error"), Settings: settings})
}

func (s *Server) login(w http.ResponseWriter, request *http.Request) {
	if !sameOriginLogin(request) {
		http.Error(w, "cross-site login request rejected", http.StatusForbidden)
		return
	}
	key := authn.ClientIP(request, s.trustedProxies)
	now := time.Now().UTC()
	if !s.loginLimiter.Allow(key, now) {
		http.Redirect(w, request, "/login?error="+url.QueryEscape("失败次数过多，请稍后再试"), http.StatusSeeOther)
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, 8<<10)
	if err := request.ParseForm(); err != nil {
		http.Redirect(w, request, "/login?error="+url.QueryEscape("登录请求无效"), http.StatusSeeOther)
		return
	}
	actual := sha256.Sum256([]byte(request.PostFormValue("token")))
	role, roleOK := s.roleTokens[actual]
	if !roleOK {
		s.loginLimiter.Failure(key, now)
		time.Sleep(250 * time.Millisecond)
		slog.Warn("web login rejected", "remote", key)
		s.recordAudit(request, "login.failed", "", "管理令牌不正确")
		http.Redirect(w, request, "/login?error="+url.QueryEscape("管理令牌不正确"), http.StatusSeeOther)
		return
	}
	s.loginLimiter.Success(key)
	s.recordAudit(request, "login.succeeded", "", string(role))
	s.revokeRequestSessions(request)
	sessionToken, err := core.NewToken()
	if err != nil {
		http.Error(w, "cannot create session", http.StatusInternalServerError)
		return
	}
	csrf, err := core.NewToken()
	if err != nil {
		http.Error(w, "cannot create session", http.StatusInternalServerError)
		return
	}
	expires := time.Now().UTC().Add(12 * time.Hour)
	s.sessionsMu.Lock()
	s.pruneSessionsLocked(time.Now().UTC())
	s.sessions[sessionToken] = session{CSRF: csrf, ExpiresAt: expires, Role: role}
	s.sessionsMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    sessionToken,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int((12 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, request, "/", http.StatusSeeOther)
}

func sameOriginLogin(request *http.Request) bool {
	if fetchSite := strings.ToLower(strings.TrimSpace(request.Header.Get("Sec-Fetch-Site"))); fetchSite != "" && fetchSite != "same-origin" && fetchSite != "none" {
		return false
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return false
	}
	return strings.EqualFold(parsed.Host, request.Host)
}

func (s *Server) logout(w http.ResponseWriter, request *http.Request) {
	_, ok := s.requireCSRF(w, request)
	if !ok {
		return
	}
	s.revokeRequestSessions(request)
	for _, name := range []string{sessionCookie, devSessionCookie} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Path: "/", MaxAge: -1, HttpOnly: true,
			Secure: name == sessionCookie, SameSite: http.SameSiteStrictMode,
		})
	}
	http.Redirect(w, request, "/login", http.StatusSeeOther)
}

func (s *Server) dashboard(w http.ResponseWriter, request *http.Request) {
	s.renderPage(w, request, "dashboard", "总览", nil, "")
}

func (s *Server) agents(w http.ResponseWriter, request *http.Request) {
	s.renderPage(w, request, "agents", "节点", nil, "")
}

func (s *Server) clientAccess(w http.ResponseWriter, request *http.Request) {
	s.renderPage(w, request, "client-access", "客户端配置", nil, "")
}

func (s *Server) configs(w http.ResponseWriter, request *http.Request) {
	s.renderPage(w, request, "live-config", "手动配置", nil, "")
}

func (s *Server) configArchives(w http.ResponseWriter, request *http.Request) {
	s.renderPage(w, request, "configs", "配置档案", nil, "")
}

func installedConfigEngines(agent core.Agent) []core.Engine {
	engines := make([]core.Engine, 0, len(agent.Capabilities))
	for _, engine := range agent.Capabilities {
		if agent.Runtime[engine].Installed {
			engines = append(engines, engine)
		}
	}
	return engines
}

func (s *Server) tasks(w http.ResponseWriter, request *http.Request) {
	s.renderPage(w, request, "tasks", "执行记录", nil, "")
}

func (s *Server) settings(w http.ResponseWriter, request *http.Request) {
	s.renderPage(w, request, "settings", "系统设置", nil, "")
}

func (s *Server) saveConfig(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireRoleWithLimit(w, request, core.RoleOperator, core.MaxConfigEnvelopeBytes); !ok {
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, core.MaxConfigEnvelopeBytes)
	if err := request.ParseForm(); err != nil {
		s.renderPage(w, request, "configs", "配置档案", nil, "提交内容太大或格式无效")
		return
	}
	engine, err := core.ParseEngine(request.PostFormValue("engine"))
	input := &core.Config{
		ID:          strings.TrimSpace(request.PostFormValue("id")),
		Name:        strings.TrimSpace(request.PostFormValue("name")),
		Description: strings.TrimSpace(request.PostFormValue("description")),
		Engine:      engine,
		Content:     request.PostFormValue("content"),
	}
	if err != nil {
		s.renderPage(w, request, "configs", "配置档案", input, "不支持的配置内核")
		return
	}
	if input.ID == "" {
		created, createErr := s.store.CreateConfig(request.Context(), *input)
		if createErr != nil {
			s.renderPage(w, request, "configs", "配置档案", input, createErr.Error())
			return
		}
		s.recordAudit(request, "config.created", created.ID, created.Name+" ("+string(created.Engine)+")")
		redirectNotice(w, request, "/configs/archive?id="+url.QueryEscape(created.ID), "配置档案已创建")
		return
	}
	input.Version, err = strconv.Atoi(request.PostFormValue("version"))
	if err != nil || input.Version < 1 {
		s.renderPage(w, request, "configs", "配置档案", input, "配置版本无效，请刷新页面后重试")
		return
	}
	updated, updateErr := s.store.UpdateConfig(request.Context(), input.ID, *input)
	if updateErr != nil {
		s.renderPage(w, request, "configs", "配置档案", input, updateErr.Error())
		return
	}
	s.recordAudit(request, "config.updated", updated.ID, "v"+strconv.Itoa(updated.Version)+" "+updated.Name)
	redirectNotice(w, request, "/configs/archive?id="+url.QueryEscape(updated.ID), "配置已保存为 v"+strconv.Itoa(updated.Version))
}

func (s *Server) deleteConfig(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireCSRF(w, request); !ok {
		return
	}
	if err := s.store.DeleteConfig(request.Context(), request.PathValue("id")); err != nil {
		redirectError(w, request, "/configs/archive", err.Error())
		return
	}
	s.recordAudit(request, "config.deleted", request.PathValue("id"), "")
	redirectNotice(w, request, "/configs/archive?new=1", "配置档案已删除")
}

func (s *Server) restoreConfigRevision(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireCSRF(w, request); !ok {
		return
	}
	returnTo := safeReturnTo(request.PostFormValue("return_to"))
	revisionVersion, revisionErr := strconv.Atoi(request.PathValue("version"))
	expectedVersion, expectedErr := strconv.Atoi(request.PostFormValue("expected_version"))
	if revisionErr != nil || revisionVersion < 1 || expectedErr != nil || expectedVersion < 1 {
		redirectError(w, request, returnTo, "修订号无效，请刷新页面后重试")
		return
	}
	restored, err := s.store.RestoreConfigRevision(request.Context(), request.PathValue("id"), revisionVersion, expectedVersion)
	if err != nil {
		redirectError(w, request, returnTo, err.Error())
		return
	}
	s.recordAudit(request, "config.restored", restored.ID, "v"+strconv.Itoa(revisionVersion)+" → v"+strconv.Itoa(restored.Version))
	returnTo = restoredConfigReturnTo(returnTo, restored)
	returnTo = addQuery(returnTo, "revision", strconv.Itoa(restored.Version))
	redirectNotice(w, request, returnTo, "已用 v"+strconv.Itoa(revisionVersion)+" 的内容创建 v"+strconv.Itoa(restored.Version))
}

func (s *Server) createTask(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireRole(w, request, core.RoleOperator); !ok {
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, 32<<10)
	if err := request.ParseForm(); err != nil {
		redirectError(w, request, "/tasks", "任务请求无效")
		return
	}
	engine, err := core.ParseEngine(request.PostFormValue("engine"))
	if err != nil {
		redirectError(w, request, safeReturnTo(request.PostFormValue("return_to")), "不支持的内核")
		return
	}
	action := core.Action(request.PostFormValue("action"))
	returnTo := safeReturnTo(request.PostFormValue("return_to"))
	if action == core.ActionReadConfig && request.PostFormValue("automatic_read") == "1" {
		agentID := strings.TrimSpace(request.PostFormValue("agent_id"))
		recent, recentErr := s.store.RecentReadTask(request.Context(), agentID, engine, 15*time.Second)
		if recentErr != nil && !errors.Is(recentErr, store.ErrNotFound) {
			s.renderDatabaseError(w, recentErr)
			return
		}
		if recentErr == nil {
			if _, snapshotErr := s.store.ReadTaskConfigSnapshot(request.Context(), recent.ID, agentID, engine); snapshotErr == nil {
				http.Redirect(w, request, currentConfigSourceDestination(returnTo, recent.ID), http.StatusSeeOther)
				return
			} else if !errors.Is(snapshotErr, store.ErrNotFound) {
				s.renderDatabaseError(w, snapshotErr)
				return
			}
		}
	}
	coreVersion := ""
	if action == core.ActionInstall {
		coreVersion, err = taskCoreVersion(request)
		if err != nil {
			redirectError(w, request, safeReturnTo(request.PostFormValue("return_to")), err.Error())
			return
		}
	}
	task, err := s.store.CreateTask(request.Context(), core.TaskRequest{
		AgentID:     strings.TrimSpace(request.PostFormValue("agent_id")),
		Action:      action,
		Engine:      engine,
		ConfigID:    strings.TrimSpace(request.PostFormValue("config_id")),
		CoreVersion: strings.TrimSpace(coreVersion),
	})
	if err != nil {
		redirectError(w, request, returnTo, err.Error())
		return
	}
	notice := actionName(action) + "正在执行"
	if action == core.ActionReadConfig {
		notice = "正在打开当前配置"
	} else if task.Reused {
		notice = actionName(action) + "已在执行，页面将继续同步结果"
	}
	s.recordAudit(request, "task.created", task.ID, string(action)+" "+string(engine)+" "+task.AgentID)
	redirectNotice(w, request, addQuery(returnTo, "task", task.ID), notice)
}

// batchTasks submits one service task per selected node. It is the backing
// handler for the fleet-level batch toolbar on the agents page.
func (s *Server) batchTasks(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireRole(w, request, core.RoleOperator); !ok {
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, 32<<10)
	if err := request.ParseForm(); err != nil {
		redirectError(w, request, "/agents", "批量请求无效")
		return
	}
	engine, err := core.ParseEngine(request.PostFormValue("engine"))
	if err != nil {
		redirectError(w, request, "/agents", "不支持的内核")
		return
	}
	action := core.Action(request.PostFormValue("action"))
	switch action {
	case core.ActionStart, core.ActionStop, core.ActionRestart, core.ActionStatus:
	default:
		redirectError(w, request, "/agents", "批量操作只支持启动、停止、重启与查询状态")
		return
	}
	agentIDs := make([]string, 0)
	for _, item := range strings.Split(request.PostFormValue("agent_ids"), ",") {
		if item = strings.TrimSpace(item); item != "" {
			agentIDs = append(agentIDs, item)
		}
	}
	if len(agentIDs) == 0 || len(agentIDs) > 50 {
		redirectError(w, request, "/agents", "请选择 1 到 50 个节点")
		return
	}
	created := 0
	for _, agentID := range agentIDs {
		if _, createErr := s.store.CreateTask(request.Context(), core.TaskRequest{
			AgentID: agentID, Action: action, Engine: engine,
		}); createErr == nil {
			created++
		}
	}
	s.recordAudit(request, "task.batch_created", "", fmt.Sprintf("%s %s × %d", actionName(action), engine, created))
	redirectNotice(w, request, "/tasks", fmt.Sprintf("已为 %d 个节点提交%s任务（共选择 %d）", created, actionName(action), len(agentIDs)))
}

// createTemplate stores a reusable configuration template (operator+).
func (s *Server) createTemplate(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireRole(w, request, core.RoleOperator); !ok {
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, core.MaxConfigEnvelopeBytes)
	if err := request.ParseForm(); err != nil {
		redirectError(w, request, "/configs/archive?new=1", "模板内容太大或格式无效")
		return
	}
	template, err := s.store.CreateConfigTemplate(request.Context(),
		request.PostFormValue("name"), request.PostFormValue("engine"), request.PostFormValue("content"))
	if err != nil {
		redirectError(w, request, "/configs/archive?new=1", err.Error())
		return
	}
	s.recordAudit(request, "template.created", template.ID, template.Name)
	redirectNotice(w, request, "/configs/archive?new=1", "模板已创建")
}

// deleteTemplate removes a template (operator+).
func (s *Server) deleteTemplate(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireRole(w, request, core.RoleOperator); !ok {
		return
	}
	templateID := request.PathValue("id")
	if err := s.store.DeleteConfigTemplate(request.Context(), templateID); err != nil {
		redirectError(w, request, "/configs/archive?new=1", err.Error())
		return
	}
	s.recordAudit(request, "template.deleted", templateID, "")
	redirectNotice(w, request, "/configs/archive?new=1", "模板已删除")
}

// applyTemplate renders a template with the selected node's values and saves
// the result as that node's configuration (operator+).
func (s *Server) applyTemplate(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireRole(w, request, core.RoleOperator); !ok {
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, 32<<10)
	if err := request.ParseForm(); err != nil {
		redirectError(w, request, "/configs/archive?new=1", "模板应用请求无效")
		return
	}
	templateID := request.PathValue("id")
	agentID := strings.TrimSpace(request.PostFormValue("agent_id"))
	if agentID == "" {
		redirectError(w, request, "/configs/archive?new=1", "请选择目标节点")
		return
	}
	template, agent, rendered, err := s.store.RenderTemplateForAgent(request.Context(), templateID, agentID)
	if err != nil {
		redirectError(w, request, "/configs/archive?new=1", err.Error())
		return
	}
	current, currentErr := s.store.AgentConfig(request.Context(), agentID, template.Engine)
	expectedVersion := 0
	if currentErr == nil {
		expectedVersion = current.Version
	} else if !errors.Is(currentErr, store.ErrNotFound) {
		s.renderDatabaseError(w, currentErr)
		return
	}
	saved, err := s.store.SaveAgentConfig(request.Context(), core.Config{
		AgentID: agentID, Name: template.Name + " · 模板", Description: "由配置模板渲染",
		Engine: template.Engine, Content: rendered,
	}, expectedVersion)
	if err != nil {
		redirectError(w, request, "/configs/archive?new=1", err.Error())
		return
	}
	s.recordAudit(request, "template.applied", template.ID, agent.Name+" "+string(template.Engine))
	redirectNotice(w, request, "/agents/"+url.PathEscape(agentID)+"/config/"+url.PathEscape(string(template.Engine)),
		"模板已应用到 "+agent.Name+" 并保存为 v"+strconv.Itoa(saved.Version))
}

func taskCoreVersion(request *http.Request) (string, error) {
	selected := ""
	switch channel := request.PostFormValue("release_channel"); channel {
	case core.CoreVersionStable, core.CoreVersionDevelopment:
		selected = channel
	case "custom":
		selected = request.PostFormValue("custom_version")
	default:
		return "", errors.New("内核版本来源无效")
	}
	return core.NormalizeCoreVersionSelector(selected)
}

func currentConfigSourceDestination(returnTo, taskID string) string {
	destination, err := url.Parse(addQuery(returnTo, "source_task", taskID))
	if err != nil {
		return returnTo
	}
	if destination.Path == "/configs" {
		destination.Fragment = "config-editor"
	} else {
		destination.Fragment = ""
	}
	return destination.String()
}

func (s *Server) cancelTask(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireRole(w, request, core.RoleOperator); !ok {
		return
	}
	taskID := request.PathValue("id")
	if err := s.store.CancelTask(request.Context(), taskID); err != nil {
		redirectError(w, request, "/tasks?task="+url.QueryEscape(taskID), err.Error())
		return
	}
	s.recordAudit(request, "task.canceled", taskID, "")
	redirectNotice(w, request, "/tasks?task="+url.QueryEscape(taskID), "任务 "+shortID(taskID)+" 已取消")
}

func (s *Server) retryTask(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireRole(w, request, core.RoleOperator); !ok {
		return
	}
	retried, err := s.store.RetryTask(request.Context(), request.PathValue("id"))
	if err != nil {
		redirectError(w, request, "/tasks", err.Error())
		return
	}
	s.recordAudit(request, "task.retried", retried.ID, "")
	redirectNotice(w, request, "/tasks?task="+url.QueryEscape(retried.ID), "任务 "+shortID(retried.ID)+" 已重新提交")
}

func (s *Server) deleteAgent(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireCSRF(w, request); !ok {
		return
	}
	agentID := request.PathValue("id")
	if err := s.store.DeleteAgent(request.Context(), agentID); err != nil {
		redirectError(w, request, "/agents", err.Error())
		return
	}
	if s.disconnectAgent != nil {
		s.disconnectAgent(agentID)
	}
	s.recordAudit(request, "agent.deleted", agentID, "")
	redirectNotice(w, request, "/agents", "节点已移除，其签名身份已永久失效")
}

func (s *Server) createEnrollmentToken(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireCSRF(w, request); !ok {
		return
	}
	settings, err := s.panelSettings(request.Context())
	if err != nil {
		s.renderDatabaseError(w, err)
		return
	}
	ttlMinutes := settings.EnrollmentTTLMinutes
	if raw := strings.TrimSpace(request.PostFormValue("ttl_minutes")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			redirectError(w, request, "/agents", "注册码有效期无效")
			return
		}
		ttlMinutes = parsed
	}
	created, err := s.store.CreateEnrollmentToken(request.Context(), core.EnrollmentTokenRequest{
		Name: strings.TrimSpace(request.PostFormValue("name")), TTLMinutes: ttlMinutes, MaxUses: 1,
	})
	if err != nil {
		redirectError(w, request, "/agents", err.Error())
		return
	}
	if !s.setEnrollmentFlash(request, created.Token, "一次性注册码已生成，请立即复制；关闭提示后不会再次显示") {
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	}
	s.recordAudit(request, "enrollment_token.created", created.ID, created.Name)
	http.Redirect(w, request, "/agents", http.StatusSeeOther)
}

func (s *Server) saveSettings(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireCSRF(w, request); !ok {
		return
	}
	settings := core.DefaultPanelSettings()
	notice := "已恢复默认设置并立即生效"
	if request.PostFormValue("action") != "reset" {
		var err error
		settings, err = panelSettingsFromForm(request)
		if err != nil {
			redirectError(w, request, "/settings", err.Error())
			return
		}
		notice = "设置已保存并立即生效"
	}
	if _, err := s.store.SavePanelSettings(request.Context(), settings); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			redirectError(w, request, "/settings", "设置内容无效，请检查后重试")
			return
		}
		s.renderDatabaseError(w, err)
		return
	}
	s.recordAudit(request, "settings.saved", "", request.PostFormValue("action"))
	redirectNotice(w, request, "/settings", notice)
}

func (s *Server) revokeEnrollmentToken(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireCSRF(w, request); !ok {
		return
	}
	if err := s.store.RevokeEnrollmentToken(request.Context(), request.PathValue("id")); err != nil {
		redirectError(w, request, "/agents", err.Error())
		return
	}
	s.recordAudit(request, "enrollment_token.revoked", request.PathValue("id"), "")
	redirectNotice(w, request, "/agents", "注册码已吊销")
}

func (s *Server) renderPage(w http.ResponseWriter, request *http.Request, active, title string, override *core.Config, pageError string) {
	current, ok := s.currentSession(request)
	if !ok {
		http.Redirect(w, request, "/login", http.StatusSeeOther)
		return
	}
	// Fetch the four independent page-level data sources concurrently.
	var (
		settings    core.PanelSettings
		configs     []core.Config
		agents      []core.Agent
		overview    core.Overview
		settingsErr error
		configsErr  error
		agentsErr   error
		overviewErr error
	)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); settings, settingsErr = s.panelSettings(request.Context()) }()
	go func() { defer wg.Done(); configs, configsErr = s.store.ListConfigs(request.Context()) }()
	go func() { defer wg.Done(); agents, agentsErr = s.store.ListAgents(request.Context()) }()
	go func() { defer wg.Done(); overview, overviewErr = s.store.Overview(request.Context()) }()
	wg.Wait()
	for _, err := range []error{settingsErr, configsErr, agentsErr, overviewErr} {
		if err != nil {
			s.renderDatabaseError(w, err)
			return
		}
	}
	sortAgentsForDisplay(agents)

	filters := taskFiltersFromRequest(request, settings.TaskPageSize)
	tasks, err := s.store.ListTasksFiltered(request.Context(), filters.AgentID, filters.Status, filters.Action, filters.Limit)
	if err != nil {
		s.renderDatabaseError(w, err)
		return
	}
	taskConfigIDs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if (task.Status == core.TaskFailed || task.Status == core.TaskCanceled) && task.ConfigID != "" {
			taskConfigIDs = append(taskConfigIDs, task.ConfigID)
		}
	}
	existingTaskConfigs, err := s.store.ExistingConfigIDs(request.Context(), taskConfigIDs)
	if err != nil {
		s.renderDatabaseError(w, err)
		return
	}

	var agentsBranch agentsPageBranch
	if active == "agents" || active == "client-access" {
		agentsBranch, err = s.loadAgentsBranch(request.Context(), active == "agents", agents, configs)
		if err != nil {
			s.renderDatabaseError(w, err)
			return
		}
	}

	data := pageData{
		Title:              title,
		Active:             active,
		Role:               current.Role,
		CSRF:               current.CSRF,
		Notice:             firstNonEmpty(current.FlashNotice, request.URL.Query().Get("notice")),
		Error:              firstNonEmpty(pageError, filters.PageError, request.URL.Query().Get("error")),
		Overview:           overview,
		Agents:             agents,
		Configs:            configs,
		Tasks:              tasks,
		Deployments:        agentsBranch.Deployments,
		DeploymentDetails:  agentsBranch.DeploymentDetails,
		DeploymentStatuses: agentsBranch.DeploymentStatuses,
		ClientAccess:       agentsBranch.ClientAccess,
		ConfigDiffs:        agentsBranch.ConfigDiffs,
		EnrollmentTokens:   agentsBranch.EnrollmentTokens,
		EnrollmentSecret:   current.EnrollmentSecret,
		SelectedAgentID:    selectedAgentForDisplay(request.URL.Query().Get("node"), agents),
		FocusTaskID:        request.URL.Query().Get("task"),
		TaskAgentFilter:    filters.AgentID,
		TaskStatusFilter:   filters.Status,
		TaskActionFilter:   filters.Action,
		TaskLimit:          filters.Limit,
		TaskRetryReasons:   taskRetryReasons(tasks, agents, existingTaskConfigs),
		Settings:           settings,
	}
	switch active {
	case "client-access":
		data.ClientAccessPage = buildClientAccessPage(request, agents, agentsBranch)
	case "agents":
		data.MetricHistory, err = s.loadSelectedMetricHistory(request.Context(), data.SelectedAgentID)
		if err != nil {
			s.renderDatabaseError(w, err)
			return
		}
	case "settings":
		data.AuditLogs, err = s.loadSettingsPageAudit(request.Context())
		if err != nil {
			s.renderDatabaseError(w, err)
			return
		}
	case "live-config":
		data.LiveConfigPage, pageError, err = s.buildLiveConfigPage(request.Context(), request, agents)
		if err != nil {
			s.renderDatabaseError(w, err)
			return
		}
		data.Error = firstNonEmpty(data.Error, pageError)
	case "configs":
		pageError, err = s.buildConfigsPage(request.Context(), request, agents, configs, override, &data)
		if err != nil {
			s.renderDatabaseError(w, err)
			return
		}
		data.Error = firstNonEmpty(data.Error, pageError)
	}
	if s.renderTemplate(w, "app", data) && active == "agents" {
		s.clearEnrollmentFlash(request)
	}
}
func configForPage(request *http.Request, configs []core.Config, override *core.Config) (core.Config, bool) {
	if override != nil {
		return *override, override.ID == ""
	}
	if request.URL.Query().Get("new") == "1" || len(configs) == 0 {
		return defaultConfig(), true
	}
	selectedID := request.URL.Query().Get("id")
	if selectedID != "" {
		for _, config := range configs {
			if config.ID == selectedID {
				return config, false
			}
		}
	}
	return configs[0], false
}

func defaultConfig() core.Config {
	return core.Config{
		Name:    "新配置",
		Engine:  core.EngineMihomo,
		Content: "mixed-port: 7890\nallow-lan: false\nmode: rule\nlog-level: info\nproxies: []\nproxy-groups: []\nrules:\n  - MATCH,DIRECT\n",
	}
}

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data pageData) bool {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("render web page", "template", name, "error", err)
		return false
	}
	return true
}

func (s *Server) panelSettings(ctx context.Context) (core.PanelSettings, error) {
	if s.store == nil {
		return core.DefaultPanelSettings(), nil
	}
	return s.store.PanelSettings(ctx)
}

func panelSettingsFromForm(request *http.Request) (core.PanelSettings, error) {
	settings := core.PanelSettings{
		PanelName:        strings.TrimSpace(request.PostFormValue("panel_name")),
		PanelDescription: strings.TrimSpace(request.PostFormValue("panel_description")),
		WebhookURL:       strings.TrimSpace(request.PostFormValue("webhook_url")),
	}
	var err error
	if settings.EnrollmentTTLMinutes, err = strconv.Atoi(request.PostFormValue("enrollment_ttl_minutes")); err != nil {
		return core.PanelSettings{}, errors.New("入网码默认有效期无效")
	}
	if settings.TaskPageSize, err = strconv.Atoi(request.PostFormValue("task_page_size")); err != nil {
		return core.PanelSettings{}, errors.New("任务默认显示数量无效")
	}
	if settings.TaskPollIntervalMS, err = strconv.Atoi(request.PostFormValue("task_poll_interval_ms")); err != nil {
		return core.PanelSettings{}, errors.New("任务刷新频率无效")
	}
	if settings.PanelName == "" {
		return core.PanelSettings{}, errors.New("面板名称不能为空")
	}
	if err := settings.Validate(); err != nil {
		if strings.Contains(err.Error(), "webhook") {
			return core.PanelSettings{}, errors.New("Webhook 地址无效：必须是完整的 http(s) URL")
		}
		return core.PanelSettings{}, errors.New("设置内容超出允许范围，请检查名称长度和选项")
	}
	return settings, nil
}

func panelName(settings core.PanelSettings) string {
	if strings.TrimSpace(settings.PanelName) == "" {
		return core.DefaultPanelSettings().PanelName
	}
	return settings.PanelName
}

func panelDescription(settings core.PanelSettings) string {
	if strings.TrimSpace(settings.PanelName) == "" && settings.PanelDescription == "" {
		return core.DefaultPanelSettings().PanelDescription
	}
	return settings.PanelDescription
}

func enrollmentTTL(settings core.PanelSettings) int {
	if settings.EnrollmentTTLMinutes == 0 {
		return core.DefaultPanelSettings().EnrollmentTTLMinutes
	}
	return settings.EnrollmentTTLMinutes
}

func taskPageSize(settings core.PanelSettings) int {
	if settings.TaskPageSize == 0 {
		return core.DefaultPanelSettings().TaskPageSize
	}
	return settings.TaskPageSize
}

func taskPollInterval(settings core.PanelSettings) int {
	if settings.TaskPollIntervalMS == 0 {
		return core.DefaultPanelSettings().TaskPollIntervalMS
	}
	return settings.TaskPollIntervalMS
}

func (s *Server) renderDatabaseError(w http.ResponseWriter, err error) {
	slog.Error("load web page data", "error", err)
	http.Error(w, "数据库暂时不可用", http.StatusInternalServerError)
}

// recordAudit appends an administrative action to the audit trail. Failures
// are logged but never propagate to the request.
func (s *Server) recordAudit(request *http.Request, action, target, detail string) {
	if s.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entry := core.AuditLogEntry{
		Actor: "admin", Action: action, Target: target, Detail: detail,
		RemoteIP: authn.ClientIP(request, s.trustedProxies),
	}
	if err := s.store.RecordAudit(ctx, entry); err != nil {
		slog.Warn("record audit log", "action", action, "error", err)
	}
}

// WatchAgentAvailability periodically checks agent heartbeats and delivers
// agent.offline / agent.online webhook events on state transitions. It runs
// until ctx is canceled, mirroring the janitor pattern in cmd/control-plane.
func (s *Server) WatchAgentAvailability(ctx context.Context) {
	const (
		checkEvery   = 30 * time.Second
		offlineAfter = 2 * time.Minute
	)
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	notified := make(map[string]bool)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkAgentAvailability(ctx, offlineAfter, notified)
		}
	}
}

func (s *Server) checkAgentAvailability(ctx context.Context, offlineAfter time.Duration, notified map[string]bool) {
	operationContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	settings, err := s.store.PanelSettings(operationContext)
	if err != nil || strings.TrimSpace(settings.WebhookURL) == "" {
		return
	}
	agents, err := s.store.ListAgents(operationContext)
	if err != nil {
		slog.Warn("list agents for availability watch", "error", err)
		return
	}
	now := time.Now()
	for _, agent := range agents {
		offline := now.Sub(agent.LastSeen) > offlineAfter
		var event *notify.Event
		switch {
		case offline && !notified[agent.ID]:
			notified[agent.ID] = true
			delivered := notify.AgentOfflineEvent(agent)
			event = &delivered
		case !offline && notified[agent.ID]:
			notified[agent.ID] = false
			delivered := notify.AgentOnlineEvent(agent)
			event = &delivered
		}
		if event == nil {
			continue
		}
		go func(agentID string, payload notify.Event) {
			if err := s.notifier.Send(operationContext, settings.WebhookURL, payload); err != nil {
				slog.Warn("deliver agent availability webhook", "agent_id", agentID, "error", err)
			}
		}(agent.ID, *event)
	}
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if _, ok := s.currentSession(request); !ok {
			if strings.Contains(strings.ToLower(request.Header.Get("Accept")), "application/json") {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("{\"error\":\"session expired\"}\n"))
				return
			}
			http.Redirect(w, request, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, request)
	})
}

func (s *Server) currentSession(request *http.Request) (session, bool) {
	cookie, err := request.Cookie(s.cookieName())
	if err != nil || cookie.Value == "" {
		return session{}, false
	}
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	current, ok := s.sessions[cookie.Value]
	if !ok || time.Now().UTC().After(current.ExpiresAt) {
		if ok {
			delete(s.sessions, cookie.Value)
		}
		return session{}, false
	}
	return current, true
}

func (s *Server) setEnrollmentFlash(request *http.Request, secret, notice string) bool {
	cookie, err := request.Cookie(s.cookieName())
	if err != nil || cookie.Value == "" {
		return false
	}
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	current, ok := s.sessions[cookie.Value]
	if !ok || time.Now().UTC().After(current.ExpiresAt) {
		return false
	}
	current.EnrollmentSecret = secret
	current.FlashNotice = notice
	s.sessions[cookie.Value] = current
	return true
}

func (s *Server) clearEnrollmentFlash(request *http.Request) {
	cookie, err := request.Cookie(s.cookieName())
	if err != nil || cookie.Value == "" {
		return
	}
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	current, ok := s.sessions[cookie.Value]
	if !ok {
		return
	}
	current.EnrollmentSecret = ""
	current.FlashNotice = ""
	s.sessions[cookie.Value] = current
}

func (s *Server) revokeRequestSessions(request *http.Request) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	for _, name := range []string{sessionCookie, devSessionCookie} {
		if cookie, err := request.Cookie(name); err == nil {
			delete(s.sessions, cookie.Value)
		}
	}
}

func (s *Server) cookieName() string {
	if s.cookieSecure {
		return sessionCookie
	}
	return devSessionCookie
}

func (s *Server) requireCSRF(w http.ResponseWriter, request *http.Request) (session, bool) {
	return s.requireCSRFWithLimit(w, request, 0)
}

// requireRole is the write-path guard for the web console: the session must
// carry at least the requested role. It also validates CSRF, so handlers use
// it exactly where they previously used requireCSRF.
func (s *Server) requireRole(w http.ResponseWriter, request *http.Request, minimum core.Role) (session, bool) {
	return s.requireRoleWithLimit(w, request, minimum, 0)
}

// requireRoleWithLimit is requireRole with a CSRF-rate-limit budget for
// large-payload form submissions.
func (s *Server) requireRoleWithLimit(w http.ResponseWriter, request *http.Request, minimum core.Role, limit int64) (session, bool) {
	current, ok := s.requireCSRFWithLimit(w, request, limit)
	if !ok {
		return session{}, false
	}
	if !current.Role.AtLeast(minimum) {
		redirectError(w, request, safeReturnTo(request.PostFormValue("return_to")), "当前账号角色无权执行此操作")
		return session{}, false
	}
	return current, true
}

func (s *Server) requireCSRFWithLimit(w http.ResponseWriter, request *http.Request, limit int64) (session, bool) {
	current, ok := s.currentSession(request)
	if !ok {
		http.Redirect(w, request, "/login", http.StatusSeeOther)
		return session{}, false
	}
	request.Body = http.MaxBytesReader(w, request.Body, limit)
	if err := request.ParseForm(); err != nil || !secureEqual(request.PostFormValue("csrf"), current.CSRF) {
		http.Error(w, "CSRF validation failed", http.StatusForbidden)
		return session{}, false
	}
	return current, true
}

func (s *Server) pruneSessionsLocked(now time.Time) {
	for token, current := range s.sessions {
		if now.After(current.ExpiresAt) {
			delete(s.sessions, token)
		}
	}
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if !strings.HasPrefix(request.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if s.cookieSecure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, request)
	})
}

func secureEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

func redirectNotice(w http.ResponseWriter, request *http.Request, destination, notice string) {
	http.Redirect(w, request, addQuery(destination, "notice", notice), http.StatusSeeOther)
}

func redirectError(w http.ResponseWriter, request *http.Request, destination, message string) {
	http.Redirect(w, request, addQuery(destination, "error", message), http.StatusSeeOther)
}

func addQuery(destination, key, value string) string {
	parsed, err := url.Parse(destination)
	if err != nil {
		return destination
	}
	query := parsed.Query()
	query.Set(key, value)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func safeReturnTo(value string) string {
	switch {
	case value == "/agents":
		return value
	case strings.HasPrefix(value, "/agents?node="):
		return value
	case value == "/tasks":
		return value
	case value == "/configs/archive":
		return value
	case strings.HasPrefix(value, "/configs/archive?"):
		return value
	case strings.HasPrefix(value, "/configs?"):
		return value
	case strings.HasPrefix(value, "/agents/") && strings.Contains(value, "/config/"):
		return value
	default:
		return "/tasks"
	}
}

func restoredConfigReturnTo(destination string, restored core.Config) string {
	if restored.AgentID == "" {
		return destination
	}
	input, ok := serverconfig.Parse(restored.Engine, restored.Content)
	if !ok || input.Protocol == "" {
		return destination
	}
	parsed, err := url.Parse(destination)
	if err != nil {
		return destination
	}
	query := parsed.Query()
	query.Set("protocol", input.Protocol)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func timeAgo(value time.Time) string {
	if value.IsZero() {
		return "从未"
	}
	delta := time.Since(value)
	if delta < 0 {
		delta = 0
	}
	switch {
	case delta < time.Minute:
		return "刚刚"
	case delta < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(delta.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(delta.Hours()/24))
	}
}

func heartbeatLabel(value time.Time) string {
	if value.IsZero() || value.Unix() <= 0 {
		return "尚未心跳"
	}
	return "心跳 " + timeAgo(value)
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.Local().Format("2006-01-02 15:04:05")
}

func shortID(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func statusClass(value any) string {
	status := fmt.Sprint(value)
	switch status {
	case "online", "succeeded", "active":
		return "ok"
	case "pending", "running", "activating", "deactivating", "dry-run":
		return "warn"
	case "offline", "failed", "inactive":
		return "bad"
	default:
		return "muted"
	}
}

func statusName(value any) string {
	switch fmt.Sprint(value) {
	case "online":
		return "在线"
	case "offline":
		return "离线"
	case "pending":
		return "准备中"
	case "running":
		return "执行中"
	case "succeeded":
		return "成功"
	case "failed":
		return "失败"
	case "canceled":
		return "已取消"
	case "active":
		return "运行中"
	case "inactive":
		return "已停止"
	case "activating":
		return "启动中"
	case "deactivating":
		return "停止中"
	case "dry-run":
		return "演练模式"
	case "unknown":
		return "未知"
	default:
		return fmt.Sprint(value)
	}
}

func actionName(action core.Action) string {
	switch action {
	case core.ActionValidate:
		return "校验配置"
	case core.ActionDeploy:
		return "部署并重启"
	case core.ActionStart:
		return "启动服务"
	case core.ActionStop:
		return "停止服务"
	case core.ActionRestart:
		return "重启服务"
	case core.ActionStatus:
		return "查询状态"
	case core.ActionInstall:
		return "安装或切换内核"
	case core.ActionReadConfig:
		return "读取当前配置"
	default:
		return string(action)
	}
}

// auditActionName maps a recorded audit action to a stable Chinese label.
// Unknown actions fall back to the raw action string so new events never
// break rendering.
func auditActionName(action string) string {
	switch action {
	case "login.succeeded":
		return "登录成功"
	case "login.failed":
		return "登录失败"
	case "config.created":
		return "创建配置"
	case "config.updated":
		return "更新配置"
	case "config.deleted":
		return "删除配置"
	case "config.restored":
		return "恢复修订"
	case "agent_config.saved":
		return "保存节点配置"
	case "task.created":
		return "提交任务"
	case "task.canceled":
		return "取消任务"
	case "task.retried":
		return "重试任务"
	case "agent.deleted":
		return "移除节点"
	case "enrollment_token.created":
		return "签发入网码"
	case "enrollment_token.revoked":
		return "吊销入网码"
	case "settings.saved":
		return "更新设置"
	default:
		return action
	}
}

// roleAtLeast reports whether the session role grants the named privilege
// level; it is the template-side mirror of core.Role.AtLeast.
func roleAtLeast(role core.Role, minimum string) bool {
	return role.AtLeast(core.Role(minimum))
}

// roleName renders the Chinese label of a session role.
func roleName(role core.Role) string {
	switch role {
	case core.RoleAdmin:
		return "管理员"
	case core.RoleOperator:
		return "运维"
	case core.RoleReadonly:
		return "只读"
	default:
		return "访客"
	}
}

func engineName(engine core.Engine) string {
	switch engine {
	case core.EngineMihomo:
		return "Mihomo"
	case core.EngineXray:
		return "Xray"
	case core.EngineSingBox:
		return "sing-box"
	case core.EngineShadowsocksRust:
		return "Shadowsocks Rust"
	default:
		return string(engine)
	}
}

// displayEngineVersion renders a concise "Engine 内核 version" label for a core
// binary's banner. It is the single source of truth used both by the SSR
// templates and the realtime metrics API, so the live-poll JS overwrites the
// DOM with the same short text instead of the raw banner.
func displayEngineVersion(engine core.Engine, raw string) string {
	return engineName(engine) + " 内核 " + engineVersion(engine, raw)
}

// versionTokenRE matches a release token from a core binary's --version banner:
// numeric versions such as "v1.19.29" / "26.3.27" (with optional pre-release
// suffixes like "v1.20.0-alpha-abc123"), and pre-release tags such as
// Mihomo's dev builds "alpha-99ce79c".
var versionTokenRE = regexp.MustCompile(`\b(?:v?\d+(?:\.\d+){1,5}(?:[-.][0-9A-Za-z]+)*|(?:alpha|beta|dev|rc|pre|nightly|stable)[-]?[0-9A-Za-z]{6,})`)

// engineVersion extracts the short release version from a core binary's --version
// banner (e.g. "Mihomo Meta v1.19.29 linux amd64 with go1.26.5 ..." -> "v1.19.29",
// "Mihomo Meta alpha-99ce79c ..." -> "alpha-99ce79c", "sing-box version 1.13.16"
// -> "v1.13.16"). Build/compiler/platform/time metadata trailing the version is
// dropped to keep the version readout concise. Falls back to the raw banner
// unchanged if no version token is found.
func engineVersion(_ core.Engine, raw string) string {
	if raw == "" || raw == "unknown" {
		return raw
	}
	if m := versionTokenRE.FindString(raw); m != "" {
		if !strings.HasPrefix(m, "v") && m[0] >= '0' && m[0] <= '9' {
			m = "v" + m
		}
		return m
	}
	return raw
}

func deploymentKey(agentID string, engine core.Engine) string {
	return agentID + "|" + string(engine)
}

func tokenUsable(value core.EnrollmentToken) bool {
	return value.RevokedAt == nil && time.Now().UTC().Before(value.ExpiresAt) && value.UsedCount < value.MaxUses
}
