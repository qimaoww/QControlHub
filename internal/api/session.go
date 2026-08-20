package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	apiSessionCookie    = "__Host-qcontrolhub_api_session"
	apiDevSessionCookie = "qcontrolhub_api_session_dev"
	csrfHeader          = "X-QControlHub-CSRF"
)

type apiSession struct {
	CSRF      string
	ExpiresAt time.Time
	Role      core.Role
	UserID    string
	Username  string
}

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
	secret := strings.TrimSpace(input.Token)
	role, ok := core.Role(""), false
	userID := ""
	if username != "" && secret != "" && s.store != nil {
		if user, hash, err := s.store.UserForLogin(request.Context(), strings.ToLower(username)); err == nil && authn.CheckPassword(hash, secret) {
			role, ok, userID = user.Role, true, user.ID
			username = user.Username
		}
	}
	// Keep the environment token as a break-glass administrator credential.
	// It remains compatible with existing installations while durable users
	// provide named access for day-to-day panel work.
	if !ok && (username == "" || strings.EqualFold(username, "admin")) {
		role, ok = s.roleForToken(secret)
	}
	if !ok {
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
	s.sessions[token] = apiSession{CSRF: csrf, ExpiresAt: expires, Role: role, UserID: userID, Username: username}
	s.sessionsMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: token, Path: "/", Expires: expires,
		MaxAge: int(s.sessionTTL.Seconds()), HttpOnly: true, Secure: s.secureTransport,
		SameSite: http.SameSiteStrictMode,
	})
	s.recordAudit(request, "login.succeeded", "", string(role))
	w.Header().Set("Cache-Control", "no-store")
	if userID != "" && s.store != nil {
		_ = s.store.RecordUserLogin(request.Context(), userID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"role": role, "user_id": userID, "username": username, "csrf_token": csrf, "expires_at": expires})
}

func (s *Server) session(w http.ResponseWriter, request *http.Request) {
	value, ok := s.sessionForRequest(request)
	if !ok {
		writeError(w, http.StatusUnauthorized, "session expired")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"role": value.Role, "user_id": value.UserID, "username": value.Username, "csrf_token": value.CSRF, "expires_at": value.ExpiresAt})
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

func (s *Server) pruneSessionsLocked(now time.Time) {
	for key, value := range s.sessions {
		if !value.ExpiresAt.After(now) {
			delete(s.sessions, key)
		}
	}
}

func (s *Server) sessionRole(request *http.Request) (core.Role, bool) {
	if token := bearerToken(request); token != "" {
		return s.roleForToken(token)
	}
	value, ok := s.sessionForRequest(request)
	return value.Role, ok
}

func (s *Server) sessionUserID(request *http.Request) string {
	if bearerToken(request) != "" {
		return ""
	}
	value, ok := s.sessionForRequest(request)
	if !ok {
		return ""
	}
	return value.UserID
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
	if s.store == nil {
		return
	}
	role, _ := s.sessionRole(request)
	actor := string(role)
	if value, ok := s.sessionForRequest(request); ok && value.Username != "" {
		actor = value.Username
	}
	if actor == "" {
		actor = "api"
	}
	remoteIP := authn.ClientIP(request, s.trustedProxies)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.store.RecordAudit(ctx, core.AuditLogEntry{Actor: actor, Action: action, Target: target, Detail: detail, RemoteIP: remoteIP})
	}()
}
