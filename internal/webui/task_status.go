package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type taskStatusResponse struct {
	ID           string          `json:"id"`
	Status       core.TaskStatus `json:"status"`
	Simulated    bool            `json:"simulated,omitempty"`
	Action       core.Action     `json:"action"`
	Engine       core.Engine     `json:"engine"`
	Attempt      int             `json:"attempt"`
	Timing       string          `json:"timing"`
	HasResult    bool            `json:"has_result"`
	TasksActive  int             `json:"tasks_active"`
	TasksQueued  int             `json:"tasks_queued"`
	TasksRunning int             `json:"tasks_running"`
}

func (s *Server) taskStatus(w http.ResponseWriter, request *http.Request) {
	task, err := s.store.GetTask(request.Context(), request.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if err != nil {
		s.renderDatabaseError(w, err)
		return
	}
	overview, err := s.store.Overview(request.Context())
	if err != nil {
		s.renderDatabaseError(w, err)
		return
	}
	response := taskStatusSnapshot(task, overview)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(response)
}

func taskStatusSnapshot(task core.Task, overview core.Overview) taskStatusResponse {
	return taskStatusResponse{
		ID: task.ID, Status: task.Status, Simulated: task.Simulated, Action: task.Action, Engine: task.Engine, Attempt: task.Attempt,
		Timing: taskTiming(task), HasResult: task.Output != "" || task.Error != "",
		TasksActive: overview.TasksPending, TasksQueued: overview.TasksQueued, TasksRunning: overview.TasksRunning,
	}
}
