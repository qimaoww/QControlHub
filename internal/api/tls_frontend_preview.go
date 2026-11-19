package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) previewTLSFrontend(w http.ResponseWriter, r *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, r)
	if !ok {
		return
	}
	var input store.TLSFrontendSelection
	if err := decodeJSON(w, r, &input, 4096); err != nil {
		writeError(w, 400, "TLS 前端预览参数无效")
		return
	}
	preview, err := s.store.PreviewTLSFrontend(r.Context(), agent, engine, r.PathValue("tag"), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}
