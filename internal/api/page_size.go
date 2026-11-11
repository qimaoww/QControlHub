package api

import (
	"errors"
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) getPageSize(w http.ResponseWriter, r *http.Request) {
	preference, err := s.store.ReadPageSize(r.Context(), r.PathValue("page"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preference)
}

func (s *Server) putPageSize(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PageSize int    `json:"page_size"`
		Revision *int64 `json:"revision"`
	}
	if err := decodeJSON(w, r, &input, 4096); err != nil || input.Revision == nil {
		writeError(w, http.StatusBadRequest, "每页条数格式无效")
		return
	}
	preference, err := s.store.SavePageSize(r.Context(), r.PathValue("page"), input.PageSize, *input.Revision)
	if errors.Is(err, store.ErrPageSizeConflict) {
		writeError(w, http.StatusConflict, "每页条数已更新，请刷新后重试")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "account.page-size.updated", "", r.PathValue("page"))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preference)
}
