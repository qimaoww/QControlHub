package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) importAgentConfig(w http.ResponseWriter, r *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, r)
	if !ok {
		return
	}
	var input store.ConfigImportRequest
	if err := decodeJSON(w, r, &input, 64<<10); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	task, err := s.store.CreateConfigImport(r.Context(), agent.ID, engine, input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "task.created", task.ID, "import-existing "+string(engine)+" "+agent.ID)
	writeJSON(w, 201, task)
}
