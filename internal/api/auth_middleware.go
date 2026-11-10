package api

import (
	"bytes"
	"context"
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
	return s.requirePermissions(permissions, false, next)
}

// requireAnyPermission permits shared reference data used by distinct features.
// Authentication, API key restrictions and resource authorization stay identical.
func (s *Server) requireAnyPermission(permissions []core.Permission, next http.Handler) http.Handler {
	return s.requirePermissions(permissions, true, next)
}

func (s *Server) requirePermissions(permissions []core.Permission, anyPermission bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var valid bool
		mutationCompleted := false
		request, valid = s.validateSession(w, request)
		if !valid {
			return
		}
		if k, isKey := requestAPIKey(request); isKey {
			// Legacy handlers derive validators from their original payload.
			// The Key receives a redacted representation without those validators.
			request = request.Clone(request.Context())
			request.Header.Del("If-None-Match")
			response := &apiKeyResponseWriter{ResponseWriter: w, secret: bearerToken(request), revealSecrets: s.sessionAllows(request, core.PermissionSecretsRead) || request.Pattern == "GET /api/v1/openapi.json"}
			// Retain a failure only after the rejected business transaction has
			// rolled back, so its returned diagnostic ID remains queryable.
			response.failure = func(status int, value any) any {
				failure := &failureResponseWriter{ResponseWriter: response.ResponseWriter, server: s, request: request.WithContext(store.WithoutRequestTransaction(request.Context()))}
				return failure.failure(status, value)
			}
			w = response
			defer func() {
				status := response.finish()
				auditRequest := request.WithContext(store.WithoutRequestTransaction(request.Context()))
				s.recordAudit(auditRequest, "api_key.request", k.ID, fmt.Sprintf("%s %s status=%d request_id=%s", request.Method, request.Pattern, status, w.Header().Get("X-Request-ID")))
				if status >= 200 && status < 300 {
					ctx, cancel := context.WithTimeout(context.WithoutCancel(auditRequest.Context()), 2*time.Second)
					defer cancel()
					_ = s.store.TouchAPIKey(ctx, k.ID)
				}
			}()
			policy, allowed := apiKeyPolicies[request.Pattern]
			effective, _ := s.sessionPermissions(request)
			if !allowed || !policy.allows(k.Mode, effective) {
				writeError(w, 403, "API Key 模式或权限不允许此操作")
				return
			}
			// Persist every capability needed for this route, including an any-of
			// route's actual granted alternative. Body-dependent task permissions
			// are appended by the store before queuing.
			required := []core.Permission{}
			for _, p := range policy.Permissions {
				if core.HasPermission(effective, p) {
					required = append(required, p)
				}
			}
			if policy.Write {
				ctx, finish, err := s.store.BeginAPIKeyMutation(request.Context(), k, required)
				if err != nil {
					writeStoreError(w, err)
					return
				}
				request = request.WithContext(ctx)
				defer func() {
					if err := finish(mutationCompleted && (response.status == 0 || response.status < 400)); err != nil {
						response.body.Reset()
						response.status = 0
						writeStoreError(response, err)
					}
				}()
			}
			request = request.WithContext(store.WithAPIKeyAuth(request.Context(), k.ID, required, effective))
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
		request = request.WithContext(store.WithConfigScope(request.Context(), s.configOwnerID(request), role == core.RoleAdmin))
		value, _ := s.sessionForRequest(request)
		request = request.WithContext(store.WithPrincipalAuth(request.Context(), value.UserID, value.AuthRevision))
		if _, isKey := requestAPIKey(request); !isKey {
			w = &failureResponseWriter{ResponseWriter: w, server: s, request: request}
		}
		_, permissionsOK := s.sessionPermissions(request)
		if !permissionsOK {
			writeError(w, http.StatusForbidden, "token role does not permit this operation")
			return
		}
		allowed := !anyPermission
		for _, permission := range permissions {
			if s.sessionAllows(request, permission) {
				if anyPermission {
					allowed = true
					break
				}
			} else if !anyPermission {
				allowed = false
				break
			}
		}
		if !allowed {
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
		request = request.WithContext(store.WithConfigScope(request.Context(), s.configOwnerID(request), role == core.RoleAdmin))
		if role == core.RoleAdmin {
			request = request.WithContext(store.WithUserManagementAuth(request.Context(), s.sessionUserID(request), value.AuthRevision, s.tokenAdministratorAvailable()))
		}
		if !s.authorizeAgentResource(w, request) {
			return
		}
		next.ServeHTTP(w, request)
		mutationCompleted = true
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
	if token == "" {
		return tokenPrincipal{}, false
	}
	principal, ok := s.roleTokens[sha256.Sum256([]byte(token))]
	return principal, ok
}

// Consult the final authentication registry: a legacy token collision can
// replace the configured administrator credential with a non-admin role.
func (s *Server) tokenAdministratorAvailable() bool {
	for _, principal := range s.roleTokens {
		if principal.Role == core.RoleAdmin {
			return true
		}
	}
	return false
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
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-None-Match, X-QControlHub-CSRF, X-QControlHub-Agent-ID, X-QControlHub-Timestamp, X-QControlHub-Nonce, X-QControlHub-Signature")
				w.Header().Set("Access-Control-Expose-Headers", "ETag")
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
		slog.Debug("http request", "method", request.Method, "path", core.RedactDiagnostic(request.URL.Path), "duration", time.Since(started))
	})
}
