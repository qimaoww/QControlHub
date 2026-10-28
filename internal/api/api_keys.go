package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type apiKeyContextKey struct{}

func requestAPIKey(r *http.Request) (core.APIKey, bool) {
	k, ok := r.Context().Value(apiKeyContextKey{}).(core.APIKey)
	return k, ok
}
func environmentCreatorID(digest [32]byte) string {
	return fmt.Sprintf("env_%x", sha256.Sum256(append([]byte("qch-api-key-creator\x00"), digest[:]...)))
}
func (s *Server) apiKeyCreator(r *http.Request) (string, string) {
	if token := bearerToken(r); token != "" {
		return "environment", environmentCreatorID(sha256.Sum256([]byte(token)))
	}
	if v, ok := s.sessionForRequest(r); ok && v.UserID != "" {
		return "user", v.UserID
	}
	// Cookie sessions carry the environment principal's stable registry ID.
	v, _ := s.sessionForRequest(r)
	return "environment", v.EnvironmentID
}

func (s *Server) authenticateAPIKey(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	secret := bearerToken(r)
	// Existing environment tokens take precedence, including historical values
	// that happen to use the new namespace.
	if _, ok := s.principalForToken(secret); ok || !strings.HasPrefix(secret, "qch_key_") {
		return r, true
	}
	ip := authn.ClientIP(r, s.trustedProxies)
	now := time.Now().UTC()
	if !s.adminLimiter.Allow(ip, now) {
		writeError(w, 429, "too many authentication failures")
		return r, false
	}
	invalid := func() (*http.Request, bool) {
		s.adminLimiter.Failure(ip, now)
		w.Header().Set("WWW-Authenticate", `Bearer realm="QControlHub admin API"`)
		writeError(w, 401, "invalid admin token")
		return r, false
	}
	if s.store == nil {
		return invalid()
	}
	k, err := s.store.APIKeyForAuthentication(r.Context(), secret)
	if errors.Is(err, store.ErrNotFound) {
		return invalid()
	}
	if err != nil {
		writeInternalError(w, err)
		return r, false
	}
	invalidKnownKey := func() (*http.Request, bool) {
		auditRequest := r.WithContext(context.WithValue(store.WithConfigScope(r.Context(), k.OwnerID, true), apiKeyContextKey{}, k))
		s.recordAudit(auditRequest, "api_key.request", k.ID, "authentication denied: 401 "+r.Method+" "+r.Pattern)
		return invalid()
	}
	if k.Status != "active" {
		return invalidKnownKey()
	}
	v := apiSession{Role: core.RoleAdmin, ConfigOwnerID: k.OwnerID, Permissions: k.Permissions, ExpiresAt: now.Add(time.Minute)}
	current := []core.Permission(nil)
	if k.CreatorType == "user" {
		user, err := s.store.UserForSession(r.Context(), k.CreatorID)
		if errors.Is(err, store.ErrNotFound) {
			return invalidKnownKey()
		}
		if err != nil {
			writeInternalError(w, err)
			return r, false
		}
		if user.Role != core.RoleAdmin {
			return invalidKnownKey()
		}
		v.UserID = user.ID
		v.Username = user.Username
		v.AuthRevision = user.AuthRevision
		current = user.Permissions
		for _, p := range core.AllPermissions() {
			if user.Role.Allows(p) && !core.HasPermission(current, p) {
				current = append(current, p)
			}
		}
	} else {
		for digest, p := range s.roleTokens {
			if p.Role == core.RoleAdmin && environmentCreatorID(digest) == k.CreatorID {
				current = p.Permissions
				v.EnvironmentID = k.CreatorID
				break
			}
		}
		if v.EnvironmentID == "" {
			return invalidKnownKey()
		}
	}
	effective := []core.Permission{}
	for _, p := range k.Permissions {
		if core.HasPermission(current, p) {
			effective = append(effective, p)
		}
	}
	v.Permissions = effective
	ctx := context.WithValue(r.Context(), validatedSessionKey{}, v)
	ctx = context.WithValue(ctx, apiKeyContextKey{}, k)
	r = r.WithContext(store.WithConfigScope(ctx, k.OwnerID, true))
	if !core.IPAllowed(k.IPAllowlist, ip) {
		s.recordAudit(r, "api_key.request", k.ID, "IP allowlist denied: 403 "+r.Method+" "+r.Pattern)
		writeError(w, 403, "来源 IP 不在 API Key 白名单内")
		return r, false
	}
	// Reset failures only after every credential check has passed. A valid key
	// from a denied source must not clear the shared IP failure counter that
	// throttles other authentication paths.
	s.adminLimiter.Success(ip)
	return r, true
}

func (s *Server) apiKeyRoutes(mux *http.ServeMux) {
	manage := func(next http.HandlerFunc) http.Handler {
		return s.requirePermission(core.PermissionSettingsManage, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, key := requestAPIKey(r); key {
				writeError(w, 403, "API Key 无法管理凭据")
				return
			}
			role, _ := s.sessionRole(r)
			if role != core.RoleAdmin {
				writeError(w, 403, "仅管理员可管理 API Key")
				return
			}
			next(w, r)
		}))
	}
	mux.Handle("GET /api/v1/settings/api-keys", manage(s.listAPIKeys))
	mux.Handle("POST /api/v1/settings/api-keys", manage(s.createAPIKey))
	mux.Handle("PUT /api/v1/settings/api-keys/{id}", manage(s.updateAPIKey))
	mux.Handle("DELETE /api/v1/settings/api-keys/{id}", manage(s.revokeAPIKey))
	mux.Handle("GET /api/v1/api-key", s.requireAllPermissions(nil, http.HandlerFunc(s.getAPIKeyAuthorization)))
}
func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	typ, id := s.apiKeyCreator(r)
	items, err := s.store.ListAPIKeys(r.Context(), typ, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "read_permissions": core.DiagnosticPermissions()})
}
func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var input core.APIKeyInput
	if err := decodeJSON(w, r, &input, 32<<10); err != nil {
		writeError(w, 400, "API Key 请求格式无效")
		return
	}
	typ, id := s.apiKeyCreator(r)
	if id == "" {
		writeError(w, 401, "invalid admin token")
		return
	}
	k, secret, err := s.store.CreateAPIKey(r.Context(), typ, id, input, s.auditEntry(r, "", "", ""))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"key": k, "secret": secret})
}
func (s *Server) updateAPIKey(w http.ResponseWriter, r *http.Request) {
	var input core.APIKeyInput
	if err := decodeJSON(w, r, &input, 32<<10); err != nil {
		writeError(w, 400, "API Key 请求格式无效")
		return
	}
	s.saveAPIKey(w, r, input, false)
}
func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	s.saveAPIKey(w, r, core.APIKeyInput{}, true)
}
func (s *Server) saveAPIKey(w http.ResponseWriter, r *http.Request, input core.APIKeyInput, revoke bool) {
	typ, id := s.apiKeyCreator(r)
	k, err := s.store.UpdateAPIKey(r.Context(), typ, id, r.PathValue("id"), input, revoke, s.auditEntry(r, "", "", ""))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, k)
}
func (s *Server) getAPIKeyAuthorization(w http.ResponseWriter, r *http.Request) {
	k, ok := requestAPIKey(r)
	if !ok {
		writeError(w, 403, "此接口仅供 API Key 查询自身授权")
		return
	}
	permissions, _ := s.sessionPermissions(r)
	endpoints := diagnosticRouteList(permissions)
	sort.Strings(endpoints)
	ip := authn.ClientIP(r, s.trustedProxies)
	if addr, err := netip.ParseAddr(ip); err == nil {
		ip = addr.Unmap().String()
	} else {
		ip = ""
	}
	writeJSON(w, 200, map[string]any{"id": k.ID, "name": k.Name, "prefix": k.Prefix, "permissions": permissions, "ip_allowlist": k.IPAllowlist, "expires_at": k.ExpiresAt, "workspace_id": k.OwnerID, "user_id": s.sessionUserID(r), "source_ip": ip, "endpoints": endpoints})
}
