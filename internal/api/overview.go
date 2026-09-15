package api

import (
	"net/http"
)

func (s *Server) overview(w http.ResponseWriter, request *http.Request) {
	result, err := s.store.Overview(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
