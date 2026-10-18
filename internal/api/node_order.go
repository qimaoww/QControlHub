package api

import (
	"errors"
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) getNodeOrder(w http.ResponseWriter, request *http.Request) {
	order, err := s.store.ReadNodeOrder(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) putNodeOrder(w http.ResponseWriter, request *http.Request) {
	var input struct {
		IDs        []string `json:"ids"`
		Revision   *int64   `json:"revision"`
		Initialize bool     `json:"initialize"`
	}
	if err := decodeJSON(w, request, &input, 512<<10); err != nil || input.Revision == nil {
		writeError(w, http.StatusBadRequest, "节点顺序格式无效")
		return
	}
	order, err := s.store.SaveNodeOrder(request.Context(), input.IDs, *input.Revision, input.Initialize)
	if errors.Is(err, store.ErrNodeOrderConflict) {
		writeError(w, http.StatusConflict, "节点顺序已更新，请重试")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, order)
}
