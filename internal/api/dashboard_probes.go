package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Server) dashboardProbeRoutes(mux *http.ServeMux) {
	readPermissions := []core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead}
	writePermissions := []core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead, core.PermissionTasksExecute}
	mux.Handle("GET /api/v1/dashboard/probes", s.requireAllPermissions(readPermissions, http.HandlerFunc(s.listDashboardProbePairs)))
	mux.Handle("POST /api/v1/dashboard/probes", s.requireAllPermissions(writePermissions, http.HandlerFunc(s.createDashboardProbePair)))
	mux.Handle("PUT /api/v1/dashboard/probes/{id}", s.requireAllPermissions(writePermissions, http.HandlerFunc(s.updateDashboardProbePair)))
	mux.Handle("DELETE /api/v1/dashboard/probes/{id}", s.requireAllPermissions(writePermissions, http.HandlerFunc(s.deleteDashboardProbePair)))
	mux.Handle("POST /api/v1/dashboard/probes/{id}/run", s.requireAllPermissions(writePermissions, http.HandlerFunc(s.runDashboardProbePair)))
	mux.Handle("POST /api/v1/dashboard/probes/{id}/stop", s.requireAllPermissions(writePermissions, http.HandlerFunc(s.stopDashboardProbePair)))
}

func (s *Server) listDashboardProbePairs(w http.ResponseWriter, request *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, core.DashboardProbeListResponse{Pairs: []core.DashboardProbePair{}})
		return
	}
	pairs, err := s.store.ListDashboardProbePairs(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, core.DashboardProbeListResponse{Pairs: pairs})
}

func (s *Server) createDashboardProbePair(w http.ResponseWriter, request *http.Request) {
	var input core.DashboardProbePairRequest
	if err := decodeJSON(w, request, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pair, err := s.store.CreateDashboardProbePair(request.Context(), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "dashboard-probe.created", pair.ID, pair.Name)
	writeJSON(w, http.StatusCreated, pair)
}

func (s *Server) updateDashboardProbePair(w http.ResponseWriter, request *http.Request) {
	var input core.DashboardProbePairRequest
	if err := decodeJSON(w, request, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pair, err := s.store.UpdateDashboardProbePair(request.Context(), request.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "dashboard-probe.updated", pair.ID, pair.Name)
	writeJSON(w, http.StatusOK, pair)
}

func (s *Server) deleteDashboardProbePair(w http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if err := s.store.DeleteDashboardProbePair(request.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "dashboard-probe.deleted", id, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runDashboardProbePair(w http.ResponseWriter, request *http.Request) {
	pair, err := s.store.RunDashboardProbePair(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "dashboard-probe.started", pair.ID, pair.LastSessionID)
	writeJSON(w, http.StatusCreated, pair)
}

func (s *Server) stopDashboardProbePair(w http.ResponseWriter, request *http.Request) {
	pair, err := s.store.StopDashboardProbePair(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "dashboard-probe.stopped", pair.ID, pair.LastSessionID)
	writeJSON(w, http.StatusOK, pair)
}
