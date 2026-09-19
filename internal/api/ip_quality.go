package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Server) ipQualityHistory(w http.ResponseWriter, request *http.Request) {
	date, timezone := request.URL.Query().Get("date"), request.URL.Query().Get("timezone")
	if timezone == "" {
		timezone = "UTC"
	}
	records, err := s.store.ListIPQualityRecords(request.Context(), date, timezone)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	schedules, err := s.store.ListIPQualitySchedules(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, core.IPQualityHistory{Date: date, Timezone: timezone, Records: records, Schedules: schedules})
}

func (s *Server) createIPQualityCheck(w http.ResponseWriter, request *http.Request) {
	var input struct {
		AgentID string `json:"agent_id"`
	}
	if err := decodeJSON(w, request, &input, 4<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task, err := s.store.CreateTask(request.Context(), core.TaskRequest{AgentID: input.AgentID, Action: core.ActionIPQuality})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	status := http.StatusCreated
	if task.Reused {
		status = http.StatusOK
	} else {
		s.recordAudit(request, "ip-quality.created", task.ID, task.AgentID)
	}
	writeJSON(w, status, task)
}

func (s *Server) putIPQualitySchedule(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(w, request, &input, 4<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	schedule, err := s.store.SetIPQualitySchedule(request.Context(), request.PathValue("id"), *input.Enabled)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "ip-quality.schedule.updated", schedule.AgentID, "")
	writeJSON(w, http.StatusOK, schedule)
}

func (s *Server) authorizeIPQuality(w http.ResponseWriter, request *http.Request) bool {
	role, _ := s.sessionRole(request)
	permissions, _ := s.sessionPermissions(request)
	if !role.Allows(core.PermissionAgentsManage) && !core.HasPermission(permissions, core.PermissionAgentsManage) {
		writeError(w, http.StatusForbidden, "IPQuality requires agents.manage and tasks.execute")
		return false
	}
	return true
}
