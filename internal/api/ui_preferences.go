package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) getUIPreferences(w http.ResponseWriter, r *http.Request) {
	preference, err := s.store.ReadUIPreferences(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preference)
}
func (s *Server) patchUIPreferences(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Values   map[string]map[string]json.RawMessage `json:"values"`
		Revision *int64                                `json:"revision"`
	}
	if err := decodeJSON(w, r, &input, 16384); err != nil || input.Revision == nil {
		writeError(w, http.StatusBadRequest, "界面偏好格式无效")
		return
	}
	preference, err := s.store.PatchUIPreferences(r.Context(), input.Values, *input.Revision)
	if errors.Is(err, store.ErrUIPreferencesConflict) {
		writeError(w, http.StatusConflict, "界面偏好已更新，请重试")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preference)
}
