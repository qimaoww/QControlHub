package api

import (
	"net/http"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// The preset source editor uses the same all-or-nothing save/deploy contract
// as the builder. The general manual PUT endpoint remains a save-only API.
func (s *Server) savePresetSource(w http.ResponseWriter, request *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, request)
	if !ok {
		return
	}
	var input struct {
		core.Config
		Intent string `json:"intent"`
	}
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Intent != "validate" && input.Intent != "deploy" || input.Engine != "" && input.Engine != engine {
		writeError(w, http.StatusBadRequest, "invalid preset engine or intent")
		return
	}
	current, err := s.store.AgentConfig(request.Context(), agent.ID, engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if current.Version != input.Version {
		writeStoreError(w, store.ErrConflict)
		return
	}
	content := input.Content
	managed := strings.Contains(current.Content, "qch-trf-") || strings.Contains(content, "qch-trf-")
	if engine == core.EngineShadowsocksRust {
		managed = strings.Contains(current.Content, "outbound_fwmark")
	}
	if managed {
		content, err = serverconfig.AccountingUpdateSource(engine, content, current.Content)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if len(serverconfig.DiscoverTrafficPorts(engine, content)) > 0 {
		plan, err := serverconfig.PlanPresetAccounting(engine, content)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		content = plan.Content
	}
	candidate := input.Config
	candidate.AgentID, candidate.Engine, candidate.Content = agent.ID, engine, content
	candidate.Version++
	policies, err := s.planShadowsocksRustPolicies(request.Context(), candidate)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	saved, task, err := s.store.SaveAgentConfigAndTask(request.Context(), candidate, input.Version,
		store.ConfigMutationOptions{Action: core.Action(input.Intent), MainlandPolicies: policies})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	s.refreshSavedAgentTrafficMonitoring(request.Context(), agent.ID)
	s.recordAudit(request, "agent_config.source_saved", saved.ID, string(engine)+" "+agent.ID)
	writeJSON(w, http.StatusOK, configMutationResult{Config: saved, Task: task})
}
