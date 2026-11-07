package api

import (
	"errors"
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) getTrafficOrder(w http.ResponseWriter, request *http.Request) {
	order, err := s.store.ReadTrafficOrder(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) putTrafficOrder(w http.ResponseWriter, request *http.Request) {
	var input struct {
		IDs        []string `json:"ids"`
		Revision   *int64   `json:"revision"`
		Initialize bool     `json:"initialize"`
	}
	if err := decodeJSON(w, request, &input, 512<<10); err != nil || input.Revision == nil {
		writeError(w, http.StatusBadRequest, "端口顺序格式无效")
		return
	}
	order, err := s.store.SaveTrafficOrder(request.Context(), input.IDs, *input.Revision, input.Initialize)
	if errors.Is(err, store.ErrTrafficOrderConflict) {
		writeError(w, http.StatusConflict, "端口顺序已更新，请重试")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, order)
}
