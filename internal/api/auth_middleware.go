package api

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) requirePermission(permission core.Permission, next http.Handler) http.Handler {
	return s.requireAllPermissions([]core.Permission{permission}, next)
}

// requireAllPermissions guards sensitive compound operations whose resource
// visibility and content access are separate capabilities.
func (s *Server) requireAllPermissions(permissions []core.Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var valid bool
		request, valid = s.validateSession(w, request)
		if !valid {
			return
		}
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
		granted, permissionsOK := s.sessionPermissions(request)
		if !permissionsOK {
			writeError(w, http.StatusForbidden, "token role does not permit this operation")
			return
		}
		for _, permission := range permissions {
			if !role.Allows(permission) && !core.HasPermission(granted, permission) {
				writeError(w, http.StatusForbidden, "token role does not permit this operation")
				return
			}
		}
		if bearerToken(request) == "" && request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions {
			value, sessionOK := s.sessionForRequest(request)
			if !sessionOK || !constantEqual(request.Header.Get(csrfHeader), value.CSRF) {
				writeError(w, http.StatusForbidden, "missing or invalid CSRF token")
				return
			}
		}
		w.Header().Set("X-QControlHub-Role", string(role))
		request = request.WithContext(store.WithConfigScope(request.Context(), s.configOwnerID(request), role == core.RoleAdmin))
		if !s.authorizeAgentResource(w, request) {
			return
		}
		next.ServeHTTP(w, request)
	})
}

// requireRole is kept for package-level compatibility with older integrations.
// New routes must use requirePermission so a role cannot accidentally inherit
// an unrelated capability by rank.
func (s *Server) requireRole(minimum core.Role, next http.Handler) http.Handler {
	permission := core.PermissionOverviewRead
	switch minimum {
	case core.RoleAdmin:
		permission = core.PermissionUsersManage
	case core.RoleUser:
		permission = core.PermissionOverviewRead
	}
	return s.requirePermission(permission, next)
}

func (s *Server) roleForToken(token string) (core.Role, bool) {
	principal, ok := s.roleTokens[sha256.Sum256([]byte(token))]
	return principal.Role, ok
}

func (s *Server) principalForToken(token string) (tokenPrincipal, bool) {
	principal, ok := s.roleTokens[sha256.Sum256([]byte(token))]
	return principal, ok
}

func tokenConfigOwnerID(token string) string {
	// Domain separation keeps this stable identifier distinct from the token's
	// authentication digest. Two legacy tokens never share a user workspace.
	return fmt.Sprintf("token_%x", sha256.Sum256([]byte("qcontrolhub-config-owner\x00"+token)))
}

func legacyOperatorPermissions() []core.Permission {
	return []core.Permission{core.PermissionOverviewRead, core.PermissionAgentsRead, core.PermissionDeploymentsRead, core.PermissionClientAccessRead, core.PermissionCatalogsRead, core.PermissionAgentConfigRead, core.PermissionAgentConfigWrite, core.PermissionConfigsRead, core.PermissionConfigsWrite, core.PermissionTasksRead, core.PermissionTasksExecute, core.PermissionSettingsRead, core.PermissionAuditRead, core.PermissionMetricsRead, core.PermissionCoreLogsRead, core.PermissionTrafficRead, core.PermissionTrafficManage, core.PermissionTemplatesRead, core.PermissionTemplatesWrite}
}
func legacyAuditorPermissions() []core.Permission {
	return []core.Permission{core.PermissionOverviewRead, core.PermissionAgentsRead, core.PermissionDeploymentsRead, core.PermissionTasksRead, core.PermissionAuditRead, core.PermissionMetricsRead, core.PermissionCoreLogsRead, core.PermissionTrafficRead}
}
func legacyReadonlyPermissions() []core.Permission {
	return []core.Permission{core.PermissionOverviewRead, core.PermissionAgentsRead, core.PermissionDeploymentsRead, core.PermissionClientAccessRead, core.PermissionCatalogsRead, core.PermissionAgentConfigRead, core.PermissionConfigsRead, core.PermissionTasksRead, core.PermissionSettingsRead, core.PermissionAuditRead, core.PermissionMetricsRead, core.PermissionCoreLogsRead, core.PermissionTrafficRead, core.PermissionTemplatesRead}
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
		if requestAcceptsGzip(request) {
			// Large JSON lists (kernel log windows in particular) are polled
			// repeatedly; negotiate gzip before the handler commits the status.
			compressed := &compressedResponseWriter{ResponseWriter: w, status: http.StatusOK}
			defer compressed.finish()
			w = compressed
		}
		next.ServeHTTP(w, request)
		slog.Debug("http request", "method", request.Method, "path", request.URL.Path, "duration", time.Since(started))
	})
}
