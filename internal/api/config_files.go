package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// File edits form one encrypted, versioned configuration transaction. There is
// no endpoint accepting a node filesystem path and no partial revision save.
func (s *Server) getAgentConfigFiles(w http.ResponseWriter, r *http.Request) {
	engine, err := core.ParseEngine(r.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.store.AgentConfig(r.Context(), r.PathValue("id"), engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	files, err := serverconfig.SplitConfigFiles(engine, config.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Version int                       `json:"version"`
		Files   []serverconfig.ConfigFile `json:"files"`
	}{config.Version, files})
}

func (s *Server) putAgentConfigFiles(w http.ResponseWriter, r *http.Request) {
	engine, err := core.ParseEngine(r.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		Name        string                    `json:"name"`
		Description string                    `json:"description"`
		Version     int                       `json:"version"`
		Files       []serverconfig.ConfigFile `json:"files"`
	}
	if err := decodeJSON(w, r, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	content, err := serverconfig.MergeConfigFiles(engine, input.Files)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.saveAgentConfigResponse(w, r, core.Config{AgentID: r.PathValue("id"), Engine: engine, Name: input.Name, Description: input.Description, Version: input.Version, Content: content})
}
