package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

type mainlandAccessResource struct {
	AgentID                  string      `json:"agent_id"`
	AgentName                string      `json:"agent_name"`
	AgentStatus              string      `json:"agent_status"`
	ConfigID                 string      `json:"config_id"`
	ConfigVersion            int         `json:"config_version"`
	Engine                   core.Engine `json:"engine"`
	Tag                      string      `json:"tag"`
	Port                     int         `json:"port"`
	Kind                     string      `json:"kind"`
	BlockMainlandDestination bool        `json:"block_mainland_destination"`
	BlockMainlandSource      bool        `json:"block_mainland_source"`
}

func (s *Server) listMainlandAccessPolicies(w http.ResponseWriter, request *http.Request) {
	agents, err := s.store.ListAgents(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	configs, err := s.store.ListAgentConfigs(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	agentByID := make(map[string]core.Agent, len(agents))
	for _, agent := range agents {
		agentByID[agent.ID] = agent
	}
	result := make([]mainlandAccessResource, 0)
	for _, config := range configs {
		if config.Engine != core.EngineMihomo && config.Engine != core.EngineXray && config.Engine != core.EngineSingBox {
			continue
		}
		agent, exists := agentByID[config.AgentID]
		if !exists {
			continue
		}
		for _, policy := range serverconfig.DiscoverMainlandAccessPolicies(config.Engine, config.Content) {
			result = append(result, mainlandAccessResource{
				AgentID: agent.ID, AgentName: agent.Name, AgentStatus: agent.Status,
				ConfigID: config.ID, ConfigVersion: config.Version, Engine: config.Engine,
				Tag: policy.Tag, Port: policy.Port, Kind: policy.Kind,
				BlockMainlandDestination: policy.BlockMainlandDestination,
				BlockMainlandSource:      policy.BlockMainlandSource,
			})
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].AgentName != result[right].AgentName {
			return result[left].AgentName < result[right].AgentName
		}
		if result[left].Port != result[right].Port {
			return result[left].Port < result[right].Port
		}
		return result[left].Tag < result[right].Tag
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) putMainlandAccessPolicy(w http.ResponseWriter, request *http.Request) {
	var input struct {
		AgentID                  string      `json:"agent_id"`
		Engine                   core.Engine `json:"engine"`
		Tag                      string      `json:"tag"`
		Port                     int         `json:"port"`
		BlockMainlandDestination bool        `json:"block_mainland_destination"`
		BlockMainlandSource      bool        `json:"block_mainland_source"`
		ExpectedVersion          int         `json:"expected_version"`
		Intent                   string      `json:"intent"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.AgentID = strings.TrimSpace(input.AgentID)
	if input.Intent != "validate" && input.Intent != "deploy" {
		writeError(w, http.StatusBadRequest, "intent must be validate or deploy")
		return
	}
	if input.Engine != core.EngineMihomo && input.Engine != core.EngineXray && input.Engine != core.EngineSingBox {
		writeError(w, http.StatusBadRequest, "该内核暂不支持按入站限制大陆访问")
		return
	}
	agent, err := s.store.GetAgent(request.Context(), input.AgentID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	config, err := s.store.AgentConfig(request.Context(), agent.ID, input.Engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var prefixes []string
	if input.BlockMainlandDestination || input.BlockMainlandSource {
		prefixes, err = serverconfig.LoadChinaRoutes(request.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if input.Engine == core.EngineMihomo {
			prefixes = nil
		}
	}
	content, err := serverconfig.ApplyMainlandAccessPolicyWithPrefixes(input.Engine, config.Content, serverconfig.MainlandAccessPolicy{
		Tag: input.Tag, Port: input.Port, Engine: input.Engine,
		BlockMainlandDestination: input.BlockMainlandDestination,
		BlockMainlandSource:      input.BlockMainlandSource,
	}, prefixes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.store.SaveAgentConfig(request.Context(), core.Config{
		AgentID: config.AgentID, Name: config.Name, Description: config.Description,
		Engine: config.Engine, Content: content,
	}, input.ExpectedVersion)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task, ok := s.createConfigMutationTask(w, request, saved, input.Intent)
	if !ok {
		return
	}
	s.recordAudit(request, "agent_config.mainland_access_saved", saved.ID, input.Tag+" "+input.Intent)
	writeJSON(w, http.StatusOK, configMutationResult{Config: saved, Task: task})
}
