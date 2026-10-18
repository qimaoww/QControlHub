package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

const (
	apiSessionCookie    = "__Host-qcontrolhub_api_session"
	apiDevSessionCookie = "qcontrolhub_api_session_dev"
	csrfHeader          = "X-QControlHub-CSRF"
)

type apiSession struct {
	CSRF          string
	ExpiresAt     time.Time
	Role          core.Role
	UserID        string
	ConfigOwnerID string
	Username      string
	Permissions   []core.Permission
	EnvironmentID string
	AuthRevision  int64
}

type validatedSessionKey struct{}

func sessionTTL(value time.Duration) time.Duration {
	if value <= 0 {
		return 12 * time.Hour
	}
	if value < 5*time.Minute {
		return 5 * time.Minute
	}
	return value
}

func (s *Server) cookieName() string {
	if s.secureTransport {
		return apiSessionCookie
	}
	return apiDevSessionCookie
}

func (s *Server) login(w http.ResponseWriter, request *http.Request) {
	if !s.sameOrigin(request) {
		writeError(w, http.StatusForbidden, "cross-site login request rejected")
		return
	}
	key := authn.ClientIP(request, s.trustedProxies)
	now := time.Now().UTC()
	if !s.adminLimiter.Allow(key, now) {
		writeError(w, http.StatusTooManyRequests, "too many authentication failures")
		return
	}
	var input struct {
		Username string `json:"username"`
		Token    string `json:"token"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	username := strings.TrimSpace(input.Username)
	// Database passwords are opaque UTF-8 bytes, just as on create/reset.
	// Whitespace tolerance belongs only to the legacy environment-token path.
	secret := input.Token
	role, ok := core.Role(""), false
	permissions := []core.Permission(nil)
	userID := ""
	configOwnerID := ""
	var authRevision int64
	var environmentID string
	var loginLookupErr error
	if username != "" && secret != "" && s.store != nil {
		user, hash, lookupErr := s.store.UserForLogin(request.Context(), strings.ToLower(username))
		if lookupErr == nil {
			if authn.CheckPassword(hash, secret) {
				role, ok, userID, permissions = user.Role, true, user.ID, user.Permissions
				configOwnerID = user.ID
				authRevision = user.AuthRevision
				username = user.Username
			}
		} else if errors.Is(lookupErr, store.ErrNotFound) {
			// An unknown username must pay the same bcrypt cost as a wrong
			// password so logins cannot be timed to enumerate valid users.
			authn.CheckPasswordDummy(secret)
		} else {
			loginLookupErr = fmt.Errorf("lookup database account for login: %w", lookupErr)
		}
	}
	// Keep the environment token as a break-glass administrator credential.
	// It remains compatible with existing installations while durable users
	// provide named access for day-to-day panel work.
	if !ok && (username == "" || strings.EqualFold(username, "admin")) {
		if principal, found := s.principalForToken(strings.TrimSpace(secret)); found {
			role, ok, permissions = principal.Role, true, principal.Permissions
			configOwnerID = principal.ConfigOwnerID
			environmentID = environmentCreatorID(sha256.Sum256([]byte(strings.TrimSpace(secret))))
		}
	}
	if ok && loginLookupErr != nil {
		slog.Error("database account lookup failed during environment-token login", "error", loginLookupErr)
	}
	if !ok {
		if loginLookupErr != nil {
			// Infrastructure failures must be diagnosable and must not consume
			// the credential-failure budget. The response contains no DB detail.
			writeInternalError(w, loginLookupErr)
			return
		}
		s.adminLimiter.Failure(key, now)
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	s.adminLimiter.Success(key)
	token, err := core.NewToken()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	csrf, err := core.NewToken()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	expires := now.Add(s.sessionTTL)
	s.sessionsMu.Lock()
	s.pruneSessionsLocked(now)
	s.sessions[token] = apiSession{CSRF: csrf, ExpiresAt: expires, Role: role, UserID: userID, ConfigOwnerID: configOwnerID, Username: username, Permissions: append([]core.Permission(nil), permissions...), AuthRevision: authRevision, EnvironmentID: environmentID}
	s.sessionsMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: token, Path: "/", Expires: expires,
		MaxAge: int(s.sessionTTL.Seconds()), HttpOnly: true, Secure: s.secureTransport,
		SameSite: http.SameSiteStrictMode,
	})
	auditRequest := request.WithContext(store.WithConfigScope(request.Context(), configOwnerID, role == core.RoleAdmin))
	s.recordAudit(auditRequest, "login.succeeded", "", string(role))
	w.Header().Set("Cache-Control", "no-store")
	if userID != "" && s.store != nil {
		_ = s.store.RecordUserLogin(request.Context(), userID)
	}
	writeJSON(w, http.StatusOK, s.sessionResponse(apiSession{Role: role, UserID: userID, ConfigOwnerID: configOwnerID, Username: username, Permissions: permissions, CSRF: csrf, ExpiresAt: expires}))
}

func (s *Server) session(w http.ResponseWriter, request *http.Request) {
	if _, legacy := s.principalForToken(bearerToken(request)); !legacy && strings.HasPrefix(bearerToken(request), "qch_key_") {
		writeError(w, 401, "API Key 不能用于浏览器登录")
		return
	}
	var valid bool
	request, valid = s.validateSession(w, request)
	if !valid {
		return
	}
	value, ok := s.sessionForRequest(request)
	if !ok {
		writeError(w, http.StatusUnauthorized, "session expired")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.sessionResponse(value))
}

func (s *Server) sessionResponse(value apiSession) map[string]any {
	authType := "environment_token"
	if value.UserID != "" {
		authType = "database_account"
	}
	return map[string]any{"role": value.Role, "user_id": value.UserID, "workspace_id": value.ConfigOwnerID,
		"username": value.Username, "permissions": value.Permissions, "csrf_token": value.CSRF, "expires_at": value.ExpiresAt,
		"preference_id": store.NodeOrderAccount(value.ConfigOwnerID), "auth_type": authType, "token_admin_available": s.tokenAdministratorAvailable()}
}

func (s *Server) logout(w http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireCSRF(w, request); !ok {
		return
	}
	if cookie, err := request.Cookie(s.cookieName()); err == nil {
		s.sessionsMu.Lock()
		delete(s.sessions, cookie.Value)
		s.sessionsMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secureTransport, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sessionForRequest(request *http.Request) (apiSession, bool) {
	if value, ok := request.Context().Value(validatedSessionKey{}).(apiSession); ok {
		return value, true
	}
	if bearerToken(request) != "" {
		return apiSession{}, false
	}
	cookie, err := request.Cookie(s.cookieName())
	if err != nil || cookie.Value == "" {
		return apiSession{}, false
	}
	now := time.Now().UTC()
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	s.pruneSessionsLocked(now)
	value, ok := s.sessions[cookie.Value]
	return value, ok && value.ExpiresAt.After(now)
}

func (s *Server) validateSession(w http.ResponseWriter, request *http.Request) (*http.Request, bool) {
	if bearerToken(request) != "" {
		return s.authenticateAPIKey(w, request)
	}
	value, ok := s.sessionForRequest(request)
	// Optional on reads for compatibility with API clients. The browser sends
	// this on every request so an old tab cannot render a new cookie's account.
	if ok && request.Header.Get(csrfHeader) != "" &&
		(request.Method == http.MethodGet || request.Method == http.MethodHead) &&
		!constantEqual(request.Header.Get(csrfHeader), value.CSRF) {
		writeError(w, http.StatusUnauthorized, "session changed")
		return request, false
	}
	if !ok || value.UserID == "" || s.store == nil {
		return request, true // The normal authentication path handles no cookie.
	}
	user, err := s.store.UserForSession(request.Context(), value.UserID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeInternalError(w, err)
		return request, false
	}
	if err != nil || user.AuthRevision != value.AuthRevision {
		s.revokeUserSessions(value.UserID)
		writeError(w, http.StatusUnauthorized, "session expired")
		return request, false
	}
	value.Role, value.Permissions, value.Username = user.Role, user.Permissions, user.Username
	return request.WithContext(context.WithValue(request.Context(), validatedSessionKey{}, value)), true
}

func (s *Server) pruneSessionsLocked(now time.Time) {
	for key, value := range s.sessions {
		if !value.ExpiresAt.After(now) {
			delete(s.sessions, key)
		}
	}
}

func (s *Server) sessionRole(request *http.Request) (core.Role, bool) {
	if value, ok := request.Context().Value(validatedSessionKey{}).(apiSession); ok {
		return value.Role, true
	}
	if token := bearerToken(request); token != "" {
		return s.roleForToken(token)
	}
	value, ok := s.sessionForRequest(request)
	return value.Role, ok
}

func (s *Server) sessionPermissions(request *http.Request) ([]core.Permission, bool) {
	if value, ok := request.Context().Value(validatedSessionKey{}).(apiSession); ok {
		return value.Permissions, true
	}
	if token := bearerToken(request); token != "" {
		principal, ok := s.principalForToken(token)
		return principal.Permissions, ok
	}
	value, ok := s.sessionForRequest(request)
	return value.Permissions, ok
}

func (s *Server) sessionUserID(request *http.Request) string {
	if value, ok := request.Context().Value(validatedSessionKey{}).(apiSession); ok {
		return value.UserID
	}
	if bearerToken(request) != "" {
		return ""
	}
	value, ok := s.sessionForRequest(request)
	if !ok {
		return ""
	}
	return value.UserID
}

func (s *Server) configOwnerID(request *http.Request) string {
	if value, ok := request.Context().Value(validatedSessionKey{}).(apiSession); ok {
		return value.ConfigOwnerID
	}
	if token := bearerToken(request); token != "" {
		principal, _ := s.principalForToken(token)
		if principal.ConfigOwnerID == "" && principal.Role != core.RoleAdmin {
			return tokenConfigOwnerID(token)
		}
		return principal.ConfigOwnerID
	}
	value, _ := s.sessionForRequest(request)
	if value.UserID != "" {
		return value.UserID
	}
	if value.ConfigOwnerID != "" || value.Role == core.RoleAdmin {
		return value.ConfigOwnerID
	}
	// A principal without a durable identity must never inherit legacy data.
	return "unassigned"
}

func (s *Server) revokeUserSessions(userID string) {
	if userID == "" {
		return
	}
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	for token, session := range s.sessions {
		if session.UserID == userID {
			delete(s.sessions, token)
		}
	}
}

func (s *Server) requireCSRF(w http.ResponseWriter, request *http.Request) (core.Role, bool) {
	role, ok := s.sessionRole(request)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return "", false
	}
	if bearerToken(request) != "" {
		return role, true
	}
	value, ok := s.sessionForRequest(request)
	if !ok || !constantEqual(request.Header.Get(csrfHeader), value.CSRF) {
		writeError(w, http.StatusForbidden, "missing or invalid CSRF token")
		return "", false
	}
	return role, true
}

func constantEqual(actual, expected string) bool {
	if actual == "" || expected == "" || len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func (s *Server) sameOrigin(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if _, allowed := s.allowedOrigins[origin]; allowed {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	expectedScheme := request.URL.Scheme
	if expectedScheme == "" {
		if s.secureTransport {
			expectedScheme = "https"
		} else {
			expectedScheme = "http"
		}
	}
	return strings.EqualFold(parsed.Scheme, expectedScheme) && strings.EqualFold(parsed.Host, request.Host)
}

func (s *Server) recordAudit(request *http.Request, action, target, detail string) {
	if s.store == nil && s.auditWriter == nil {
		return
	}
	entry := s.auditEntry(request, action, target, detail)
	// Preserve the authenticated account even after its HTTP request ends.
	auditContext := context.WithoutCancel(request.Context())
	go func() {
		ctx, cancel := context.WithTimeout(auditContext, 2*time.Second)
		defer cancel()
		_ = s.writeAudit(ctx, entry)
	}()
}

func (s *Server) auditEntry(request *http.Request, action, target, detail string) core.AuditLogEntry {
	role, _ := s.sessionRole(request)
	actor := string(role)
	if value, ok := s.sessionForRequest(request); ok && value.Username != "" {
		actor = value.Username
	}
	if actor == "" {
		actor = "api"
	}
	remoteIP := authn.ClientIP(request, s.trustedProxies)
	if k, ok := requestAPIKey(request); ok {
		actor = "key:" + k.ID
	}
	return core.AuditLogEntry{Actor: actor, Action: action, Target: target, Detail: detail, RemoteIP: remoteIP}
}

func (s *Server) writeAudit(ctx context.Context, entry core.AuditLogEntry) error {
	if s.auditWriter != nil {
		return s.auditWriter(ctx, entry)
	}
	if s.store == nil {
		return errors.New("audit store unavailable")
	}
	return s.store.RecordAudit(ctx, entry)
}

func (s *Server) recordAuditSync(request *http.Request, action, target, detail string) error {
	if s.store == nil && s.auditWriter == nil {
		return errors.New("audit store unavailable")
	}
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	return s.writeAudit(ctx, s.auditEntry(request, action, target, detail))
}
