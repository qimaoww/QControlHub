package api

import (
	"errors"
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) getNodeView(w http.ResponseWriter, r *http.Request) {
	preference, err := s.store.ReadNodeView(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preference)
}

func (s *Server) putNodeView(w http.ResponseWriter, r *http.Request) {
	var input struct {
		View     string `json:"view"`
		Revision *int64 `json:"revision"`
	}
	if err := decodeJSON(w, r, &input, 4096); err != nil || input.Revision == nil {
		writeError(w, http.StatusBadRequest, "节点布局格式无效")
		return
	}
	preference, err := s.store.SaveNodeView(r.Context(), input.View, *input.Revision)
	if errors.Is(err, store.ErrNodeViewConflict) {
		writeError(w, http.StatusConflict, "节点布局已更新，请刷新后重试")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "account.node-view.updated", "", input.View)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preference)
}
