package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

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

// Automatic page entry may reuse an already validated encrypted task snapshot.
// Deployment preflight deliberately omits prefer_cached. It is an early drift
// check, not an atomic compare-and-swap of the Agent's configuration.
const automaticConfigReadCacheTTL = 600 * time.Second

func (s *Server) createTask(w http.ResponseWriter, request *http.Request) {
	var input struct {
		core.TaskRequest
		PreferCached bool `json:"prefer_cached,omitempty"`
	}
	if err := decodeJSON(w, request, &input, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.PreferCached && input.Action != core.ActionReadConfig && input.Action != core.ActionReadManagedConfig {
		writeError(w, http.StatusBadRequest, "prefer_cached is only supported for configuration read tasks")
		return
	}
	if input.Action.SystemBBR() && !s.authorizeSystemBBR(w, request, input.AgentID) {
		return
	}
	if input.Action == core.ActionIPQuality && !s.authorizeIPQuality(w, request) {
		return
	}
	var task core.Task
	var err error
	if input.PreferCached {
		task, err = s.store.CreateTaskWithReadCache(request.Context(), input.TaskRequest, automaticConfigReadCacheTTL)
	} else {
		task, err = s.store.CreateTask(request.Context(), input.TaskRequest)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	if !(task.Reused && task.Status == core.TaskSucceeded) {
		s.recordAudit(request, "task.created", task.ID, string(task.Action)+" "+string(task.Engine)+" "+task.AgentID)
	}
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
	if previous.Action == core.ActionIPQuality && !s.authorizeIPQuality(w, request) {
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
