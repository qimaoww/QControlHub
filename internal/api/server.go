package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/geoip"
	"github.com/qimaoww/qcontrolhub/internal/komari"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
	"github.com/qimaoww/qcontrolhub/internal/notify"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func agentHasFeature(features []string, feature string) bool {
	for _, candidate := range features {
		if candidate == feature {
			return true
		}
	}
	return false
}

type tokenPrincipal struct {
	Role          core.Role
	ConfigOwnerID string
	Permissions   []core.Permission
}

type liveConnection struct {
	trafficRefresh chan struct{}
	id             string
	cancel         context.CancelFunc
}

func New(dataStore *store.Store, config Config) *Server {
	origins := make(map[string]struct{}, len(config.AllowedOrigins))
	for _, origin := range config.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			origins[origin] = struct{}{}
		}
	}
	adminTokenDigest := config.AdminTokenDigest
	if adminTokenDigest == ([32]byte{}) {
		adminTokenDigest = sha256.Sum256([]byte(config.AdminToken))
	}
	roleTokens := map[[32]byte]tokenPrincipal{adminTokenDigest: {Role: core.RoleAdmin, Permissions: core.AllPermissions()}}
	for _, token := range config.OperatorTokens {
		if token = strings.TrimSpace(token); token != "" {
			roleTokens[sha256.Sum256([]byte(token))] = tokenPrincipal{Role: core.RoleUser, ConfigOwnerID: tokenConfigOwnerID(token), Permissions: legacyOperatorPermissions()}
		}
	}
	for _, token := range config.AuditorTokens {
		if token = strings.TrimSpace(token); token != "" {
			roleTokens[sha256.Sum256([]byte(token))] = tokenPrincipal{Role: core.RoleUser, ConfigOwnerID: tokenConfigOwnerID(token), Permissions: legacyAuditorPermissions()}
		}
	}
	for _, token := range config.ReadonlyTokens {
		if token = strings.TrimSpace(token); token != "" {
			roleTokens[sha256.Sum256([]byte(token))] = tokenPrincipal{Role: core.RoleUser, ConfigOwnerID: tokenConfigOwnerID(token), Permissions: legacyReadonlyPermissions()}
		}
	}
	komariClient, komariErr := komari.New(config.KomariURL, config.KomariAPIKey, config.KomariHTTPClient)
	geoipClient := geoip.New(config.GeoIPHTTPClient)
	if komariErr != nil {
		// Keep the control plane available when an optional integration is
		// misconfigured; the endpoint reports the configuration error to an
		// operator instead of preventing unrelated node management.
		komariClient = nil
	}
	komariHTTPClient := config.KomariHTTPClient
	if komariHTTPClient == nil {
		komariHTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Server{
		store:                      dataStore,
		allowedOrigins:             origins,
		secureTransport:            config.SecureTransport,
		databaseTLSVerified:        config.DatabaseTLSVerified,
		configEncryptionConfigured: config.ConfigEncryptionConfigured,
		adminLimiter:               authn.NewFailureLimiter(8, 5*time.Minute, 10*time.Minute),
		enrollLimiter:              authn.NewFailureLimiter(5, 10*time.Minute, 20*time.Minute),
		agentLimiter:               authn.NewFailureLimiter(20, time.Minute, 5*time.Minute),
		trustedProxies:             config.TrustedProxies,
		agentBinary:                config.AgentBinary,
		agentBinaryGzip:            gzipCompress(config.AgentBinary),
		agentVersion:               strings.TrimSpace(config.AgentVersion),
		controlPlaneVersion:        strings.TrimSpace(config.ControlPlaneVersion),
		agentInstaller:             config.AgentInstaller,
		publicIPProbe:              config.PublicIPProbe,
		geoip:                      geoipClient,
		komari:                     komariClient,
		komariHTTPClient:           komariHTTPClient,
		komariConfigError:          komariErr,
		webhookSigningConfigured:   strings.TrimSpace(config.WebhookSecret) != "",
		notifier:                   notify.New(config.WebhookSecret, slog.Default()),
		subStoreHTTP:               newSubStoreHTTPClient(),
		roleTokens:                 roleTokens,
		sessions:                   make(map[string]apiSession),
		sessionTTL:                 sessionTTL(config.SessionTTL),
		connections:                make(map[string]liveConnection),
	}
}

func (s *Server) enrollmentDownloadAllowed(w http.ResponseWriter, request *http.Request) bool {
	token := strings.TrimSpace(request.Header.Get("X-QControlHub-Enrollment"))
	if s.store == nil || !s.store.EnrollmentTokenUsable(request.Context(), token) {
		writeError(w, http.StatusUnauthorized, "valid add-node credential required")
		return false
	}
	return true
}

func acceptsGzip(request *http.Request) bool {
	return strings.Contains(strings.ToLower(request.Header.Get("Accept-Encoding")), "gzip")
}

func gzipCompress(input []byte) []byte {
	if len(input) == 0 {
		return nil
	}
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, _ = writer.Write(input)
	_ = writer.Close()
	return buffer.Bytes()
}

// writeAgentBinary writes the immutable agent executable. When the client
// advertises gzip support it is sent with Content-Encoding: gzip, which
// conforming clients (Go's transport and curl --compressed) decompress before
// hashing or saving it. The checksum header always reflects the raw binary.
func (s *Server) writeAgentBinary(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	body := s.agentBinary
	if len(s.agentBinaryGzip) != 0 && acceptsGzip(request) {
		body = s.agentBinaryGzip
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// agentBinary serves the statically-extracted agent executable only to a valid
// node-bound add-node credential.
func (s *Server) serveAgentBinary(w http.ResponseWriter, r *http.Request) {
	if len(s.agentBinary) == 0 {
		http.NotFound(w, r)
		return
	}
	if !s.enrollmentDownloadAllowed(w, r) {
		return
	}
	s.writeAgentBinary(w, r)
}

// serveAgentBinaryForAgent serves the same immutable binary as the installer,
// but authenticates an already-enrolled Agent with its Ed25519 request
// signature. This keeps the enrollment credential out of long-lived Agent
// state while allowing an operator to upgrade it from the panel.
func (s *Server) serveAgentBinaryForAgent(w http.ResponseWriter, r *http.Request) {
	if len(s.agentBinary) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-QControlHub-Agent-SHA256", fmt.Sprintf("%x", sha256.Sum256(s.agentBinary)))
	if s.agentVersion != "" {
		w.Header().Set("X-QControlHub-Agent-Version", s.agentVersion)
	}
	s.writeAgentBinary(w, r)
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

	mux.Handle("GET /api/v1/overview", s.requirePermission(core.PermissionOverviewRead, http.HandlerFunc(s.overview)))
	mux.Handle("GET /api/v1/panel-metrics", s.requirePermission(core.PermissionPanelMetricsRead, http.HandlerFunc(s.getPanelMetrics)))
	mux.Handle("GET /api/v1/agents", s.requirePermission(core.PermissionAgentsRead, http.HandlerFunc(s.listAgents)))
	mux.Handle("GET /api/v1/agent-access", s.requireAllPermissions(nil, http.HandlerFunc(s.getOwnAgentAccess)))
	mux.Handle("GET /api/v1/agent-directory", s.requirePermission(core.PermissionAgentsRead, http.HandlerFunc(s.listAgentDirectory)))
	mux.Handle("POST /api/v1/agent-access/{id}/response", s.requireAllPermissions(nil, http.HandlerFunc(s.respondAgentShare)))
	mux.Handle("GET /api/v1/deployments", s.requirePermission(core.PermissionDeploymentsRead, http.HandlerFunc(s.listDeployments)))
	mux.Handle("GET /api/v1/client-access", s.requirePermission(core.PermissionClientAccessRead, http.HandlerFunc(s.listClientAccess)))
	mux.Handle("GET /api/v1/substore-sync", s.requirePermission(core.PermissionClientAccessRead, http.HandlerFunc(s.getSubStoreSync)))
	mux.Handle("PUT /api/v1/substore-sync/settings", s.requirePermission(core.PermissionSettingsManage, http.HandlerFunc(s.putSubStoreSettings)))
	mux.Handle("POST /api/v1/substore-sync/targets", s.requirePermission(core.PermissionSettingsManage, s.subStoreMutation(s.createSubStoreTarget)))
	mux.Handle("GET /api/v1/substore-sync/remote-targets", s.requirePermission(core.PermissionSettingsManage, http.HandlerFunc(s.listSubStoreRemoteTargets)))
	mux.Handle("POST /api/v1/substore-sync/targets/import", s.requirePermission(core.PermissionSettingsManage, s.subStoreMutation(s.importSubStoreRemoteTarget)))
	mux.Handle("POST /api/v1/substore-sync/targets/{id}/remote", s.requirePermission(core.PermissionSettingsManage, s.subStoreMutation(s.linkSubStoreRemoteTarget)))
	mux.Handle("PUT /api/v1/substore-sync/targets/{id}", s.requirePermission(core.PermissionSettingsManage, s.subStoreMutation(s.updateSubStoreTarget)))
	mux.Handle("DELETE /api/v1/substore-sync/targets/{id}", s.requirePermission(core.PermissionSettingsManage, s.subStoreMutation(s.deleteSubStoreTarget)))
	mux.Handle("PUT /api/v1/substore-sync/selections", s.requirePermission(core.PermissionSettingsManage, s.subStoreMutation(s.putSubStoreSelections)))
	mux.Handle("POST /api/v1/substore-sync/test", s.requirePermission(core.PermissionSettingsManage, http.HandlerFunc(s.testSubStoreConnection)))
	mux.Handle("POST /api/v1/substore-sync/run", s.requirePermission(core.PermissionSettingsManage, s.subStoreMutation(s.runSubStoreSync)))
	mux.Handle("GET /api/v1/core-logs", s.requirePermission(core.PermissionCoreLogsRead, http.HandlerFunc(s.listCoreLogs)))
	mux.Handle("GET /api/v1/access-controls", s.requirePermission(core.PermissionAgentConfigRead, http.HandlerFunc(s.listMainlandAccessPolicies)))
	mux.Handle("PUT /api/v1/access-controls", s.requireAllPermissions(
		[]core.Permission{core.PermissionAgentConfigWrite, core.PermissionTasksExecute},
		http.HandlerFunc(s.putMainlandAccessPolicy),
	))
	mux.Handle("PUT /api/v1/agents/{id}/client-address", s.requirePermission(core.PermissionAgentsManage, http.HandlerFunc(s.putAgentClientAddress)))
	mux.Handle("GET /api/v1/agents/{id}/region", s.requirePermission(core.PermissionAgentsRead, http.HandlerFunc(s.getAgentRegion)))
	mux.Handle("PUT /api/v1/agents/{id}/region", s.requirePermission(core.PermissionAgentsManage, http.HandlerFunc(s.putAgentRegion)))
	mux.Handle("GET /api/v1/regions", s.requirePermission(core.PermissionAgentsRead, http.HandlerFunc(s.listRegions)))
	mux.Handle("GET /api/v1/region-flags/{code}", s.requirePermission(core.PermissionAgentsRead, http.HandlerFunc(s.getRegionFlag)))
	mux.Handle("GET /api/v1/agents/{id}/komari", s.requirePermission(core.PermissionAgentsRead, http.HandlerFunc(s.getAgentKomari)))
	mux.Handle("PUT /api/v1/agents/{id}/komari", s.requirePermission(core.PermissionAgentsManage, http.HandlerFunc(s.putAgentKomari)))
	mux.Handle("PUT /api/v1/agents/{id}/name", s.requirePermission(core.PermissionAgentsManage, http.HandlerFunc(s.putAgentName)))
	mux.Handle("PUT /api/v1/agents/{id}/visibility", s.requirePermission(core.PermissionAgentsManage, http.HandlerFunc(s.putAgentVisibility)))
	mux.Handle("GET /api/v1/agents/{id}/sharing", s.requirePermission(core.PermissionAgentsManage, http.HandlerFunc(s.getAgentSharing)))
	mux.Handle("PUT /api/v1/agents/{id}/sharing", s.requirePermission(core.PermissionAgentsManage, http.HandlerFunc(s.putAgentSharing)))
	mux.Handle("PUT /api/v1/agents/{id}/capabilities/{engine}", s.requirePermission(core.PermissionAgentsManage, http.HandlerFunc(s.putAgentEngineCapability)))
	mux.Handle("GET /api/v1/config-catalogs/{engine}", s.requirePermission(core.PermissionCatalogsRead, http.HandlerFunc(s.configCatalog)))
	mux.Handle("DELETE /api/v1/agents/{id}", s.requirePermission(core.PermissionAgentsManage, http.HandlerFunc(s.deleteAgent)))
	mux.Handle("POST /api/v1/agents/{id}/enrollment-token", s.requirePermission(core.PermissionEnrollmentManage, http.HandlerFunc(s.createAgentEnrollmentToken)))
	mux.Handle("POST /api/v1/agents/{id}/enrollment-command", s.requirePermission(core.PermissionEnrollmentManage, http.HandlerFunc(s.getAgentEnrollmentCommand)))
	mux.Handle("GET /api/v1/agents/{id}/configs", s.requirePermission(core.PermissionAgentConfigRead, http.HandlerFunc(s.listAgentConfigs)))
	mux.Handle("GET /api/v1/agents/{id}/configs/{engine}", s.requirePermission(core.PermissionAgentConfigRead, http.HandlerFunc(s.getAgentConfig)))
	mux.Handle("PUT /api/v1/agents/{id}/configs/{engine}", s.requirePermission(core.PermissionAgentConfigWrite, http.HandlerFunc(s.putAgentConfig)))
	mux.Handle("GET /api/v1/agents/{id}/configs/{engine}/files", s.requirePermission(core.PermissionAgentConfigRead, http.HandlerFunc(s.getAgentConfigFiles)))
	mux.Handle("PUT /api/v1/agents/{id}/configs/{engine}/files", s.requirePermission(core.PermissionAgentConfigWrite, http.HandlerFunc(s.putAgentConfigFiles)))
	mux.Handle("GET /api/v1/agents/{id}/configs/{engine}/workspace", s.requirePermission(core.PermissionAgentConfigRead, http.HandlerFunc(s.agentConfigWorkspace)))
	mux.Handle("POST /api/v1/agents/{id}/configs/{engine}/plans", s.requirePermission(core.PermissionAgentConfigWrite, http.HandlerFunc(s.newServerPlan)))
	mux.Handle("POST /api/v1/agents/{id}/configs/{engine}/server-inbounds", s.requireAllPermissions([]core.Permission{core.PermissionAgentConfigWrite, core.PermissionTasksExecute}, http.HandlerFunc(s.saveServerInbound)))
	mux.Handle("GET /api/v1/agents/{id}/configs/{engine}/fields/{key}", s.requirePermission(core.PermissionAgentConfigRead, http.HandlerFunc(s.getConfigField)))
	mux.Handle("POST /api/v1/agents/{id}/configs/{engine}/fields/{key}", s.requireAllPermissions([]core.Permission{core.PermissionAgentConfigWrite, core.PermissionTasksExecute}, http.HandlerFunc(s.saveConfigField)))
	mux.Handle("POST /api/v1/agents/{id}/configs/{engine}/source", s.requireAllPermissions([]core.Permission{core.PermissionAgentConfigWrite, core.PermissionTasksExecute}, http.HandlerFunc(s.savePresetSource)))
	mux.Handle("GET /api/v1/configs", s.requirePermission(core.PermissionConfigsRead, http.HandlerFunc(s.listConfigs)))
	mux.Handle("POST /api/v1/configs", s.requirePermission(core.PermissionConfigsWrite, http.HandlerFunc(s.createConfig)))
	mux.Handle("PUT /api/v1/configs/{id}", s.requirePermission(core.PermissionConfigsWrite, http.HandlerFunc(s.updateConfig)))
	mux.Handle("DELETE /api/v1/configs/{id}", s.requirePermission(core.PermissionConfigsDelete, http.HandlerFunc(s.deleteConfig)))
	mux.Handle("GET /api/v1/configs/{id}/revisions", s.requirePermission(core.PermissionConfigsRead, http.HandlerFunc(s.listConfigRevisions)))
	mux.Handle("GET /api/v1/configs/{id}/revisions/{version}", s.requirePermission(core.PermissionConfigsRead, http.HandlerFunc(s.getConfigRevision)))
	mux.Handle("POST /api/v1/configs/{id}/revisions/{version}/restore", s.requirePermission(core.PermissionConfigsRestore, http.HandlerFunc(s.restoreConfigRevision)))
	mux.Handle("GET /api/v1/tasks", s.requirePermission(core.PermissionTasksRead, http.HandlerFunc(s.listTasks)))
	mux.Handle("GET /api/v1/system-tcp/tasks", s.requirePermission(core.PermissionTasksRead, http.HandlerFunc(s.latestSystemTCPTasks)))
	mux.Handle("GET /api/v1/system-tcp/parameters", s.requirePermission(core.PermissionAgentsRead, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeJSON(w, http.StatusOK, core.TCPParameterRules())
	})))
	mux.Handle("POST /api/v1/tasks", s.requirePermission(core.PermissionTasksExecute, http.HandlerFunc(s.createTask)))
	mux.Handle("GET /api/v1/tasks/{id}", s.requirePermission(core.PermissionTasksRead, http.HandlerFunc(s.getTask)))
	mux.Handle("GET /api/v1/tasks/{id}/config-snapshot", s.requireAllPermissions(
		[]core.Permission{core.PermissionTasksRead, core.PermissionAgentConfigRead},
		http.HandlerFunc(s.getTaskConfigSnapshot),
	))
	mux.Handle("DELETE /api/v1/tasks/{id}", s.requirePermission(core.PermissionTasksExecute, http.HandlerFunc(s.cancelTask)))
	mux.Handle("POST /api/v1/tasks/{id}/retry", s.requirePermission(core.PermissionTasksExecute, http.HandlerFunc(s.retryTask)))
	mux.Handle("GET /api/v1/enrollment-tokens", s.requirePermission(core.PermissionEnrollmentManage, http.HandlerFunc(s.listEnrollmentTokens)))
	mux.Handle("POST /api/v1/enrollment-tokens", s.requirePermission(core.PermissionEnrollmentManage, http.HandlerFunc(s.createEnrollmentToken)))
	mux.Handle("DELETE /api/v1/enrollment-tokens/{id}", s.requirePermission(core.PermissionEnrollmentManage, http.HandlerFunc(s.deleteEnrollmentToken)))
	mux.Handle("POST /api/v1/enrollment-tokens/{id}/command", s.requirePermission(core.PermissionEnrollmentManage, http.HandlerFunc(s.getEnrollmentCommand)))
	mux.Handle("GET /api/v1/settings", s.requirePermission(core.PermissionSettingsRead, http.HandlerFunc(s.getSettings)))
	mux.Handle("PUT /api/v1/settings", s.requirePermission(core.PermissionSettingsManage, http.HandlerFunc(s.putSettings)))
	mux.Handle("GET /api/v1/settings/deployment", s.requirePermission(core.PermissionSettingsRead, http.HandlerFunc(s.getDeploymentSettings)))
	mux.Handle("POST /api/v1/settings/check-update", s.requirePermission(core.PermissionSettingsRead, http.HandlerFunc(s.checkUpdate)))
	mux.Handle("GET /api/v1/audit", s.requirePermission(core.PermissionAuditRead, http.HandlerFunc(s.listAudit)))
	mux.Handle("GET /api/v1/users", s.requirePermission(core.PermissionUsersManage, http.HandlerFunc(s.listUsers)))
	mux.Handle("POST /api/v1/users", s.requirePermission(core.PermissionUsersManage, http.HandlerFunc(s.createUser)))
	mux.Handle("PUT /api/v1/users/{id}", s.requirePermission(core.PermissionUsersManage, http.HandlerFunc(s.updateUser)))
	mux.Handle("DELETE /api/v1/users/{id}", s.requirePermission(core.PermissionUsersManage, http.HandlerFunc(s.deleteUser)))
	mux.Handle("POST /api/v1/users/{id}/purge", s.requirePermission(core.PermissionUsersManage, http.HandlerFunc(s.purgeUser)))
	mux.Handle("GET /api/v1/users/{id}/agent-access", s.requirePermission(core.PermissionUsersManage, http.HandlerFunc(s.getUserAgentAccess)))
	mux.Handle("PUT /api/v1/users/{id}/agent-access", s.requirePermission(core.PermissionUsersManage, http.HandlerFunc(s.putUserAgentAccess)))
	mux.Handle("GET /api/v1/metrics/{id}", s.requirePermission(core.PermissionMetricsRead, http.HandlerFunc(s.metricSamples)))
	mux.Handle("GET /api/v1/traffic-policies", s.requirePermission(core.PermissionTrafficRead, http.HandlerFunc(s.listPortTrafficPolicies)))
	mux.Handle("GET /api/v1/traffic-endpoints", s.requirePermission(core.PermissionTrafficRead, http.HandlerFunc(s.listPortTrafficEndpoints)))
	mux.Handle("POST /api/v1/traffic-endpoints/sync", s.requirePermission(core.PermissionTrafficManage, http.HandlerFunc(s.syncPortTrafficEndpoints)))
	mux.Handle("GET /api/v1/traffic-endpoints/sync", s.requirePermission(core.PermissionTrafficManage, http.HandlerFunc(s.previewPortTrafficSync)))
	mux.Handle("GET /api/v1/traffic-usage", s.requirePermission(core.PermissionTrafficRead, http.HandlerFunc(s.listPortTrafficUsage)))
	mux.Handle("POST /api/v1/traffic-policies", s.requirePermission(core.PermissionTrafficManage, http.HandlerFunc(s.createPortTrafficPolicy)))
	mux.Handle("PUT /api/v1/traffic-policies/{id}", s.requirePermission(core.PermissionTrafficManage, http.HandlerFunc(s.updatePortTrafficPolicy)))
	mux.Handle("POST /api/v1/traffic-policies/{id}/reset", s.requirePermission(core.PermissionTrafficManage, http.HandlerFunc(s.resetPortTrafficPolicy)))
	mux.Handle("DELETE /api/v1/traffic-policies/{id}", s.requirePermission(core.PermissionTrafficManage, http.HandlerFunc(s.deletePortTrafficPolicy)))
	mux.Handle("DELETE /api/v1/traffic-policies/{id}/monitoring", s.requirePermission(core.PermissionTrafficManage, http.HandlerFunc(s.deletePortTrafficMonitoring)))
	mux.Handle("GET /api/v1/templates", s.requirePermission(core.PermissionTemplatesRead, http.HandlerFunc(s.listTemplates)))
	mux.Handle("POST /api/v1/templates", s.requirePermission(core.PermissionTemplatesWrite, http.HandlerFunc(s.createTemplate)))
	mux.Handle("DELETE /api/v1/templates/{id}", s.requirePermission(core.PermissionTemplatesDelete, http.HandlerFunc(s.deleteTemplate)))
	mux.Handle("POST /api/v1/templates/{id}/apply", s.requireAllPermissions(
		[]core.Permission{core.PermissionTemplatesWrite, core.PermissionAgentConfigWrite},
		http.HandlerFunc(s.applyTemplate),
	))

	mux.HandleFunc("GET /api/v1/agent-binary", s.serveAgentBinary)
	mux.Handle("GET /agent/v1/binary", s.agent(http.HandlerFunc(s.serveAgentBinaryForAgent)))

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

// sessionAllows reports whether the authenticated caller holds a capability.
// Role and explicit permission lookups are both deny-by-default, so an
// unresolvable session never grants access.
func (s *Server) sessionAllows(request *http.Request, permission core.Permission) bool {
	role, roleOK := s.sessionRole(request)
	permissions, permissionsOK := s.sessionPermissions(request)
	if !roleOK || !permissionsOK {
		return false
	}
	return role.Allows(permission) || core.HasPermission(permissions, permission)
}

func (s *Server) listAgents(w http.ResponseWriter, request *http.Request) {
	list := s.store.ListAgents
	if s.sessionAllows(request, core.PermissionEnrollmentManage) {
		list = s.store.ListAgentsWithEnrollmentCommands
	}
	agents, err := list(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if !s.sessionAllows(request, core.PermissionMetricsRead) {
		for index := range agents {
			agents[index].Metrics = core.HostMetrics{}
		}
	}
	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) redactAgentMetrics(request *http.Request, agent *core.Agent) {
	if !s.sessionAllows(request, core.PermissionMetricsRead) {
		agent.Metrics = core.HostMetrics{}
	}
}

func (s *Server) putAgentName(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if err := s.store.SetAgentName(request.Context(), request.PathValue("id"), input.Name); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "agent.renamed", request.PathValue("id"), input.Name)
	writeJSON(w, http.StatusOK, input)
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
	s.saveAgentConfigResponse(w, request, input)
}

func (s *Server) saveAgentConfigResponse(w http.ResponseWriter, request *http.Request, input core.Config) {
	config, err := s.store.SaveAgentConfig(request.Context(), input, input.Version)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.reconcileSavedShadowsocksRustPolicies(request.Context(), config); err != nil {
		writeStoreError(w, err)
		return
	}
	s.refreshSavedAgentTrafficMonitoring(request.Context(), config.AgentID)
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
	if input.Action.SystemBBR() && !s.authorizeSystemBBR(w, request, input.AgentID) {
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
	read := s.store.GetTask
	if request.URL.Query().Get("view") == "status" {
		read = s.store.GetTaskState
	}
	task, err := read(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) getTaskConfigSnapshot(w http.ResponseWriter, request *http.Request) {
	task, err := s.store.GetTask(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.store.CheckAgentAccess(request.Context(), task.AgentID, true); err != nil {
		writeStoreError(w, err)
		return
	}
	if (task.Action != core.ActionReadConfig && task.Action != core.ActionReadManagedConfig) || task.Status != core.TaskSucceeded {
		writeStoreError(w, store.ErrNotFound)
		return
	}
	content, err := s.store.ReadTaskConfigSnapshot(request.Context(), task.ID, task.AgentID, task.Engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
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
	previous, err := s.store.GetTask(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if previous.Action.SystemBBR() && !s.authorizeSystemBBR(w, request, previous.AgentID) {
		return
	}
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

func (s *Server) putAgentClientAddress(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Address     *string                `json:"address"`
		Name        *string                `json:"name"`
		AddressMode *string                `json:"address_mode"`
		Profile     *clientProfileSelector `json:"profile"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var address *string
	if input.Address != nil {
		value := strings.TrimSpace(*input.Address)
		address = &value
	}
	if address != nil && *address != "" {
		var err error
		value, err := serverconfig.NormalizeClientAddress(*address)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		address = &value
	}
	var name *string
	if input.Name != nil {
		value := strings.TrimSpace(*input.Name)
		if value != "" && (utf8.RuneCountInString(value) > 100 || strings.ContainsAny(value, "\r\n")) {
			writeError(w, http.StatusBadRequest, "客户端节点名称不能超过 100 个字符")
			return
		}
		name = &value
	}
	var addressMode *string
	if input.AddressMode != nil {
		value := strings.TrimSpace(*input.AddressMode)
		if value != core.SubStoreAddressModeAuto && value != core.SubStoreAddressModeIPv4 && value != core.SubStoreAddressModeIPv6 {
			writeError(w, http.StatusBadRequest, "客户端地址协议栈必须是 auto、ipv4 或 ipv6")
			return
		}
		addressMode = &value
	}
	if address == nil && name == nil && addressMode == nil {
		writeError(w, http.StatusBadRequest, "至少需要提供一个客户端显示参数")
		return
	}
	var saveErr error
	if input.Profile != nil {
		label, err := s.clientProfileNameLabel(request.Context(), request.PathValue("id"), *input.Profile)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		saveErr = s.store.SetConfigClientPreferences(request.Context(), request.PathValue("id"), label, address, name, addressMode)
	} else {
		saveErr = s.store.SetAgentClientPreferences(request.Context(), request.PathValue("id"), address, name, addressMode)
	}
	if err := saveErr; err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "agent.client_display.updated", request.PathValue("id"), "client display preferences updated")
	result := map[string]string{}
	if address != nil {
		result["address"] = *address
	}
	if name != nil {
		result["name"] = *name
	}
	if addressMode != nil {
		result["address_mode"] = *addressMode
	}
	writeJSON(w, http.StatusOK, result)
}

func komariNodeResource(node komari.Node) core.KomariNode {
	return core.KomariNode{
		UUID: node.UUID, Name: node.Name, BillingCycle: node.BillingCycle,
		TrafficLimit: node.TrafficLimit, TrafficLimitType: node.TrafficLimitType,
		EffectiveTrafficLimit: node.EffectiveTrafficLimit, EffectiveTrafficType: node.EffectiveTrafficType,
		EffectiveTrafficLimitAvailable: node.EffectiveTrafficLimitSet,
		EffectiveTrafficTypeAvailable:  node.EffectiveTrafficTypeSet,
		TrafficResetDay:                node.TrafficResetDay,
		TrafficUsed:                    node.TrafficUsed,
		TrafficUsedAvailable:           node.TrafficUsedSet,
		ExpiredAt:                      node.ExpiredAt,
		UpdatedAt:                      node.UpdatedAt,
	}
}

func (s *Server) komariForRequest(ctx context.Context, allowEnvironment bool) (*komari.Client, error) {
	if s.store == nil {
		if s.komariConfigError != nil {
			return nil, s.komariConfigError
		}
		return s.komari, nil
	}
	settings, err := s.store.PanelSettings(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(settings.KomariURL) == "" {
		if !allowEnvironment {
			return nil, nil
		}
		return s.komari, s.komariConfigError
	}
	client, err := komari.New(settings.KomariURL, settings.KomariAPIKey, s.komariHTTPClient)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (s *Server) getAgentKomari(w http.ResponseWriter, request *http.Request) {
	agent, err := s.store.GetAgent(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	uuid := store.AgentKomariUUID(agent)
	result := core.KomariLink{UUID: uuid}
	if uuid == "" {
		writeJSON(w, http.StatusOK, result)
		return
	}
	komariClient, clientErr := s.komariForRequest(request.Context(), s.configOwnerID(request) == "")
	if clientErr != nil {
		writeError(w, http.StatusServiceUnavailable, clientErr.Error())
		return
	}
	if komariClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Komari integration is not configured")
		return
	}
	node, err := komariClient.GetNode(request.Context(), uuid)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	result.Server = ptr(komariNodeResource(node))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func agentGeoIP(agent core.Agent) netip.Addr {
	for _, raw := range []string{
		agent.Metrics.PublicIPv4,
		agent.Metrics.PublicIPv6,
		agent.Metrics.ObservedPublicIP,
	} {
		address, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil || !netpolicy.IsPublicAddress(address) || netpolicy.IsCloudflareAddress(address) {
			continue
		}
		return address
	}
	for _, networkInterface := range agent.Metrics.NetworkInterfaces {
		for _, raw := range networkInterface.Addresses {
			address, err := netip.ParseAddr(strings.TrimSpace(raw))
			if err != nil || !netpolicy.IsPublicAddress(address) || netpolicy.IsCloudflareAddress(address) {
				continue
			}
			return address
		}
	}
	return netip.Addr{}
}

func (s *Server) getAgentRegion(w http.ResponseWriter, request *http.Request) {
	agent, err := s.store.GetAgent(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if code := store.AgentRegionCode(agent); code != "" {
		writeJSON(w, http.StatusOK, map[string]string{"country_code": code, "source": "manual"})
		return
	}
	// Automatic detection derives the address and country from host metrics,
	// which metrics.read gates. Without the capability this endpoint must stay
	// empty instead of disclosing the node address or its GeoIP.
	if !s.sessionAllows(request, core.PermissionMetricsRead) {
		writeJSON(w, http.StatusOK, map[string]string{})
		return
	}
	address := agentGeoIP(agent)
	if !address.IsValid() {
		writeJSON(w, http.StatusOK, map[string]string{})
		return
	}
	region, err := s.geoip.Lookup(request.Context(), address)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"ip":           address.String(),
		"country_code": region.ISOCode,
		"country":      region.Name,
		"source":       "auto",
	})
}

func (s *Server) listRegions(w http.ResponseWriter, request *http.Request) {
	writeJSON(w, http.StatusOK, geoip.RegionCodes())
}

func (s *Server) putAgentRegion(w http.ResponseWriter, request *http.Request) {
	var input struct {
		CountryCode *string `json:"country_code"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.CountryCode == nil {
		writeError(w, http.StatusBadRequest, "country_code is required; use an empty string for automatic GeoIP")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(*input.CountryCode))
	if err := s.store.SetAgentRegionCode(request.Context(), request.PathValue("id"), code); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "agent.region.updated", request.PathValue("id"), code)
	writeJSON(w, http.StatusOK, map[string]string{"country_code": code})
}

func (s *Server) getRegionFlag(w http.ResponseWriter, request *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(request.PathValue("code")))
	// The panel display policy treats Taiwan as part of China and uses the
	// China flag regardless of which code a direct API caller supplies.
	if code == "TW" {
		code = "CN"
	}
	flag, err := s.geoip.Flag(request.Context(), code)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=172800, immutable")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(flag)
}

func (s *Server) putAgentKomari(w http.ResponseWriter, request *http.Request) {
	var input struct {
		UUID       *string `json:"uuid"`
		KomariUUID *string `json:"komari_uuid"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	value := input.UUID
	if value == nil {
		value = input.KomariUUID
	}
	if value == nil {
		writeError(w, http.StatusBadRequest, "uuid is required")
		return
	}
	uuid := strings.TrimSpace(*value)
	if len(uuid) > 100 || strings.ContainsAny(uuid, "\r\n\t") {
		writeError(w, http.StatusBadRequest, "Komari server UUID is invalid")
		return
	}
	if err := s.store.SetAgentKomariUUID(request.Context(), request.PathValue("id"), uuid); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "agent.komari.updated", request.PathValue("id"), "Komari UUID linked")
	writeJSON(w, http.StatusOK, core.KomariLink{UUID: uuid})
}

func ptr[T any](value T) *T { return &value }

func (s *Server) createEnrollmentToken(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Name        string `json:"name"`
		AdminHidden bool   `json:"admin_hidden"`
	}
	if err := decodeJSON(w, request, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	requestData := core.EnrollmentTokenRequest{Name: input.Name, Reusable: true, AdminHidden: input.AdminHidden}
	var created core.EnrollmentTokenCreated
	var err error
	if s.auditWriter == nil {
		created, err = s.store.CreateProtectedEnrollmentTokenWithAudit(request.Context(), requestData,
			s.auditEntry(request, "enrollment_token.created", "", input.Name))
	} else {
		created, err = s.store.CreateProtectedEnrollmentToken(request.Context(), requestData)
		if err == nil {
			auditErr := s.recordAuditSync(request, "enrollment_token.created", created.ID, created.Name)
			if auditErr != nil {
				err = fmt.Errorf("%w: %v", store.ErrAuditUnavailable, auditErr)
				if cleanupErr := s.discardEnrollmentToken(created.ID); cleanupErr != nil {
					slog.Error("enrollment credential cleanup after audit failure", "error", cleanupErr)
				}
			}
		}
	}
	if err != nil {
		if errors.Is(err, store.ErrAuditUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "audit unavailable; enrollment credential was not disclosed")
		} else {
			writeStoreError(w, err)
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) getAgentEnrollmentCommand(w http.ResponseWriter, request *http.Request) {
	created, err := s.store.EnrollmentCommandForAgent(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.recordAuditSync(request, "agent.enrollment_command.viewed", request.PathValue("id"), created.ID); err != nil {
		writeError(w, http.StatusServiceUnavailable, "audit unavailable; enrollment credential was not disclosed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, created)
}

func (s *Server) getEnrollmentCommand(w http.ResponseWriter, request *http.Request) {
	created, err := s.store.EnrollmentCommandByID(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.recordAuditSync(request, "enrollment_token.command_viewed", created.ID, created.AgentID); err != nil {
		writeError(w, http.StatusServiceUnavailable, "audit unavailable; enrollment credential was not disclosed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, created)
}

func (s *Server) deleteEnrollmentToken(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteEnrollmentToken(request.Context(), request.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "enrollment_token.deleted", request.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createAgentEnrollmentToken(w http.ResponseWriter, request *http.Request) {
	agentID := request.PathValue("id")
	var created core.EnrollmentTokenCreated
	var err error
	if s.auditWriter == nil {
		created, err = s.store.CreateAgentEnrollmentTokenWithAudit(request.Context(), agentID,
			s.auditEntry(request, "agent.enrollment_token.created", agentID, ""))
	} else {
		created, err = s.store.CreateAgentEnrollmentToken(request.Context(), agentID)
		if err == nil {
			auditErr := s.recordAuditSync(request, "agent.enrollment_token.created", agentID, created.ID)
			if auditErr != nil {
				err = fmt.Errorf("%w: %v", store.ErrAuditUnavailable, auditErr)
				if cleanupErr := s.discardEnrollmentToken(created.ID); cleanupErr != nil {
					slog.Error("enrollment credential cleanup after audit failure", "error", cleanupErr)
				}
			}
		}
	}
	if err != nil {
		if errors.Is(err, store.ErrAuditUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "audit unavailable; enrollment credential was not disclosed")
		} else {
			writeStoreError(w, err)
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) discardEnrollmentToken(id string) error {
	cleanupContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.store.DeleteEnrollmentToken(cleanupContext, id)
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
			writeError(w, http.StatusUnauthorized, "invalid, deleted, or mismatched add-node credential")
			return
		}
		writeStoreError(w, err)
		return
	}
	s.enrollLimiter.Success(key)
	if agent.Reinstalled {
		s.DisconnectAgent(agent.ID)
	}
	status := http.StatusCreated
	if agent.Reinstalled {
		status = http.StatusOK
	}
	slog.Info("agent enrolled", "agent_id", agent.ID, "name", agent.Name, "reinstalled", agent.Reinstalled)
	writeJSON(w, status, core.EnrollResponse{AgentID: agent.ID})
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
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrSecretUnavailable):
		writeError(w, http.StatusServiceUnavailable, "protected credential storage is unavailable; configure QCH_CONFIG_ENCRYPTION_KEY")
	case errors.Is(err, store.ErrAuditUnavailable):
		writeError(w, http.StatusServiceUnavailable, "audit unavailable; enrollment credential was not disclosed")
	default:
		writeInternalError(w, err)
	}
}

func writeInternalError(w http.ResponseWriter, err error) {
	slog.Error("HTTP request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": chineseErrorMessage(status, message)})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("encode HTTP response", "error", err)
	}
}
