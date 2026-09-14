package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) listEnrollmentTokens(w http.ResponseWriter, request *http.Request) {
	tokens, err := s.store.ListEnrollmentTokens(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

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
