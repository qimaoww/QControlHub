package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type clientAccessProfile struct {
	Tag            string                     `json:"tag"`
	Protocol       string                     `json:"protocol"`
	Port           int                        `json:"port"`
	Profile        serverconfig.ClientProfile `json:"profile"`
	ClientName     string                     `json:"client_name,omitempty"`
	NameOverridden bool                       `json:"name_overridden,omitempty"`
	// Address, AddressMode, and AddressOverridden describe this listening
	// endpoint only; they never inherit another port's override.
	Address           string `json:"address,omitempty"`
	AddressMode       string `json:"address_mode,omitempty"`
	AddressOverridden bool   `json:"address_overridden,omitempty"`
}

type clientAccessAddressOption struct {
	Address  string                `json:"address"`
	Source   string                `json:"source"`
	Family   string                `json:"family"`
	Profiles []clientAccessProfile `json:"profiles"`
}

type clientAccessEntry struct {
	AgentID         string                      `json:"agent_id"`
	ConfigID        string                      `json:"config_id"`
	AgentName       string                      `json:"agent_name"`
	AgentStatus     string                      `json:"agent_status"`
	Engine          core.Engine                 `json:"engine"`
	Address         string                      `json:"address"`
	Source          string                      `json:"source"`
	AddressRequired bool                        `json:"address_required,omitempty"`
	Profiles        []clientAccessProfile       `json:"profiles"`
	AddressOptions  []clientAccessAddressOption `json:"address_options,omitempty"`
	AddressMode     string                      `json:"address_mode"`
}

type clientAccessDataSource interface {
	ListAgents(context.Context) ([]core.Agent, error)
	DeployedConfigs(context.Context) ([]store.DeployedConfig, error)
}

type clientAccessSnapshot struct {
	agents  []core.Agent
	configs []store.DeployedConfig
}

func loadClientAccessSnapshot(ctx context.Context, source clientAccessDataSource) (clientAccessSnapshot, error) {
	var snapshot clientAccessSnapshot
	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error {
		var err error
		snapshot.agents, err = source.ListAgents(groupContext)
		return err
	})
	group.Go(func() error {
		var err error
		snapshot.configs, err = source.DeployedConfigs(groupContext)
		return err
	})
	err := group.Wait()
	return snapshot, err
}

type configCatalogResource struct {
	Catalog   configschema.Catalog    `json:"catalog"`
	Protocols []serverconfig.Protocol `json:"protocols"`
}

type agentConfigWorkspaceResource struct {
	AccountingPlan  *serverconfig.AccountingPlan `json:"accounting_plan,omitempty"`
	AccountingError string                       `json:"accounting_error,omitempty"`
	Agent           core.Agent                   `json:"agent"`
	Config          *core.Config                 `json:"config,omitempty"`
	Catalog         configschema.Catalog         `json:"catalog"`
	Protocols       []serverconfig.Protocol      `json:"protocols"`
	Inbounds        []serverconfig.Input         `json:"inbounds"`
	PresentFields   map[string]bool              `json:"present_fields"`
	RealityPresets  []string                     `json:"reality_presets"`
}

type configMutationResult struct {
	Config core.Config `json:"config"`
	Task   core.Task   `json:"task"`
}

func (s *Server) listDeployments(w http.ResponseWriter, request *http.Request) {
	deployments, err := s.store.LatestDeployments(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deployments)
}

func (s *Server) listAgentConfigs(w http.ResponseWriter, request *http.Request) {
	agentID := request.PathValue("id")
	configs, err := s.store.AgentConfigs(request.Context(), agentID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configs)
}

func (s *Server) configCatalog(w http.ResponseWriter, request *http.Request) {
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	catalog, err := configschema.CatalogFor(engine)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, configCatalogResource{Catalog: catalog, Protocols: serverconfig.Protocols(engine)})
}

func (s *Server) agentConfigWorkspace(w http.ResponseWriter, request *http.Request) {
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var agent core.Agent
	var config core.Config
	var policies []core.MainlandAccessPolicy
	group, ctx := errgroup.WithContext(request.Context())
	group.Go(func() error {
		var err error
		agent, err = s.store.GetAgent(ctx, request.PathValue("id"))
		return err
	})
	group.Go(func() error {
		var err error
		config, err = s.store.AgentConfig(ctx, request.PathValue("id"), engine)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	})
	if engine == core.EngineShadowsocksRust {
		group.Go(func() error {
			var err error
			policies, err = s.store.ListMainlandAccessPolicies(ctx, request.PathValue("id"))
			return err
		})
	}
	if err := group.Wait(); err != nil {
		writeStoreError(w, err)
		return
	}
	if !agentSupportsEngine(agent, engine) {
		writeError(w, http.StatusBadRequest, "agent does not support the requested engine")
		return
	}
	s.redactAgentMetrics(request, &agent)
	catalog, err := configschema.CatalogFor(engine)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result := agentConfigWorkspaceResource{
		Agent: agent, Catalog: catalog, Protocols: serverconfig.Protocols(engine),
		Inbounds: []serverconfig.Input{}, PresentFields: map[string]bool{},
		RealityPresets: serverconfig.RealityServerNamePresets(),
	}
	if config.ID != "" {
		result.Config = &config
		// Reading a saved plan only needs one verification pass. Stripping and
		// compiling it again tripled this work on every post-save page refresh.
		plan, planErr := serverconfig.PrepareAccounting(engine, config.Content)
		if planErr != nil && engine == core.EngineSingBox {
			plan, planErr = serverconfig.PlanPresetAccounting(engine, config.Content)
		}
		if planErr == nil {
			result.AccountingPlan = &plan
		} else {
			result.AccountingError = planErr.Error()
		}
		result.Inbounds = serverconfig.ParseAll(engine, config.Content)
		if err := s.hydrateClientMetadata(request.Context(), config, result.Inbounds); err != nil {
			writeInternalError(w, err)
			return
		}
		if engine == core.EngineShadowsocksRust {
			destination := false
			byKey := make(map[string]core.MainlandAccessPolicy, len(policies))
			for _, policy := range policies {
				destination = destination || policy.BlockMainlandDestination
				byKey[mainlandPolicyKey(agent.ID, engine, policy.Tag, policy.Port)] = policy
			}
			for index := range result.Inbounds {
				policy := byKey[mainlandPolicyKey(agent.ID, engine, result.Inbounds[index].Tag, result.Inbounds[index].Port)]
				result.Inbounds[index].BlockMainlandDestination = destination
				result.Inbounds[index].BlockMainlandSource = policy.BlockMainlandSource
			}
		}
		result.PresentFields, err = configschema.RootKeys(engine, config.Content)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, result)
}

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
	}, input.ExpectedVersion, store.ConfigMutationOptions{Action: core.Action(input.Intent), ClientMetadata: &metadata, MainlandPolicies: desired})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	s.refreshSavedAgentTrafficMonitoring(request.Context(), saved.AgentID)
	s.recordAudit(request, "agent_config.server_saved", saved.ID, input.Operation+" "+input.Input.Protocol+" "+agent.ID)
	writeJSON(w, http.StatusOK, configMutationResult{Config: saved, Task: task})
}

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

func (s *Server) agentEngineFromPath(w http.ResponseWriter, request *http.Request) (core.Engine, core.Agent, bool) {
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", core.Agent{}, false
	}
	agent, err := s.store.GetAgent(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return "", core.Agent{}, false
	}
	if !agentSupportsEngine(agent, engine) {
		writeError(w, http.StatusBadRequest, "agent does not support the requested engine")
		return "", core.Agent{}, false
	}
	return engine, agent, true
}

func agentSupportsEngine(agent core.Agent, engine core.Engine) bool {
	for _, candidate := range agent.Capabilities {
		if candidate == engine {
			return true
		}
	}
	return false
}

func (s *Server) createConfigMutationTask(w http.ResponseWriter, request *http.Request, config core.Config, intent string) (core.Task, bool) {
	action := core.ActionValidate
	if intent == "deploy" {
		action = core.ActionDeploy
	} else if intent != "validate" {
		writeError(w, http.StatusBadRequest, "intent must be validate or deploy")
		return core.Task{}, false
	}
	task, err := s.store.CreateTask(request.Context(), core.TaskRequest{
		AgentID: config.AgentID, Engine: config.Engine, Action: action, ConfigID: config.ID, ExpectedConfigVersion: config.Version,
	})
	if err != nil {
		writeStoreError(w, err)
		return core.Task{}, false
	}
	task.ConfigContent = ""
	return task, true
}

func (s *Server) listClientAccess(w http.ResponseWriter, request *http.Request) {
	entries, err := s.clientAccessEntries(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) clientAccessEntries(ctx context.Context) ([]clientAccessEntry, error) {
	snapshot, err := loadClientAccessSnapshot(ctx, s.store)
	if err != nil {
		return nil, err
	}
	agents := snapshot.agents

	agentsByID := make(map[string]core.Agent, len(agents))
	for _, agent := range agents {
		agentsByID[agent.ID] = agent
	}
	entries := make([]clientAccessEntry, 0, len(snapshot.configs))
	for _, deployed := range snapshot.configs {
		deployment, config := deployed.Deployment, deployed.Config
		agent, ok := agentsByID[deployment.AgentID]
		if !ok {
			continue
		}
		agent.Labels = cloneClientLabels(agent.Labels, deployed.Preferences)
		inputs := serverconfig.ParseAll(deployment.Engine, config.Content)
		if len(inputs) == 0 {
			continue
		}
		if err := applyClientMetadata(inputs, deployed.Metadata); err != nil {
			return nil, err
		}
		serverName := firstLabel(agent, "tls_server_name", "server_name")
		clientAddressMode := normalizeClientAddressMode(firstLabel(agent, "client_address_mode"))
		candidates := clientAddressCandidates(agent)
		addressOptions := buildClientAccessAddressOptions(deployment.Engine, inputs, candidates, serverName, agent.Labels)
		// Every displayed profile resolves its own connection address and address
		// family, so one listening endpoint never inherits another's settings.
		profiles := buildClientAccessProfiles(deployment.Engine, inputs, candidates, serverName, agent.Labels)
		if len(addressOptions) > 0 {
			primary := addressOptions[0]
			entries = append(entries, clientAccessEntry{
				AgentID: agent.ID, ConfigID: config.ID, AgentName: agent.Name, AgentStatus: agent.Status, Engine: deployment.Engine,
				Address: primary.Address, Source: primary.Source, Profiles: profiles, AddressOptions: addressOptions,
				AddressMode: clientAddressMode,
			})
		}
		if len(addressOptions) == 0 && len(candidates) == 0 {
			entries = append(entries, clientAccessEntry{
				AgentID: agent.ID, ConfigID: config.ID, AgentName: agent.Name, AgentStatus: agent.Status, Engine: deployment.Engine,
				AddressRequired: true, Profiles: []clientAccessProfile{}, AddressMode: clientAddressMode,
			})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].AgentName != entries[j].AgentName {
			return entries[i].AgentName < entries[j].AgentName
		}
		return entries[i].Engine < entries[j].Engine
	})
	return entries, nil
}

func cloneClientLabels(labels, preferences map[string]string) map[string]string {
	result := make(map[string]string, len(labels)+len(preferences))
	for key, value := range labels {
		if !strings.HasPrefix(key, "client_profile_") {
			result[key] = value
		}
	}
	for key, value := range preferences {
		result[key] = value
	}
	return result
}

func (s *Server) hydrateClientMetadata(ctx context.Context, config core.Config, inputs []serverconfig.Input) error {
	metadata, err := s.store.ConfigClientMetadata(ctx, config.ID, config.Version)
	if err != nil {
		return err
	}
	return applyClientMetadata(inputs, metadata)
}

func applyClientMetadata(inputs []serverconfig.Input, metadata map[string]string) error {
	for index := range inputs {
		if err := serverconfig.ApplyClientMetadata(&inputs[index], metadata[inputs[index].Tag]); err != nil {
			return fmt.Errorf("apply client metadata for %s: %w", inputs[index].Tag, err)
		}
	}
	return nil
}

type clientAddressCandidate struct {
	address     string
	source      string
	family      string
	profileOnly bool
}

func normalizeClientAddressMode(value string) string {
	if value == core.SubStoreAddressModeIPv4 || value == core.SubStoreAddressModeIPv6 {
		return value
	}
	return core.SubStoreAddressModeAuto
}

// clientProfileAddress resolves the automatic address used by one listening
// endpoint. A requested family without a matching candidate falls back to the
// first available candidate so a profile never renders an empty address.
func clientProfileAddress(candidates []clientAddressCandidate, mode string) clientAddressCandidate {
	if mode == core.SubStoreAddressModeIPv4 || mode == core.SubStoreAddressModeIPv6 {
		for _, candidate := range candidates {
			if candidate.family == mode {
				return candidate
			}
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return clientAddressCandidate{}
}

// buildClientAccessProfiles renders the effective profile of every listening
// endpoint. Only profile-scoped labels provide a display override: node-wide
// client display values are deliberately not inherited, so an upgraded node
// never has to clear the same legacy value on every port. The node-wide
// client address still participates as an automatic candidate.
func buildClientAccessProfiles(engine core.Engine, inputs []serverconfig.Input, candidates []clientAddressCandidate, serverName string, labels map[string]string) []clientAccessProfile {
	profiles := make([]clientAccessProfile, 0, len(inputs))
	for _, input := range inputs {
		mode := core.SubStoreAddressModeAuto
		if value, exists := labels[core.ClientProfileFamilyLabel(engine, input.Listen, input.Port)]; exists {
			mode = normalizeClientAddressMode(value)
		}
		address, overridden := strings.TrimSpace(labels[core.ClientProfileAddressLabel(engine, input.Listen, input.Port)]), false
		if address != "" {
			overridden = true
		} else {
			address = clientProfileAddress(candidates, mode).address
		}
		name, nameOverridden := "", false
		if value, exists := labels[core.ClientProfileNameLabel(engine, input.Listen, input.Port)]; exists {
			name, nameOverridden = value, true
		}
		profile, err := serverconfig.BuildClientProfileNamed(input, address, serverName, name)
		if err != nil {
			continue
		}
		protocol, found := serverconfig.FindProtocol(engine, input.Protocol)
		if !found {
			continue
		}
		profiles = append(profiles, clientAccessProfile{
			Tag: input.Tag, Protocol: protocol.Name, Port: input.Port, Profile: profile,
			ClientName: name, NameOverridden: nameOverridden,
			Address: address, AddressMode: mode, AddressOverridden: overridden,
		})
	}
	return profiles
}

func buildClientAccessAddressOptions(engine core.Engine, inputs []serverconfig.Input, candidates []clientAddressCandidate, serverName string, labelSets ...map[string]string) []clientAccessAddressOption {
	// A per-profile manual hostname may differ from every host-wide address.
	// Publish it only for that profile; do not use it as another port's
	// automatic candidate or discard the entire entry when all ports override.
	candidates = append([]clientAddressCandidate(nil), candidates...)
	if len(labelSets) > 0 {
		for _, input := range inputs {
			address := strings.TrimSpace(labelSets[0][core.ClientProfileAddressLabel(engine, input.Listen, input.Port)])
			found := address == ""
			for _, candidate := range candidates {
				found = found || candidate.address == address
			}
			if !found {
				candidates = append(candidates, clientAddressCandidate{address: address,
					source: "入站手动设置", family: clientAddressFamily(address), profileOnly: true})
			}
		}
	}
	options := make([]clientAccessAddressOption, 0, len(candidates))
	for _, candidate := range candidates {
		profiles := make([]clientAccessProfile, 0, len(inputs))
		for _, input := range inputs {
			if len(labelSets) > 0 {
				// A port with a manual address is pinned to exactly one candidate;
				// exposing it under another family would publish a second URI.
				if value := strings.TrimSpace(labelSets[0][core.ClientProfileAddressLabel(engine, input.Listen, input.Port)]); (value != "" && value != candidate.address) || (candidate.profileOnly && value == "") {
					continue
				}
			}
			name, overridden := "", false
			if len(labelSets) > 0 {
				if value, exists := labelSets[0][core.ClientProfileNameLabel(engine, input.Listen, input.Port)]; exists {
					name, overridden = value, true
				}
			}
			profile, err := serverconfig.BuildClientProfileNamed(input, candidate.address, serverName, name)
			if err != nil {
				continue
			}
			protocol, found := serverconfig.FindProtocol(engine, input.Protocol)
			if !found {
				continue
			}
			profiles = append(profiles, clientAccessProfile{Tag: input.Tag, Protocol: protocol.Name, Port: input.Port, Profile: profile, ClientName: name, NameOverridden: overridden})
		}
		if len(profiles) > 0 {
			options = append(options, clientAccessAddressOption{
				Address: candidate.address, Source: candidate.source, Family: candidate.family, Profiles: profiles,
			})
		}
	}
	return options
}

func clientAddressFamily(address string) string {
	address = strings.TrimSpace(address)
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		address = strings.TrimSuffix(strings.TrimPrefix(address, "["), "]")
	}
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return "hostname"
	}
	if parsed.Unmap().Is4() {
		return core.SubStoreAddressModeIPv4
	}
	return core.SubStoreAddressModeIPv6
}

func clientAddressCandidates(agent core.Agent) []clientAddressCandidate {
	result := make([]clientAddressCandidate, 0, 8)
	seen := make(map[string]struct{})
	labelSources := []struct {
		key    string
		source string
	}{
		{key: "client_address", source: "手动设置"},
		{key: "public_host", source: "节点公网域名"},
		{key: "public_ip", source: "节点公网 IP"},
	}
	for _, item := range labelSources {
		key := item.key
		if value := strings.TrimSpace(agent.Labels[key]); value != "" {
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				result = append(result, clientAddressCandidate{address: value, source: item.source, family: clientAddressFamily(value)})
			}
		}
	}
	// The Agent probes each family outbound, so both routable egress addresses
	// are known even when the control-plane connection itself used only one.
	for _, probed := range []struct {
		value      string
		provenance string
		wantIPv4   bool
	}{
		{agent.Metrics.PublicIPv4, agent.Metrics.PublicIPv4Source, true},
		{agent.Metrics.PublicIPv6, agent.Metrics.PublicIPv6Source, false},
	} {
		if probed.provenance != "" && probed.provenance != core.PublicIPProbeSourceAgent && probed.provenance != core.PublicIPProbeSourceControlPlane {
			continue
		}
		address := authn.NormalizePublicIP(probed.value)
		parsed, parseErr := netip.ParseAddr(address)
		if parseErr != nil || address == "" || parsed.Is4() != probed.wantIPv4 || netpolicy.IsCloudflareAddress(parsed) {
			continue
		}
		if _, exists := seen[address]; !exists {
			seen[address] = struct{}{}
			family := "IPv6"
			if probed.wantIPv4 {
				family = "IPv4"
			}
			source := "Agent 本地直连探测 · " + family
			if probed.provenance == core.PublicIPProbeSourceControlPlane {
				source = "控制面配置的 Agent 直连探测 · " + family
			}
			result = append(result, clientAddressCandidate{address: address, source: source, family: clientAddressFamily(address)})
		}
	}
	// Default-route interface addresses are the next fallback per family. Only
	// actual globally routable unicast addresses are accepted; private, CGNAT,
	// documentation, reserved, link-local, zoned and invalid values are dropped
	// so a non-routable interface can never be surfaced as a node address.
	for _, item := range publicInterfaceAddresses(agent.Metrics.NetworkInterfaces) {
		if _, exists := seen[item.address]; !exists {
			seen[item.address] = struct{}{}
			result = append(result, clientAddressCandidate{address: item.address, source: "Agent 默认路由接口 " + item.name, family: clientAddressFamily(item.address)})
		}
	}
	// The WSS observation is now only populated when the proxy chain resolved
	// unambiguously, so it is a strictly verified fallback; an ambiguous chain
	// clears the value on reconnect, never surfacing a relay as the node.
	if address := authn.NormalizePublicIP(agent.Metrics.ObservedPublicIP); address != "" {
		parsed, parseErr := netip.ParseAddr(address)
		if parseErr == nil && !netpolicy.IsCloudflareAddress(parsed) {
			family := "IPv4"
			if strings.Contains(address, ":") {
				family = "IPv6"
			}
			if _, exists := seen[address]; !exists {
				seen[address] = struct{}{}
				result = append(result, clientAddressCandidate{address: address, source: "已验证连接来源 · " + family, family: clientAddressFamily(address)})
			}
		}
	}
	return result
}

type publicInterfaceAddress struct {
	address string
	name    string
	family  int
}

// publicInterfaceAddresses returns the globally routable unicast addresses of
// the reported default-route interfaces, IPv4 before IPv6, de-duplicated and
// sorted. authn.NormalizePublicIP reuses the IANA special-purpose denylist so
// private, CGNAT, documentation, reserved, link-local and invalid values never
// become node address candidates.
func publicInterfaceAddresses(interfaces []core.HostNetworkInterface) []publicInterfaceAddress {
	addresses := make([]publicInterfaceAddress, 0, 8)
	for _, networkInterface := range interfaces {
		for _, raw := range networkInterface.Addresses {
			normalized := authn.NormalizePublicIP(raw)
			if normalized == "" {
				continue
			}
			parsed, err := netip.ParseAddr(normalized)
			if err != nil || netpolicy.IsCloudflareAddress(parsed) {
				continue
			}
			family := 1
			if !strings.Contains(normalized, ":") {
				family = 0
			}
			addresses = append(addresses, publicInterfaceAddress{address: normalized, name: networkInterface.Name, family: family})
		}
	}
	sort.SliceStable(addresses, func(i, j int) bool {
		if addresses[i].family != addresses[j].family {
			return addresses[i].family < addresses[j].family
		}
		if addresses[i].name != addresses[j].name {
			return addresses[i].name < addresses[j].name
		}
		return addresses[i].address < addresses[j].address
	})
	deduped := addresses[:0]
	seen := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		if _, exists := seen[address.address]; exists {
			continue
		}
		seen[address.address] = struct{}{}
		deduped = append(deduped, address)
	}
	return deduped
}

func firstLabel(agent core.Agent, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(agent.Labels[key]); value != "" {
			return value
		}
	}
	return ""
}
