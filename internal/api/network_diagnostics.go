package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Server) diagnosticRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/network-diagnostics/nodes", s.requireAllPermissions([]core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead, core.PermissionTasksExecute}, http.HandlerFunc(s.diagnosticNodes)))
	mux.Handle("GET /api/v1/network-diagnostics", s.requireAllPermissions([]core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead, core.PermissionTasksExecute}, http.HandlerFunc(s.listDiagnostics)))
	mux.Handle("POST /api/v1/network-diagnostics", s.requireAllPermissions([]core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead, core.PermissionTasksExecute}, http.HandlerFunc(s.createDiagnostic)))
	mux.Handle("GET /api/v1/network-diagnostics/{id}", s.requireAllPermissions([]core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead, core.PermissionTasksExecute}, http.HandlerFunc(s.getDiagnostic)))
	mux.Handle("POST /api/v1/network-diagnostics/{id}/stop", s.requireAllPermissions([]core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead, core.PermissionTasksExecute}, http.HandlerFunc(s.stopDiagnostic)))
	mux.Handle("GET /api/v1/agents/{id}/diagnostic-grants", s.requirePermission(core.PermissionAgentsManage, http.HandlerFunc(s.diagnosticGrants)))
	mux.Handle("PUT /api/v1/agents/{id}/diagnostic-grants/{share}", s.requirePermission(core.PermissionAgentsManage, http.HandlerFunc(s.setDiagnosticGrant)))
}
func (s *Server) diagnosticNodes(w http.ResponseWriter, r *http.Request) {
	nodes, e := s.store.DiagnosticNodes(r.Context())
	if e != nil {
		writeStoreError(w, e)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}
func (s *Server) listDiagnostics(w http.ResponseWriter, r *http.Request) {
	tool := r.URL.Query().Get("tool")
	switch tool {
	case "", "ping", "traceroute", "mtr", "iperf3":
	default:
		writeError(w, http.StatusBadRequest, "不支持的诊断工具")
		return
	}
	items, e := s.store.ListDiagnosticsForTool(r.Context(), tool)
	if e != nil {
		writeStoreError(w, e)
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *Server) createDiagnostic(w http.ResponseWriter, r *http.Request) {
	var input core.DiagnosticRequest
	if e := decodeJSON(w, r, &input, 16<<10); e != nil {
		writeError(w, http.StatusBadRequest, e.Error())
		return
	}
	d, e := s.store.CreateDiagnostic(r.Context(), input)
	if e != nil {
		writeStoreError(w, e)
		return
	}
	s.recordAudit(r, "network-diagnostic.created", d.ID, d.Request.Parameters.Tool)
	writeJSON(w, http.StatusCreated, d)
}
func (s *Server) getDiagnostic(w http.ResponseWriter, r *http.Request) {
	d, e := s.store.GetDiagnostic(r.Context(), r.PathValue("id"))
	if e != nil {
		writeStoreError(w, e)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
func (s *Server) stopDiagnostic(w http.ResponseWriter, r *http.Request) {
	if e := s.store.StopDiagnostic(r.Context(), r.PathValue("id")); e != nil {
		writeStoreError(w, e)
		return
	}
	s.recordAudit(r, "network-diagnostic.stop", r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) diagnosticGrants(w http.ResponseWriter, r *http.Request) {
	d, e := s.store.DiagnosticGrants(r.Context(), r.PathValue("id"))
	if e != nil {
		writeStoreError(w, e)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
func (s *Server) setDiagnosticGrant(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled  *bool `json:"enabled"`
		Revision int64 `json:"revision"`
	}
	if e := decodeJSON(w, r, &input, 1024); e != nil || input.Enabled == nil || input.Revision < 0 {
		writeError(w, http.StatusBadRequest, "请明确授权状态")
		return
	}
	if e := s.store.SetDiagnosticGrant(r.Context(), r.PathValue("id"), r.PathValue("share"), *input.Enabled, input.Revision); e != nil {
		writeStoreError(w, e)
		return
	}
	s.recordAudit(r, "network-diagnostic.grant", r.PathValue("share"), "")
	w.WriteHeader(http.StatusNoContent)
}
