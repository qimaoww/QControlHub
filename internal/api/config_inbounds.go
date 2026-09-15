package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) newServerPlan(w http.ResponseWriter, request *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, request)
	if !ok {
		return
	}
	var input struct {
		Protocol string              `json:"protocol"`
		Input    *serverconfig.Input `json:"input"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	protocol, found := serverconfig.FindProtocol(engine, strings.TrimSpace(input.Protocol))
	if !found {
		writeError(w, http.StatusBadRequest, "unknown server inbound protocol")
		return
	}
	var (
		plan serverconfig.Input
		err  error
	)
	if input.Input == nil {
		plan, err = serverconfig.NewPlan(protocol)
		if err == nil && engine == core.EngineShadowsocksRust {
			config, configErr := s.store.AgentConfig(request.Context(), agent.ID, engine)
			if configErr == nil {
				plan = serverconfig.InheritShadowsocksRustGlobals(plan, config.Content)
			} else if !errors.Is(configErr, store.ErrNotFound) {
				writeStoreError(w, configErr)
				return
			}
		}
	} else {
		input.Input.Protocol = protocol.Key
		plan, err = serverconfig.RegeneratePlan(protocol, *input.Input)
	}
	if err != nil {
		if errors.Is(err, serverconfig.ErrInvalidPlanInput) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeInternalError(w, err)
		return
	}
	s.recordAudit(request, "agent_config.plan_created", agent.ID, string(engine)+" "+protocol.Key)
	writeJSON(w, http.StatusCreated, plan)
}

func (s *Server) saveServerInbound(w http.ResponseWriter, request *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, request)
	if !ok {
		return
	}
	var input struct {
		Operation             string             `json:"operation"`
		InstallIfMissing      bool               `json:"install_if_missing"`
		PreserveSSRustGlobals bool               `json:"preserve_ss_rust_globals"`
		OriginalTag           string             `json:"original_tag"`
		ExpectedVersion       int                `json:"expected_version"`
		Name                  string             `json:"name"`
		Description           string             `json:"description"`
		Intent                string             `json:"intent"`
		Input                 serverconfig.Input `json:"input"`
	}
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Operation != "add" && input.Operation != "modify" && input.Operation != "delete" {
		writeError(w, http.StatusBadRequest, "operation must be add, modify, or delete")
		return
	}
	if input.Intent != "validate" && input.Intent != "deploy" {
		writeError(w, http.StatusBadRequest, "intent must be validate or deploy")
		return
	}
	if input.InstallIfMissing && input.Operation != "add" {
		writeError(w, http.StatusBadRequest, "automatic installation is only supported when adding an inbound")
		return
	}
	if input.Operation == "add" && input.OriginalTag != "" {
		writeError(w, http.StatusBadRequest, "新增入站不能携带原入站标识")
		return
	}
	if _, found := serverconfig.FindProtocol(engine, input.Input.Protocol); !found && input.Operation != "delete" {
		writeError(w, http.StatusBadRequest, "unknown server inbound protocol")
		return
	}
	// Validate local parameters before any DNS/TLS I/O. Invalid credentials
	// should fail immediately rather than waiting for the camouflage target.
	var generated string
	var err error
	if input.Operation != "delete" {
		generated, err = serverconfig.Generate(engine, input.Input)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	// Reject already-stale drafts before network probes or full-config
	// planning. The transactional save still checks again to close races.
	current, currentErr := s.store.AgentConfig(request.Context(), agent.ID, engine)
	if currentErr != nil && !errors.Is(currentErr, store.ErrNotFound) {
		writeStoreError(w, currentErr)
		return
	}
	if input.ExpectedVersion != current.Version {
		writeStoreError(w, store.ErrConflict)
		return
	}
	if errors.Is(currentErr, store.ErrNotFound) && input.Operation != "add" {
		writeError(w, http.StatusConflict, "no saved configuration exists for this operation")
		return
	}
	content := generated
	var existingShadowsocksRustPolicies []core.MainlandAccessPolicy
	if engine == core.EngineShadowsocksRust {
		existingShadowsocksRustPolicies, err = s.store.ListMainlandAccessPolicies(request.Context(), agent.ID)
		if err != nil {
			writeInternalError(w, err)
			return
		}
	}
	if currentErr == nil {
		current.Content, err = serverconfig.PresetAccountingSource(engine, current.Content)
		if err != nil {
			writeError(w, http.StatusBadRequest, "无法安全更新独立出口配置："+err.Error())
			return
		}
		if input.Operation == "delete" {
			content, err = serverconfig.DeletePresetInbound(engine, current.Content, input.OriginalTag)
		} else if engine == core.EngineShadowsocksRust && input.PreserveSSRustGlobals {
			content, err = serverconfig.MutateSSRustPort(current.Content, generated, input.OriginalTag, input.Operation)
		} else {
			content, err = serverconfig.MutateGenerated(engine, current.Content, generated, input.OriginalTag, input.Operation)
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if engine == core.EngineShadowsocksRust && input.PreserveSSRustGlobals {
		content, err = serverconfig.MutateGenerated(engine, "{}", generated, "", "add")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if engine == core.EngineShadowsocksRust && input.Operation != "delete" && input.Input.BlockMainlandDestination && len(serverconfig.DiscoverMainlandAccessPolicies(engine, content)) > 1 {
		writeError(w, http.StatusBadRequest, "Shadowsocks Rust 的 ACL 由单个 ssserver 进程统一加载；多端口配置请拆分为单独进程后再启用目标限制")
		return
	}
	if engine == core.EngineMihomo || engine == core.EngineXray || engine == core.EngineSingBox || engine == core.EngineShadowsocksRust {
		if currentErr == nil && input.OriginalTag != "" {
			if engine != core.EngineShadowsocksRust {
				content, err = serverconfig.RemoveMainlandAccessPolicy(engine, content, input.OriginalTag)
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
			}
		}
		if input.Operation != "delete" && engine != core.EngineShadowsocksRust {
			content, err = serverconfig.ApplyMainlandAccessPolicyWithPrefixes(engine, content, serverconfig.MainlandAccessPolicy{
				Tag: input.Input.Tag, Port: input.Input.Port, Engine: engine,
				BlockMainlandDestination: input.Input.BlockMainlandDestination,
				BlockMainlandSource:      input.Input.BlockMainlandSource,
			}, nil)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}
	if input.Operation != "add" {
		next := input.Input.Tag
		if input.Operation == "delete" {
			next = ""
		}
		content, err = serverconfig.ReconcilePresetInboundReferences(engine, content, input.OriginalTag, next)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	// Reject unsupported attribution before saving a version or queuing a task.
	// Deleting the last inbound must remain possible.
	if len(serverconfig.DiscoverTrafficPorts(engine, content)) > 0 {
		plan, err := serverconfig.PlanPresetAccounting(engine, content)
		if err != nil {
			writeError(w, http.StatusBadRequest, "预设配置无法建立独立出口归属，未保存或部署："+err.Error())
			return
		}
		content = plan.Content
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = agent.Name + " · " + string(engine)
	}
	var clientMetadata string
	if input.Operation != "delete" {
		clientMetadata, err = serverconfig.MarshalClientMetadata(input.Input)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	metadata := store.ConfigClientMetadataMutation{
		OriginalTag: input.OriginalTag, Tag: input.Input.Tag, Content: clientMetadata, Delete: input.Operation == "delete",
	}
	if metadata.Delete {
		metadata.Tag = ""
	}
	var desired []core.MainlandAccessPolicy
	if engine == core.EngineShadowsocksRust {
		entries := serverconfig.DiscoverMainlandAccessPolicies(engine, content)
		destination := false
		for _, policy := range existingShadowsocksRustPolicies {
			destination = destination || policy.BlockMainlandDestination
		}
		canonicalTag := input.Input.Tag
		applySelection := input.Operation != "delete"
		if applySelection {
			if input.Operation != "add" || len(entries) == 1 {
				destination = input.Input.BlockMainlandDestination
			}
			for _, entry := range entries {
				if entry.Port == input.Input.Port && (entry.Tag == canonicalTag || len(entries) == 1) {
					canonicalTag = entry.Tag
					break
				}
			}
		}
		if destination && len(entries) > 1 {
			writeError(w, http.StatusBadRequest, "当前 SS Rust 已启用进程级目标限制，请先关闭目标限制或拆分进程后再新增端口")
			return
		}
		desired = reconcileShadowsocksRustPolicies(entries, existingShadowsocksRustPolicies, agent.ID, input.ExpectedVersion+1,
			destination, canonicalTag, input.Input.Port, input.Input.BlockMainlandSource, applySelection)
	}
	// Finish local identity, port, routing and policy checks before spending
	// time on DNS/TLS. A duplicate inbound should fail immediately.
	if input.Input.RealityEnabled && input.Operation != "delete" {
		target, err := serverconfig.ProbeRealityTarget(request.Context(), input.Input.RealityServerName)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if input.Input.RealityMLDSA65Seed != "" {
			if err := serverconfig.ValidateRealityMLDSATarget(target); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}
	saved, task, err := s.store.SaveAgentConfigAndTask(request.Context(), core.Config{
		AgentID: agent.ID, Name: name, Description: input.Description, Engine: engine, Content: content,
	}, input.ExpectedVersion, store.ConfigMutationOptions{
		Action: core.Action(input.Intent), ClientMetadata: &metadata, MainlandPolicies: desired,
		InstallIfMissing: input.InstallIfMissing && !agent.Runtime[engine].Installed,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	s.refreshSavedAgentTrafficMonitoring(request.Context(), saved.AgentID)
	s.recordAudit(request, "agent_config.server_saved", saved.ID, input.Operation+" "+input.Input.Protocol+" "+agent.ID)
	writeJSON(w, http.StatusOK, configMutationResult{Config: saved, Task: task})
}
