package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) getInboundSource(w http.ResponseWriter, r *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, r)
	if !ok {
		return
	}
	config, err := s.store.AgentConfig(r.Context(), agent.ID, engine)
	snapshot := r.URL.Query().Get("snapshot")
	if err != nil && !(errors.Is(err, store.ErrNotFound) && snapshot != "") {
		writeStoreError(w, err)
		return
	}
	if snapshot != "" {
		if !s.sessionAllows(r, core.PermissionTasksRead) {
			writeError(w, http.StatusForbidden, "没有读取配置快照的权限")
			return
		}
		config.Content, err = s.store.ReadTaskConfigSnapshot(r.Context(), snapshot, agent.ID, engine)
		if err != nil {
			writeStoreError(w, err)
			return
		}
	}
	fragment, _, err := serverconfig.InboundSource(engine, config.Content, r.PathValue("tag"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": config.Version, "fragment": fragment})
}

func (s *Server) saveInboundSource(w http.ResponseWriter, r *http.Request, current core.Config, source, tag, fragment, intent string, expectedInstanceID *string, installIfMissing bool) {
	content, err := serverconfig.ReplaceInboundSource(current.Engine, source, tag, fragment)
	if err == nil && (strings.Contains(current.Content, "qch-trf-") || strings.Contains(content, "qch-trf-") || current.Engine == core.EngineShadowsocksRust && strings.Contains(current.Content, "outbound_fwmark")) {
		content, err = s.accountingUpdateSource(r.Context(), current, content)
	}
	if err == nil && len(serverconfig.DiscoverTrafficPorts(current.Engine, content)) > 0 {
		var plan serverconfig.AccountingPlan
		plan, err = serverconfig.PlanPresetAccounting(current.Engine, content)
		content = plan.Content
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	candidate := current
	candidate.Content, candidate.Version = content, current.Version+1
	policies, err := s.planShadowsocksRustPolicies(r.Context(), candidate)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Retain detached client/TLS details. Existing identity checks decide whether
	// they still apply to the edited native endpoint, peer and transport.
	saved, task, err := s.store.SaveAgentConfigAndTask(r.Context(), candidate, current.Version,
		store.ConfigMutationOptions{InstallIfMissing: installIfMissing, ExpectedInstanceID: expectedInstanceID, TaskID: r.Header.Get("X-Config-Task-ID"), Action: core.Action(intent), MainlandPolicies: policies})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	s.refreshSavedAgentTrafficMonitoring(r.Context(), saved.AgentID)
	s.recordAudit(r, "agent_config.inbound_source_saved", saved.ID, string(saved.Engine)+" "+saved.AgentID)
	writeJSON(w, http.StatusOK, configMutationResult{Config: saved, Task: task})
}
