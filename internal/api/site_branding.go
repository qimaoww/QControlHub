package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Server) getPublicBranding(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.SiteBranding(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Logo string `json:"logo"`
	}{value.Logo})
}

func (s *Server) siteBranding(w http.ResponseWriter, r *http.Request) {
	if role, ok := s.sessionRole(r); !ok || role != core.RoleAdmin {
		writeError(w, http.StatusForbidden, "仅站点管理员可管理全站 Logo")
		return
	}
	if r.Method == http.MethodGet {
		value, err := s.store.SiteBranding(r.Context())
		if err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
		return
	}
	var input struct {
		Logo     *string `json:"logo"`
		Revision int64   `json:"revision"`
	}
	if err := decodeJSON(w, r, &input, 360<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	value, err := s.store.SaveSiteBranding(r.Context(), input.Logo, input.Revision)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "settings.branding.saved", "", "api")
	writeJSON(w, http.StatusOK, value)
}
