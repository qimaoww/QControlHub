package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
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
	resourceIndex := make(map[string]int)
	for _, config := range configs {
		if config.Engine != core.EngineMihomo && config.Engine != core.EngineXray && config.Engine != core.EngineSingBox && config.Engine != core.EngineShadowsocksRust {
			continue
		}
		agent, exists := agentByID[config.AgentID]
		if !exists {
			continue
		}
		for _, policy := range serverconfig.DiscoverMainlandAccessPolicies(config.Engine, config.Content) {
			policy.ConfigVersion = config.Version
			resource := mainlandAccessResource{
				AgentID: agent.ID, AgentName: agent.Name, AgentStatus: agent.Status,
				ConfigID: config.ID, ConfigVersion: config.Version, Engine: config.Engine,
				Tag: policy.Tag, Port: policy.Port, Kind: policy.Kind,
				BlockMainlandDestination: policy.BlockMainlandDestination,
				BlockMainlandSource:      policy.BlockMainlandSource,
			}
			resourceIndex[mainlandPolicyKey(resource.AgentID, resource.Engine, resource.Tag, resource.Port)] = len(result)
			result = append(result, resource)
		}
	}
	agentPolicies, err := s.store.ListMainlandAccessPolicies(request.Context(), "")
	if err != nil {
		writeInternalError(w, err)
		return
	}
	for _, policy := range agentPolicies {
		agent, exists := agentByID[policy.AgentID]
		if !exists {
			continue
		}
		resource := mainlandAccessResource{
			AgentID: policy.AgentID, AgentName: agent.Name, AgentStatus: agent.Status,
			Engine: policy.Engine, Tag: policy.Tag, Port: policy.Port, Kind: policy.Kind,
			BlockMainlandDestination: policy.BlockMainlandDestination,
			BlockMainlandSource:      policy.BlockMainlandSource,
		}
		if index, exists := resourceIndex[mainlandPolicyKey(resource.AgentID, resource.Engine, resource.Tag, resource.Port)]; exists {
			result[index].BlockMainlandDestination = resource.BlockMainlandDestination
			result[index].BlockMainlandSource = resource.BlockMainlandSource
		} else {
			resourceIndex[mainlandPolicyKey(resource.AgentID, resource.Engine, resource.Tag, resource.Port)] = len(result)
			result = append(result, resource)
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

func mainlandPolicyKey(agentID string, engine core.Engine, tag string, port int) string {
	return agentID + "\x00" + string(engine) + "\x00" + tag + "\x00" + strconv.Itoa(port)
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
	if input.Engine != core.EngineMihomo && input.Engine != core.EngineXray && input.Engine != core.EngineSingBox && input.Engine != core.EngineShadowsocksRust {
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
	if input.Engine == core.EngineShadowsocksRust {
		entries := serverconfig.DiscoverMainlandAccessPolicies(input.Engine, config.Content)
		if input.BlockMainlandDestination && len(entries) > 1 {
			writeError(w, http.StatusBadRequest, "Shadowsocks Rust 的 ACL 由单个 ssserver 进程统一加载；多端口配置请拆分为单独进程后再启用目标限制")
			return
		}
		existingPolicies, err := s.store.ListMainlandAccessPolicies(request.Context(), agent.ID)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		var kind string
		canonicalTag := strings.TrimSpace(input.Tag)
		for _, entry := range entries {
			if entry.Port == input.Port && (entry.Tag == canonicalTag || len(entries) == 1) {
				kind = entry.Kind
				canonicalTag = entry.Tag
				break
			}
		}
		if kind == "" {
			writeError(w, http.StatusBadRequest, "当前配置中不存在匹配的入站标签和端口")
			return
		}
		saved, err := s.store.SaveAgentConfig(request.Context(), core.Config{
			AgentID: config.AgentID, Name: config.Name, Description: config.Description,
			Engine: config.Engine, Content: config.Content,
		}, input.ExpectedVersion)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		desired := reconcileShadowsocksRustPolicies(entries, existingPolicies, agent.ID, saved.Version,
			input.BlockMainlandDestination, canonicalTag, input.Port, input.BlockMainlandSource, true)
		if err := s.store.ReplaceMainlandAccessPolicies(request.Context(), agent.ID, saved.Version, desired); err != nil {
			writeStoreError(w, err)
			return
		}
		task, ok := s.createConfigMutationTask(w, request, saved, input.Intent)
		if !ok {
			return
		}
		s.recordAudit(request, "agent_config.mainland_access_saved", saved.ID, canonicalTag+" "+input.Intent)
		writeJSON(w, http.StatusOK, configMutationResult{Config: saved, Task: task})
		return
	}
	content, err := serverconfig.ApplyMainlandAccessPolicyWithPrefixes(input.Engine, config.Content, serverconfig.MainlandAccessPolicy{
		Tag: input.Tag, Port: input.Port, Engine: input.Engine,
		BlockMainlandDestination: input.BlockMainlandDestination,
		BlockMainlandSource:      input.BlockMainlandSource,
	}, nil)
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

func reconcileShadowsocksRustPolicies(entries []serverconfig.MainlandAccessPolicy, existing []core.MainlandAccessPolicy, agentID string, version int,
	destination bool, selectedTag string, selectedPort int, selectedSource bool, applySelection bool,
) []core.MainlandAccessPolicy {
	existingByKey := make(map[string]core.MainlandAccessPolicy, len(existing))
	for _, policy := range existing {
		existingByKey[mainlandPolicyKey(agentID, core.EngineShadowsocksRust, policy.Tag, policy.Port)] = policy
	}
	result := make([]core.MainlandAccessPolicy, 0, len(entries))
	for _, entry := range entries {
		policy := existingByKey[mainlandPolicyKey(agentID, core.EngineShadowsocksRust, entry.Tag, entry.Port)]
		source := policy.BlockMainlandSource
		if applySelection && entry.Tag == selectedTag && entry.Port == selectedPort {
			source = selectedSource
		}
		if !destination && !source {
			continue
		}
		result = append(result, core.MainlandAccessPolicy{
			AgentID: agentID, ConfigVersion: version, Tag: entry.Tag, Port: entry.Port, Kind: entry.Kind,
			Engine: core.EngineShadowsocksRust, BlockMainlandDestination: destination, BlockMainlandSource: source,
		})
	}
	return result
}

func (s *Server) reconcileSavedShadowsocksRustPolicies(ctx context.Context, saved core.Config) error {
	if saved.Engine != core.EngineShadowsocksRust {
		return nil
	}
	existing, err := s.store.ListMainlandAccessPolicies(ctx, saved.AgentID)
	if err != nil {
		return err
	}
	destination := false
	for _, policy := range existing {
		destination = destination || policy.BlockMainlandDestination
	}
	entries := serverconfig.DiscoverMainlandAccessPolicies(saved.Engine, saved.Content)
	desired := reconcileShadowsocksRustPolicies(entries, existing, saved.AgentID, saved.Version, destination, "", 0, false, false)
	return s.store.ReplaceMainlandAccessPolicies(ctx, saved.AgentID, saved.Version, desired)
}
