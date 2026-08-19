package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/notify"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type Config struct {
	AdminToken      string
	OperatorTokens  []string
	ReadonlyTokens  []string
	AllowedOrigins  []string
	SecureTransport bool
	TrustedProxies  []*net.IPNet
	AgentBinary     []byte
	AgentInstaller  []byte
	WebhookSecret   string
	SessionTTL      time.Duration
}

type Server struct {
	store           *store.Store
	allowedOrigins  map[string]struct{}
	secureTransport bool
	adminLimiter    *authn.FailureLimiter
	enrollLimiter   *authn.FailureLimiter
	agentLimiter    *authn.FailureLimiter
	trustedProxies  []*net.IPNet
	agentBinary     []byte
	agentInstaller  []byte
	notifier        *notify.Client
	roleTokens      map[[32]byte]core.Role
	sessionsMu      sync.Mutex
	sessions        map[string]apiSession
	sessionTTL      time.Duration
	connectionsMu   sync.Mutex
	connections     map[string]liveConnection
}

type liveConnection struct {
	id     string
	cancel context.CancelFunc
}

func New(dataStore *store.Store, config Config) *Server {
	origins := make(map[string]struct{}, len(config.AllowedOrigins))
	for _, origin := range config.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			origins[origin] = struct{}{}
		}
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
		store:           dataStore,
		allowedOrigins:  origins,
		secureTransport: config.SecureTransport,
		adminLimiter:    authn.NewFailureLimiter(8, 5*time.Minute, 10*time.Minute),
		enrollLimiter:   authn.NewFailureLimiter(5, 10*time.Minute, 20*time.Minute),
		agentLimiter:    authn.NewFailureLimiter(20, time.Minute, 5*time.Minute),
		trustedProxies:  config.TrustedProxies,
		agentBinary:     config.AgentBinary,
		agentInstaller:  config.AgentInstaller,
		notifier:        notify.New(config.WebhookSecret, slog.Default()),
		roleTokens:      roleTokens,
		sessions:        make(map[string]apiSession),
		sessionTTL:      sessionTTL(config.SessionTTL),
		connections:     make(map[string]liveConnection),
	}
}

func (s *Server) enrollmentDownloadAllowed(w http.ResponseWriter, request *http.Request) bool {
	token := strings.TrimSpace(request.Header.Get("X-QControlHub-Enrollment"))
	if s.store == nil || !s.store.EnrollmentTokenUsable(request.Context(), token) {
		writeError(w, http.StatusUnauthorized, "valid enrollment token required")
		return false
	}
	return true
}

// agentBinary serves the statically-extracted agent executable to a valid
// one-time enrollment token only.
func (s *Server) serveAgentBinary(w http.ResponseWriter, r *http.Request) {
	if len(s.agentBinary) == 0 {
		http.NotFound(w, r)
		return
	}
	if !s.enrollmentDownloadAllowed(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(s.agentBinary)
}

func (s *Server) serveAgentInstaller(w http.ResponseWriter, r *http.Request) {
	if len(s.agentInstaller) == 0 {
		http.NotFound(w, r)
		return
	}
	if !s.enrollmentDownloadAllowed(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="install-agent.sh"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(s.agentInstaller)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.readiness)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("GET /api/v1/auth/session", s.session)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("GET /api/v1/agent-installer", s.serveAgentInstaller)

	mux.Handle("GET /api/v1/overview", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.overview)))
	mux.Handle("GET /api/v1/agents", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.listAgents)))
	mux.Handle("GET /api/v1/deployments", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.listDeployments)))
	mux.Handle("GET /api/v1/client-access", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.listClientAccess)))
	mux.Handle("GET /api/v1/config-catalogs/{engine}", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.configCatalog)))
	mux.Handle("DELETE /api/v1/agents/{id}", s.requireRole(core.RoleAdmin, http.HandlerFunc(s.deleteAgent)))
	mux.Handle("GET /api/v1/agents/{id}/configs", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.listAgentConfigs)))
	mux.Handle("GET /api/v1/agents/{id}/configs/{engine}", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.getAgentConfig)))
	mux.Handle("PUT /api/v1/agents/{id}/configs/{engine}", s.requireRole(core.RoleOperator, http.HandlerFunc(s.putAgentConfig)))
	mux.Handle("GET /api/v1/agents/{id}/configs/{engine}/workspace", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.agentConfigWorkspace)))
	mux.Handle("POST /api/v1/agents/{id}/configs/{engine}/plans", s.requireRole(core.RoleOperator, http.HandlerFunc(s.newServerPlan)))
	mux.Handle("POST /api/v1/agents/{id}/configs/{engine}/server-inbounds", s.requireRole(core.RoleOperator, http.HandlerFunc(s.saveServerInbound)))
	mux.Handle("GET /api/v1/agents/{id}/configs/{engine}/fields/{key}", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.getConfigField)))
	mux.Handle("POST /api/v1/agents/{id}/configs/{engine}/fields/{key}", s.requireRole(core.RoleOperator, http.HandlerFunc(s.saveConfigField)))
	mux.Handle("GET /api/v1/configs", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.listConfigs)))
	mux.Handle("POST /api/v1/configs", s.requireRole(core.RoleOperator, http.HandlerFunc(s.createConfig)))
	mux.Handle("PUT /api/v1/configs/{id}", s.requireRole(core.RoleOperator, http.HandlerFunc(s.updateConfig)))
	mux.Handle("DELETE /api/v1/configs/{id}", s.requireRole(core.RoleAdmin, http.HandlerFunc(s.deleteConfig)))
	mux.Handle("GET /api/v1/configs/{id}/revisions", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.listConfigRevisions)))
	mux.Handle("GET /api/v1/configs/{id}/revisions/{version}", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.getConfigRevision)))
	mux.Handle("POST /api/v1/configs/{id}/revisions/{version}/restore", s.requireRole(core.RoleAdmin, http.HandlerFunc(s.restoreConfigRevision)))
	mux.Handle("GET /api/v1/tasks", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.listTasks)))
	mux.Handle("POST /api/v1/tasks", s.requireRole(core.RoleOperator, http.HandlerFunc(s.createTask)))
	mux.Handle("GET /api/v1/tasks/{id}", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.getTask)))
	mux.Handle("DELETE /api/v1/tasks/{id}", s.requireRole(core.RoleOperator, http.HandlerFunc(s.cancelTask)))
	mux.Handle("POST /api/v1/tasks/{id}/retry", s.requireRole(core.RoleOperator, http.HandlerFunc(s.retryTask)))
	mux.Handle("GET /api/v1/enrollment-tokens", s.requireRole(core.RoleAdmin, http.HandlerFunc(s.listEnrollmentTokens)))
	mux.Handle("POST /api/v1/enrollment-tokens", s.requireRole(core.RoleAdmin, http.HandlerFunc(s.createEnrollmentToken)))
	mux.Handle("DELETE /api/v1/enrollment-tokens/{id}", s.requireRole(core.RoleAdmin, http.HandlerFunc(s.revokeEnrollmentToken)))
	mux.Handle("GET /api/v1/settings", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.getSettings)))
	mux.Handle("PUT /api/v1/settings", s.requireRole(core.RoleAdmin, http.HandlerFunc(s.putSettings)))
	mux.Handle("GET /api/v1/audit", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.listAudit)))
	mux.Handle("GET /api/v1/metrics/{id}", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.metricSamples)))
	mux.Handle("GET /api/v1/templates", s.requireRole(core.RoleReadonly, http.HandlerFunc(s.listTemplates)))
	mux.Handle("POST /api/v1/templates", s.requireRole(core.RoleOperator, http.HandlerFunc(s.createTemplate)))
	mux.Handle("DELETE /api/v1/templates/{id}", s.requireRole(core.RoleAdmin, http.HandlerFunc(s.deleteTemplate)))
	mux.Handle("POST /api/v1/templates/{id}/apply", s.requireRole(core.RoleOperator, http.HandlerFunc(s.applyTemplate)))

	mux.HandleFunc("GET /api/v1/agent-binary", s.serveAgentBinary)

	mux.HandleFunc("POST /agent/v1/enroll", s.enrollAgent)
	mux.Handle("GET /agent/v1/connect", s.agent(http.HandlerFunc(s.agentConnect)))

	return s.middleware(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "qcontrolhub"})
}

func (s *Server) readiness(w http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		slog.Warn("readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) overview(w http.ResponseWriter, request *http.Request) {
	result, err := s.store.Overview(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listAgents(w http.ResponseWriter, request *http.Request) {
	agents, err := s.store.ListAgents(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) deleteAgent(w http.ResponseWriter, request *http.Request) {
	agentID := request.PathValue("id")
	if err := s.store.DeleteAgent(request.Context(), agentID); err != nil {
		writeStoreError(w, err)
		return
	}
	s.DisconnectAgent(agentID)
	s.recordAudit(request, "agent.deleted", agentID, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getAgentConfig(w http.ResponseWriter, request *http.Request) {
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.store.AgentConfig(request.Context(), request.PathValue("id"), engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) putAgentConfig(w http.ResponseWriter, request *http.Request) {
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input core.Config
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Engine != "" && input.Engine != engine {
		writeError(w, http.StatusBadRequest, "configuration engine does not match the URL")
		return
	}
	input.AgentID = request.PathValue("id")
	input.Engine = engine
	config, err := s.store.SaveAgentConfig(request.Context(), input, input.Version)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) listConfigs(w http.ResponseWriter, request *http.Request) {
	configs, err := s.store.ListConfigs(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configs)
}

func (s *Server) createConfig(w http.ResponseWriter, request *http.Request) {
	var input core.Config
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.store.CreateConfig(request.Context(), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.created", config.ID, config.Name+" ("+string(config.Engine)+")")
	writeJSON(w, http.StatusCreated, config)
}

func (s *Server) updateConfig(w http.ResponseWriter, request *http.Request) {
	var input core.Config
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.store.UpdateConfig(request.Context(), request.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.updated", config.ID, "v"+strconv.Itoa(config.Version)+" "+config.Name)
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) deleteConfig(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteConfig(request.Context(), request.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.deleted", request.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listConfigRevisions(w http.ResponseWriter, request *http.Request) {
	limit := 20
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "revision limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	revisions, err := s.store.ListConfigRevisions(request.Context(), request.PathValue("id"), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, revisions)
}

func (s *Server) getConfigRevision(w http.ResponseWriter, request *http.Request) {
	version, err := strconv.Atoi(request.PathValue("version"))
	if err != nil || version < 1 {
		writeError(w, http.StatusBadRequest, "revision version must be positive")
		return
	}
	revision, err := s.store.ConfigRevision(request.Context(), request.PathValue("id"), version)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, revision)
}

func (s *Server) restoreConfigRevision(w http.ResponseWriter, request *http.Request) {
	version, err := strconv.Atoi(request.PathValue("version"))
	if err != nil || version < 1 {
		writeError(w, http.StatusBadRequest, "revision version must be positive")
		return
	}
	var input struct {
		ExpectedVersion int `json:"expected_version"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "expected_version must be positive")
		return
	}
	restored, err := s.store.RestoreConfigRevision(request.Context(), request.PathValue("id"), version, input.ExpectedVersion)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.restored", restored.ID, "v"+strconv.Itoa(version)+" -> v"+strconv.Itoa(restored.Version))
	writeJSON(w, http.StatusOK, restored)
}

func (s *Server) listTasks(w http.ResponseWriter, request *http.Request) {
	limit := 100
	if rawLimit := request.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if parsed > 500 {
			parsed = 500
		}
		limit = parsed
	}
	status := core.TaskStatus(request.URL.Query().Get("status"))
	if status != "" && !status.Valid() {
		writeError(w, http.StatusBadRequest, "invalid task status filter")
		return
	}
	action := core.Action(request.URL.Query().Get("action"))
	if action != "" && !action.Valid() {
		writeError(w, http.StatusBadRequest, "invalid task action filter")
		return
	}
	tasks, err := s.store.ListTasksFiltered(request.Context(), request.URL.Query().Get("agent_id"), status, action, limit)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) createTask(w http.ResponseWriter, request *http.Request) {
	var input core.TaskRequest
	if err := decodeJSON(w, request, &input, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task, err := s.store.CreateTask(request.Context(), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	s.recordAudit(request, "task.created", task.ID, string(task.Action)+" "+string(task.Engine)+" "+task.AgentID)
	status := http.StatusCreated
	if task.Reused {
		status = http.StatusOK
	}
	writeJSON(w, status, task)
}

func (s *Server) getTask(w http.ResponseWriter, request *http.Request) {
	task, err := s.store.GetTask(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) cancelTask(w http.ResponseWriter, request *http.Request) {
	if err := s.store.CancelTask(request.Context(), request.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "task.canceled", request.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) retryTask(w http.ResponseWriter, request *http.Request) {
	task, err := s.store.RetryTask(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	s.recordAudit(request, "task.retried", task.ID, "")
	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) listEnrollmentTokens(w http.ResponseWriter, request *http.Request) {
	tokens, err := s.store.ListEnrollmentTokens(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) createEnrollmentToken(w http.ResponseWriter, request *http.Request) {
	var input core.EnrollmentTokenRequest
	if err := decodeJSON(w, request, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.store.CreateEnrollmentToken(request.Context(), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "enrollment_token.created", created.ID, created.Name)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) revokeEnrollmentToken(w http.ResponseWriter, request *http.Request) {
	if err := s.store.RevokeEnrollmentToken(request.Context(), request.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "enrollment_token.revoked", request.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) enrollAgent(w http.ResponseWriter, request *http.Request) {
	key := authn.ClientIP(request, s.trustedProxies)
	now := time.Now().UTC()
	if !s.enrollLimiter.Allow(key, now) {
		writeError(w, http.StatusTooManyRequests, "too many enrollment attempts")
		return
	}
	var input core.EnrollRequest
	if err := decodeJSON(w, request, &input, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateEnrollment(input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	agent, err := s.store.EnrollAgent(request.Context(), input, bearerToken(request))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.enrollLimiter.Failure(key, now)
			writeError(w, http.StatusUnauthorized, "invalid, expired, or already-used enrollment token")
			return
		}
		writeStoreError(w, err)
		return
	}
	s.enrollLimiter.Success(key)
	slog.Info("agent enrolled", "agent_id", agent.ID, "name", agent.Name)
	writeJSON(w, http.StatusCreated, core.EnrollResponse{AgentID: agent.ID})
}

func (s *Server) agentConnect(w http.ResponseWriter, request *http.Request) {
	connection, err := websocket.Accept(w, request, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
		Subprotocols:    []string{"qcontrolhub.agent.v1"},
	})
	if err != nil {
		slog.Warn("accept agent websocket", "agent_id", agentID(request), "error", err)
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "connection closed")
	if connection.Subprotocol() != "qcontrolhub.agent.v1" {
		_ = connection.Close(websocket.StatusPolicyViolation, "required subprotocol was not negotiated")
		return
	}
	connection.SetReadLimit(192 << 10)

	id := agentID(request)
	ctx, cancelConnection := context.WithCancel(request.Context())
	defer cancelConnection()
	connectionID, err := core.NewID("wss")
	if err != nil {
		return
	}
	s.registerConnection(id, connectionID, cancelConnection)
	defer s.unregisterConnection(id, connectionID)
	incoming := make(chan core.WireMessage, 1)
	readErrors := make(chan error, 1)
	go func() {
		for {
			var message core.WireMessage
			if err := wsjson.Read(ctx, connection, &message); err != nil {
				readErrors <- err
				return
			}
			select {
			case incoming <- message:
			case <-ctx.Done():
				return
			}
		}
	}()

	if err := writeWire(ctx, connection, core.WireMessage{Type: core.WireHello}); err != nil {
		return
	}
	taskTicker := time.NewTicker(2 * time.Second)
	defer taskTicker.Stop()
	heartbeatDeadline := time.NewTimer(50 * time.Second)
	defer heartbeatDeadline.Stop()
	var inFlightTask string
	resumeRunning := true
	dispatchTask := func() error {
		if inFlightTask != "" {
			return nil
		}
		var task *core.Task
		var err error
		if resumeRunning {
			resumeRunning = false
			task, err = s.store.RunningTask(ctx, id)
		}
		if err == nil && task == nil {
			task, err = s.store.ClaimTask(ctx, id)
		}
		if err != nil {
			return err
		}
		if task == nil {
			return nil
		}
		if err := writeWire(ctx, connection, core.WireMessage{Type: core.WireTask, Task: task}); err != nil {
			return err
		}
		inFlightTask = task.ID
		return nil
	}
	taskReady := s.store.TaskReady(id)
	if err := dispatchTask(); err != nil {
		slog.Error("dispatch initial task for websocket", "agent_id", id, "error", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-readErrors:
			status := websocket.CloseStatus(err)
			if status != websocket.StatusNormalClosure && status != websocket.StatusGoingAway && !errors.Is(err, context.Canceled) {
				slog.Warn("agent websocket read failed", "agent_id", id, "error", err)
			}
			return
		case <-heartbeatDeadline.C:
			_ = connection.Close(websocket.StatusPolicyViolation, "heartbeat timeout")
			return
		case message := <-incoming:
			if !heartbeatDeadline.Stop() {
				select {
				case <-heartbeatDeadline.C:
				default:
				}
			}
			heartbeatDeadline.Reset(50 * time.Second)
			switch message.Type {
			case core.WireHeartbeat:
				if message.Heartbeat == nil {
					_ = connection.Close(websocket.StatusPolicyViolation, "invalid heartbeat")
					return
				}
				if err := s.store.Heartbeat(ctx, id, *message.Heartbeat); err != nil {
					slog.Error("store agent heartbeat", "agent_id", id, "error", err)
					return
				}
			case core.WireResult:
				if message.Result == nil || message.Result.TaskID == "" || message.Result.TaskID != inFlightTask {
					_ = connection.Close(websocket.StatusPolicyViolation, "unexpected task result")
					return
				}
				if err := s.store.CompleteTask(ctx, id, message.Result.TaskID, message.Result.Result); err != nil {
					slog.Warn("store task result", "agent_id", id, "task_id", message.Result.TaskID, "error", err)
					_ = connection.Close(websocket.StatusPolicyViolation, "task result rejected")
					return
				}
				s.notifyTaskFailure(id, message.Result.TaskID, message.Result.Result)
				if err := writeWire(ctx, connection, core.WireMessage{Type: core.WireResultAck, TaskID: message.Result.TaskID}); err != nil {
					return
				}
				inFlightTask = ""
				if err := dispatchTask(); err != nil {
					slog.Error("dispatch next task for websocket", "agent_id", id, "error", err)
					return
				}
			default:
				_ = connection.Close(websocket.StatusUnsupportedData, "unsupported message type")
				return
			}
		case <-taskReady:
			if err := dispatchTask(); err != nil {
				slog.Error("dispatch signaled task for websocket", "agent_id", id, "error", err)
				return
			}
		case <-taskTicker.C:
			if err := dispatchTask(); err != nil {
				slog.Error("claim fallback task for websocket", "agent_id", id, "error", err)
				return
			}
		}
	}
}

// notifyTaskFailure asynchronously delivers a task.failed webhook event when a
// webhook URL is configured. Delivery is best-effort and never blocks the
// agent connection loop.
func (s *Server) notifyTaskFailure(agentID, taskID string, result core.TaskResultRequest) {
	if result.Success {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		settings, err := s.store.PanelSettings(ctx)
		if err != nil || strings.TrimSpace(settings.WebhookURL) == "" {
			return
		}
		task, err := s.store.GetTask(ctx, taskID)
		if err != nil {
			slog.Warn("load task for failure webhook", "task_id", taskID, "error", err)
			return
		}
		agentName, _ := s.store.AgentName(ctx, agentID)
		event := notify.TaskFailedEvent(task, agentName, result.Error)
		if err := s.notifier.Send(ctx, settings.WebhookURL, event); err != nil {
			slog.Warn("deliver task failure webhook", "task_id", taskID, "error", err)
		}
	}()
}

func (s *Server) registerConnection(agentID, connectionID string, cancel context.CancelFunc) {
	s.connectionsMu.Lock()
	previous, exists := s.connections[agentID]
	s.connections[agentID] = liveConnection{id: connectionID, cancel: cancel}
	s.connectionsMu.Unlock()
	if exists {
		previous.cancel()
	}
}

func (s *Server) unregisterConnection(agentID, connectionID string) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	current, exists := s.connections[agentID]
	if exists && current.id == connectionID {
		delete(s.connections, agentID)
	}
}

// DisconnectAgent terminates the currently authenticated WSS session after
// the corresponding identity has been revoked. New handshakes are rejected by
// the store-backed authentication middleware.
func (s *Server) DisconnectAgent(agentID string) {
	s.connectionsMu.Lock()
	connection, exists := s.connections[agentID]
	if exists {
		delete(s.connections, agentID)
	}
	s.connectionsMu.Unlock()
	if exists {
		connection.cancel()
	}
}

func writeWire(parent context.Context, connection *websocket.Conn, message core.WireMessage) error {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	return wsjson.Write(ctx, connection, message)
}

// requireRole guards the management API: the bearer token must map to a role
// with at least the requested privilege level. The admin limiter applies to
// every management call regardless of role.
func (s *Server) requireRole(minimum core.Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		key := authn.ClientIP(request, s.trustedProxies)
		now := time.Now().UTC()
		if !s.adminLimiter.Allow(key, now) {
			writeError(w, http.StatusTooManyRequests, "too many authentication failures")
			return
		}
		role, ok := s.sessionRole(request)
		if !ok {
			s.adminLimiter.Failure(key, now)
			w.Header().Set("WWW-Authenticate", `Bearer realm="QControlHub admin API"`)
			writeError(w, http.StatusUnauthorized, "invalid admin token")
			return
		}
		s.adminLimiter.Success(key)
		if !role.AtLeast(minimum) {
			writeError(w, http.StatusForbidden, "token role does not permit this operation")
			return
		}
		if bearerToken(request) == "" && request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions {
			value, sessionOK := s.sessionForRequest(request)
			if !sessionOK || !constantEqual(request.Header.Get(csrfHeader), value.CSRF) {
				writeError(w, http.StatusForbidden, "missing or invalid CSRF token")
				return
			}
		}
		w.Header().Set("X-QControlHub-Role", string(role))
		next.ServeHTTP(w, request)
	})
}

func (s *Server) roleForToken(token string) (core.Role, bool) {
	role, ok := s.roleTokens[sha256.Sum256([]byte(token))]
	return role, ok
}

func (s *Server) agent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		id := agentID(request)
		key := authn.ClientIP(request, s.trustedProxies)
		if validAgentID(id) {
			key += ":" + id
		}
		now := time.Now().UTC()
		if !s.agentLimiter.Allow(key, now) {
			writeError(w, http.StatusTooManyRequests, "too many failed agent authentication attempts")
			return
		}
		if !validAgentID(id) || authn.ValidateRequestHeaders(request, now) != nil {
			s.agentLimiter.Failure(key, now)
			writeError(w, http.StatusUnauthorized, "invalid signed agent request")
			return
		}
		publicKey, err := s.store.AgentPublicKey(request.Context(), id)
		if err != nil {
			s.agentLimiter.Failure(key, now)
			writeError(w, http.StatusUnauthorized, "invalid signed agent request")
			return
		}
		request.Body = http.MaxBytesReader(w, request.Body, 192<<10)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			s.agentLimiter.Failure(key, now)
			writeError(w, http.StatusBadRequest, "request body is too large")
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		nonce, err := authn.VerifyRequest(request, body, publicKey, now)
		if err != nil {
			s.agentLimiter.Failure(key, now)
			slog.Warn("agent signature rejected", "agent_id", id, "error", err)
			writeError(w, http.StatusUnauthorized, "invalid signed agent request")
			return
		}
		if err := s.store.RecordNonce(request.Context(), id, nonce, time.Now().UTC().Add(2*authn.MaxClockSkew)); err != nil {
			if errors.Is(err, store.ErrReplay) {
				slog.Warn("replayed agent request rejected", "agent_id", id)
				writeError(w, http.StatusUnauthorized, "replayed signed request")
				return
			}
			writeInternalError(w, err)
			return
		}
		s.agentLimiter.Success(key)
		next.ServeHTTP(w, request)
	})
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		started := time.Now()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if request.URL.Path != "/healthz" {
			w.Header().Set("Cache-Control", "no-store")
		}
		if s.secureTransport {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		origin := request.Header.Get("Origin")
		if origin != "" {
			if _, allowed := s.allowedOrigins[origin]; allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-QControlHub-CSRF, X-QControlHub-Agent-ID, X-QControlHub-Timestamp, X-QControlHub-Nonce, X-QControlHub-Signature")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			} else if request.Method == http.MethodOptions {
				writeError(w, http.StatusForbidden, "origin is not allowed")
				return
			}
		}
		if request.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, request)
		slog.Debug("http request", "method", request.Method, "path", request.URL.Path, "duration", time.Since(started))
	})
}

func validateEnrollment(input core.EnrollRequest) error {
	if name := strings.TrimSpace(input.Name); name == "" || utf8.RuneCountInString(name) > 100 {
		return errors.New("agent name is required and must not exceed 100 characters")
	}
	if utf8.RuneCountInString(input.Version) > 100 {
		return errors.New("agent version must not exceed 100 characters")
	}
	if strings.TrimSpace(input.OS) == "" || strings.TrimSpace(input.Arch) == "" || utf8.RuneCountInString(input.OS) > 50 || utf8.RuneCountInString(input.Arch) > 50 {
		return errors.New("agent OS and architecture are required")
	}
	if len(input.Capabilities) == 0 || len(input.Capabilities) > 4 {
		return errors.New("agent must declare between one and four capabilities")
	}
	seen := make(map[core.Engine]struct{}, len(input.Capabilities))
	for _, engine := range input.Capabilities {
		if !engine.Valid() {
			return fmt.Errorf("unsupported capability %q", engine)
		}
		if _, exists := seen[engine]; exists {
			return fmt.Errorf("duplicate capability %q", engine)
		}
		seen[engine] = struct{}{}
	}
	if len(input.Labels) > 16 {
		return errors.New("an agent may have at most 16 labels")
	}
	for key, value := range input.Labels {
		if strings.TrimSpace(key) == "" || utf8.RuneCountInString(key) > 64 || utf8.RuneCountInString(value) > 128 {
			return errors.New("agent label is too long")
		}
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(input.PublicKey)
	if err != nil || len(publicKey) != 32 {
		return errors.New("agent must provide a valid Ed25519 public key")
	}
	return nil
}

func bearerToken(request *http.Request) string {
	value := request.Header.Get("Authorization")
	prefix, token, found := strings.Cut(value, " ")
	if !found || !strings.EqualFold(prefix, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func agentID(request *http.Request) string {
	return strings.TrimSpace(request.Header.Get(authn.HeaderAgentID))
}

func validAgentID(value string) bool {
	if len(value) != 20 || !strings.HasPrefix(value, "agt_") {
		return false
	}
	for _, character := range value[4:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func constantTokenMatch(token string, expected [32]byte) bool {
	if token == "" {
		return false
	}
	actual := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
}

func decodeJSON(w http.ResponseWriter, request *http.Request, destination any, limit int64) error {
	request.Body = http.MaxBytesReader(w, request.Body, limit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON value")
	}
	return nil
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeInternalError(w, err)
	}
}

func writeInternalError(w http.ResponseWriter, err error) {
	slog.Error("HTTP request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("encode HTTP response", "error", err)
	}
}
