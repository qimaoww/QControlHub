package api

import (
	"errors"
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) getTheme(w http.ResponseWriter, r *http.Request) {
	preference, err := s.store.ReadTheme(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preference)
}

func (s *Server) putTheme(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ThemeID  string `json:"theme_id"`
		Revision *int64 `json:"revision"`
	}
	if err := decodeJSON(w, r, &input, 4096); err != nil || input.Revision == nil {
		writeError(w, http.StatusBadRequest, "主题选择格式无效")
		return
	}
	preference, err := s.store.SaveTheme(r.Context(), input.ThemeID, *input.Revision)
	if errors.Is(err, store.ErrThemeConflict) {
		writeError(w, http.StatusConflict, "主题选择已更新，请刷新后重试")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "account.theme.updated", "", input.ThemeID)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preference)
}
