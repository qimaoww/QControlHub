package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Server) listUsers(w http.ResponseWriter, request *http.Request) {
	if s.store == nil {
		writeInternalError(w, errors.New("user store is unavailable"))
		return
	}
	users, err := s.store.ListUsers(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) createUser(w http.ResponseWriter, request *http.Request) {
	if s.store == nil {
		writeInternalError(w, errors.New("user store is unavailable"))
		return
	}
	var input core.UserRequest
	if err := decodeJSON(w, request, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateUserRequest(&input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	normalizedPermissions := core.NormalizePermissions(input.Permissions)
	if len(normalizedPermissions) != len(input.Permissions) {
		writeError(w, http.StatusBadRequest, "invalid user permission")
		return
	}
	input.Permissions = normalizedPermissions
	if input.Role == core.RoleAdmin {
		input.Permissions = core.AllPermissions()
	}
	hash, err := authn.HashPassword(input.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := s.store.CreateUser(request.Context(), input, hash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "user.created", user.ID, user.Username+" "+string(user.Role))
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) updateUser(w http.ResponseWriter, request *http.Request) {
	if s.store == nil {
		writeInternalError(w, errors.New("user store is unavailable"))
		return
	}
	var input core.UserUpdate
	if err := decodeJSON(w, request, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.DisplayName == nil && input.Role == nil && input.Password == nil && input.Disabled == nil {
		writeError(w, http.StatusBadRequest, "at least one user field must be provided")
		return
	}
	if input.Role != nil && !input.Role.Valid() {
		writeError(w, http.StatusBadRequest, "invalid user role")
		return
	}
	if input.Permissions != nil {
		normalized := core.NormalizePermissions(*input.Permissions)
		if len(normalized) != len(*input.Permissions) {
			writeError(w, http.StatusBadRequest, "invalid user permission")
			return
		}
		if input.Role != nil && *input.Role == core.RoleAdmin {
			normalized = core.AllPermissions()
		}
		input.Permissions = &normalized
	}
	if input.DisplayName != nil && utf8.RuneCountInString(strings.TrimSpace(*input.DisplayName)) > 100 {
		writeError(w, http.StatusBadRequest, "display name must be at most 100 characters")
		return
	}
	passwordHash := ""
	if input.Password != nil {
		hash, err := authn.HashPassword(*input.Password)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		passwordHash = hash
	}
	if input.Disabled != nil && *input.Disabled && s.sessionUserID(request) == request.PathValue("id") {
		writeError(w, http.StatusConflict, "cannot disable the current user session")
		return
	}
	user, err := s.store.UpdateUser(request.Context(), request.PathValue("id"), input, passwordHash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "user.updated", user.ID, user.Username)
	s.revokeUserSessions(user.ID)
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) deleteUser(w http.ResponseWriter, request *http.Request) {
	if s.store == nil {
		writeInternalError(w, errors.New("user store is unavailable"))
		return
	}
	if s.sessionUserID(request) == request.PathValue("id") {
		writeError(w, http.StatusConflict, "cannot disable the current user session")
		return
	}
	user, err := s.store.SetUserDisabled(request.Context(), request.PathValue("id"), true)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "user.disabled", user.ID, user.Username)
	s.revokeUserSessions(user.ID)
	w.WriteHeader(http.StatusNoContent)
}

func validateUserRequest(input *core.UserRequest) error {
	username, err := authn.NormalizeUsername(input.Username)
	if err != nil {
		return err
	}
	input.Username = username
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if utf8.RuneCountInString(input.DisplayName) > 100 {
		return &userInputError{"display name must be at most 100 characters"}
	}
	if !input.Role.Valid() {
		return &userInputError{"invalid user role"}
	}
	return authn.ValidatePassword(input.Password)
}

type userInputError struct{ message string }

func (e *userInputError) Error() string { return e.message }
