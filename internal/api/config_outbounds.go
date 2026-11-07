package api

import (
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"net/http"
)

func (s *Server) getManagedOutbound(w http.ResponseWriter, r *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, r)
	if !ok {
		return
	}
	config, err := s.store.AgentConfig(r.Context(), agent.ID, engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	fragment, err := serverconfig.ManagedOutboundFragment(engine, config.Content, r.PathValue("section"), r.PathValue("tag"))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, struct {
		Version  int    `json:"version"`
		Fragment string `json:"fragment"`
	}{config.Version, fragment})
}

func (s *Server) saveManagedOutbound(w http.ResponseWriter, r *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, r)
	if !ok {
		return
	}
	var input struct {
		Version   int         `json:"version"`
		Intent    core.Action `json:"intent"`
		Section   string      `json:"section"`
		Tag       string      `json:"tag"`
		Fragment  string      `json:"fragment"`
		Operation string      `json:"operation"`
	}
	if err := decodeJSON(w, r, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	config, err := s.store.AgentConfig(r.Context(), agent.ID, engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if config.Version != input.Version {
		writeStoreError(w, store.ErrConflict)
		return
	}
	state, err := s.store.ConfigManagementState(r.Context(), config.ID, config.Version)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	config.Content, state, err = serverconfig.MutateManagedOutbound(engine, config.Content, input.Section, input.Tag, input.Fragment, input.Operation, state)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if len(serverconfig.DiscoverTrafficPorts(engine, config.Content)) > 0 {
		plan, planErr := serverconfig.PlanPresetAccounting(engine, config.Content)
		if planErr != nil {
			writeError(w, 400, planErr.Error())
			return
		}
		config.Content = plan.Content
	}
	saved, task, err := s.store.SaveAgentConfigAndTask(r.Context(), config, input.Version, store.ConfigMutationOptions{Action: input.Intent, ManagementState: &state})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	s.recordAudit(r, "config.outbound.updated", saved.ID, string(engine))
	writeJSON(w, 200, configMutationResult{Config: saved, Task: task})
}
