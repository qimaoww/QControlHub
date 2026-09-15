package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) saveConfigField(w http.ResponseWriter, request *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, request)
	if !ok {
		return
	}
	key := strings.TrimSpace(request.PathValue("key"))
	inbound, scoped, scopeErr := configFieldInbound(request, engine)
	if scopeErr != nil {
		writeError(w, http.StatusBadRequest, scopeErr.Error())
		return
	}
	catalog, err := configschema.CatalogFor(engine)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	known := false
	for _, field := range catalog.Fields {
		if field.Key == key {
			known = true
			break
		}
	}
	if !known {
		writeError(w, http.StatusBadRequest, "unknown configuration field")
		return
	}
	var input struct {
		Mutation        string `json:"mutation"`
		Fragment        string `json:"fragment"`
		ExpectedVersion int    `json:"expected_version"`
		Name            string `json:"name"`
		Description     string `json:"description"`
		Intent          string `json:"intent"`
	}
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Intent != "validate" && input.Intent != "deploy" {
		writeError(w, http.StatusBadRequest, "intent must be validate or deploy")
		return
	}
	current, err := s.store.AgentConfig(request.Context(), agent.ID, engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if input.ExpectedVersion != current.Version {
		writeStoreError(w, store.ErrConflict)
		return
	}
	var present bool
	if scoped {
		_, present, _, err = serverconfig.SSRustInboundField(current.Content, inbound, key)
	} else {
		_, present, err = configschema.Fragment(engine, current.Content, key)
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	switch input.Mutation {
	case "add":
		if present {
			writeError(w, http.StatusConflict, "field already exists")
			return
		}
	case "modify", "delete":
		if !present {
			writeError(w, http.StatusConflict, "field does not exist")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "mutation must be add, modify, or delete")
		return
	}
	var content string
	managedAccounting := strings.Contains(current.Content, "qch-trf-") ||
		(engine == core.EngineShadowsocksRust && strings.Contains(current.Content, "outbound_fwmark"))
	mutationSource := current.Content
	if managedAccounting && engine == core.EngineShadowsocksRust {
		mutationSource, err = serverconfig.PresetAccountingSource(engine, current.Content)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if engine == core.EngineShadowsocksRust {
		if err := serverconfig.ValidateSSRustFieldValue(key, input.Fragment, input.Mutation == "delete"); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if scoped {
		content, err = serverconfig.MergeSSRustInboundField(mutationSource, inbound, key, input.Fragment, input.Mutation == "delete")
	} else {
		content, err = configschema.MergeFragment(engine, mutationSource, key, input.Fragment, input.Mutation == "delete")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if managedAccounting {
		if engine != core.EngineShadowsocksRust {
			content, err = serverconfig.AccountingUpdateSource(engine, content, current.Content)
		}
	}
	if err == nil && len(serverconfig.DiscoverTrafficPorts(engine, content)) > 0 {
		var plan serverconfig.AccountingPlan
		plan, err = serverconfig.PlanPresetAccounting(engine, content)
		content = plan.Content
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "字段修改无法保持独立出口归属，未保存："+err.Error())
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = current.Name
	}
	candidate := core.Config{AgentID: agent.ID, Name: name, Description: input.Description, Engine: engine, Content: content, Version: input.ExpectedVersion + 1}
	desired, err := s.planShadowsocksRustPolicies(request.Context(), candidate)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	saved, task, err := s.store.SaveAgentConfigAndTask(request.Context(), candidate, input.ExpectedVersion,
		store.ConfigMutationOptions{Action: core.Action(input.Intent), MainlandPolicies: desired})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	s.refreshSavedAgentTrafficMonitoring(request.Context(), saved.AgentID)
	s.recordAudit(request, "agent_config.field_saved", saved.ID, input.Mutation+" "+key+" "+agent.ID+" inbound="+inbound)
	writeJSON(w, http.StatusOK, configMutationResult{Config: saved, Task: task})
}

func (s *Server) getConfigField(w http.ResponseWriter, request *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, request)
	if !ok {
		return
	}
	key := strings.TrimSpace(request.PathValue("key"))
	inbound, scoped, scopeErr := configFieldInbound(request, engine)
	if scopeErr != nil {
		writeError(w, http.StatusBadRequest, scopeErr.Error())
		return
	}
	catalog, err := configschema.CatalogFor(engine)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	known := false
	for _, field := range catalog.Fields {
		if field.Key == key {
			known = true
			break
		}
	}
	if !known {
		writeError(w, http.StatusBadRequest, "unknown configuration field")
		return
	}
	config, err := s.store.AgentConfig(request.Context(), agent.ID, engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	fragment, present, err := configschema.Fragment(engine, config.Content, key)
	inherited := ""
	if scoped {
		fragment, present, inherited, err = serverconfig.SSRustInboundField(config.Content, inbound, key)
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "present": present, "fragment": fragment, "inherited_fragment": inherited, "version": config.Version})
}

// An empty/unsupported scope must never silently fall back to a global edit.
func configFieldInbound(request *http.Request, engine core.Engine) (string, bool, error) {
	values, scoped := request.URL.Query()["inbound"]
	if !scoped {
		return "", false, nil
	}
	if engine != core.EngineShadowsocksRust || len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", true, errors.New("a single SS Rust inbound must be selected")
	}
	return values[0], true, nil
}
