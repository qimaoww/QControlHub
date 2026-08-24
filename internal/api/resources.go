package api

import (
	"context"
	"errors"
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
	Tag      string                     `json:"tag"`
	Protocol string                     `json:"protocol"`
	Profile  serverconfig.ClientProfile `json:"profile"`
}

type clientAccessEntry struct {
	AgentID         string                `json:"agent_id"`
	AgentName       string                `json:"agent_name"`
	Engine          core.Engine           `json:"engine"`
	Address         string                `json:"address"`
	Source          string                `json:"source"`
	AddressRequired bool                  `json:"address_required,omitempty"`
	Profiles        []clientAccessProfile `json:"profiles"`
}

type clientAccessDataSource interface {
	ListAgents(context.Context) ([]core.Agent, error)
	LatestDeployments(context.Context) ([]core.Deployment, error)
	ListAgentConfigs(context.Context) ([]core.Config, error)
	ListConfigs(context.Context) ([]core.Config, error)
}

type clientAccessSnapshot struct {
	agents         []core.Agent
	deployments    []core.Deployment
	agentConfigs   []core.Config
	archiveConfigs []core.Config
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
		snapshot.deployments, err = source.LatestDeployments(groupContext)
		return err
	})
	group.Go(func() error {
		var err error
		snapshot.agentConfigs, err = source.ListAgentConfigs(groupContext)
		return err
	})
	group.Go(func() error {
		var err error
		snapshot.archiveConfigs, err = source.ListConfigs(groupContext)
		return err
	})
	return snapshot, group.Wait()
}

type configCatalogResource struct {
	Catalog   configschema.Catalog    `json:"catalog"`
	Protocols []serverconfig.Protocol `json:"protocols"`
}

type agentConfigWorkspaceResource struct {
	Agent          core.Agent              `json:"agent"`
	Config         *core.Config            `json:"config,omitempty"`
	Catalog        configschema.Catalog    `json:"catalog"`
	Protocols      []serverconfig.Protocol `json:"protocols"`
	Inbounds       []serverconfig.Input    `json:"inbounds"`
	PresentFields  map[string]bool         `json:"present_fields"`
	RealityPresets []string                `json:"reality_presets"`
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
	if _, err := s.store.GetAgent(request.Context(), agentID); err != nil {
		writeStoreError(w, err)
		return
	}
	configs, err := s.store.ListAgentConfigs(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	result := make([]core.Config, 0, len(configs))
	for _, config := range configs {
		if config.AgentID == agentID {
			result = append(result, config)
		}
	}
	writeJSON(w, http.StatusOK, result)
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
	agent, err := s.store.GetAgent(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !agentSupportsEngine(agent, engine) {
		writeError(w, http.StatusBadRequest, "agent does not support the requested engine")
		return
	}
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
	config, err := s.store.AgentConfig(request.Context(), agent.ID, engine)
	if err == nil {
		result.Config = &config
		result.Inbounds = serverconfig.ParseAll(engine, config.Content)
		result.PresentFields, err = configschema.RootKeys(engine, config.Content)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
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
		Operation       string             `json:"operation"`
		OriginalTag     string             `json:"original_tag"`
		ExpectedVersion int                `json:"expected_version"`
		Name            string             `json:"name"`
		Description     string             `json:"description"`
		Intent          string             `json:"intent"`
		Input           serverconfig.Input `json:"input"`
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
	if _, found := serverconfig.FindProtocol(engine, input.Input.Protocol); !found {
		writeError(w, http.StatusBadRequest, "unknown server inbound protocol")
		return
	}
	if input.Input.RealityEnabled && input.Operation != "delete" {
		target, err := serverconfig.ProbeRealityTarget(request.Context(), input.Input.RealityServerName)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		input.Input.RealityServerName = target.ServerName
	}
	generated, err := serverconfig.Generate(engine, input.Input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	content := generated
	current, currentErr := s.store.AgentConfig(request.Context(), agent.ID, engine)
	if currentErr == nil {
		content, err = serverconfig.MutateGenerated(engine, current.Content, generated, input.OriginalTag, input.Operation)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if !errors.Is(currentErr, store.ErrNotFound) {
		writeStoreError(w, currentErr)
		return
	} else if input.Operation != "add" {
		writeError(w, http.StatusConflict, "no saved configuration exists for this operation")
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = agent.Name + " · " + string(engine)
	}
	saved, err := s.store.SaveAgentConfig(request.Context(), core.Config{
		AgentID: agent.ID, Name: name, Description: input.Description, Engine: engine, Content: content,
	}, input.ExpectedVersion)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task, ok := s.createConfigMutationTask(w, request, saved, input.Intent)
	if !ok {
		return
	}
	s.recordAudit(request, "agent_config.server_saved", saved.ID, input.Operation+" "+input.Input.Protocol+" "+agent.ID)
	writeJSON(w, http.StatusOK, configMutationResult{Config: saved, Task: task})
}

func (s *Server) saveConfigField(w http.ResponseWriter, request *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, request)
	if !ok {
		return
	}
	key := strings.TrimSpace(request.PathValue("key"))
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
	present := false
	keys, err := configschema.RootKeys(engine, current.Content)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	present = keys[key]
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
	content, err := configschema.MergeFragment(engine, current.Content, key, input.Fragment, input.Mutation == "delete")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = current.Name
	}
	saved, err := s.store.SaveAgentConfig(request.Context(), core.Config{
		AgentID: agent.ID, Name: name, Description: input.Description, Engine: engine, Content: content,
	}, input.ExpectedVersion)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task, ok := s.createConfigMutationTask(w, request, saved, input.Intent)
	if !ok {
		return
	}
	s.recordAudit(request, "agent_config.field_saved", saved.ID, input.Mutation+" "+key+" "+agent.ID)
	writeJSON(w, http.StatusOK, configMutationResult{Config: saved, Task: task})
}

func (s *Server) getConfigField(w http.ResponseWriter, request *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, request)
	if !ok {
		return
	}
	key := strings.TrimSpace(request.PathValue("key"))
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
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "present": present, "fragment": fragment})
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
		AgentID: config.AgentID, Engine: config.Engine, Action: action, ConfigID: config.ID,
	})
	if err != nil {
		writeStoreError(w, err)
		return core.Task{}, false
	}
	task.ConfigContent = ""
	return task, true
}

func (s *Server) listClientAccess(w http.ResponseWriter, request *http.Request) {
	snapshot, err := loadClientAccessSnapshot(request.Context(), s.store)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	agents := snapshot.agents
	deployments := snapshot.deployments
	agentConfigs := snapshot.agentConfigs
	archiveConfigs := snapshot.archiveConfigs

	agentsByID := make(map[string]core.Agent, len(agents))
	for _, agent := range agents {
		agentsByID[agent.ID] = agent
	}
	configsByID := make(map[string]core.Config, len(agentConfigs)+len(archiveConfigs))
	for _, config := range agentConfigs {
		configsByID[config.ID] = config
	}
	for _, config := range archiveConfigs {
		configsByID[config.ID] = config
	}

	entries := make([]clientAccessEntry, 0, len(deployments))
	for _, deployment := range deployments {
		agent, ok := agentsByID[deployment.AgentID]
		if !ok {
			continue
		}
		config, ok := configsByID[deployment.ConfigID]
		if !ok {
			continue
		}
		if config.Version != deployment.ConfigVersion {
			config, err = s.store.ConfigRevision(request.Context(), deployment.ConfigID, deployment.ConfigVersion)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				writeInternalError(w, err)
				return
			}
		}
		inputs := serverconfig.ParseAll(deployment.Engine, config.Content)
		if len(inputs) == 0 {
			continue
		}
		serverName := firstLabel(agent, "tls_server_name", "server_name")
		profileGenerated := false
		candidates := clientAddressCandidates(agent)
		for _, candidate := range candidates {
			profiles := make([]clientAccessProfile, 0, len(inputs))
			for _, input := range inputs {
				profile, profileErr := serverconfig.BuildClientProfile(input, candidate.address, serverName)
				if profileErr != nil {
					continue
				}
				protocol, found := serverconfig.FindProtocol(deployment.Engine, input.Protocol)
				if !found {
					continue
				}
				profiles = append(profiles, clientAccessProfile{Tag: input.Tag, Protocol: protocol.Name, Profile: profile})
			}
			if len(profiles) > 0 {
				entries = append(entries, clientAccessEntry{
					AgentID: agent.ID, AgentName: agent.Name, Engine: deployment.Engine,
					Address: candidate.address, Source: candidate.source, Profiles: profiles,
				})
				profileGenerated = true
				break
			}
		}
		if !profileGenerated && len(candidates) == 0 {
			entries = append(entries, clientAccessEntry{
				AgentID: agent.ID, AgentName: agent.Name, Engine: deployment.Engine,
				AddressRequired: true, Profiles: []clientAccessProfile{},
			})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].AgentName != entries[j].AgentName {
			return entries[i].AgentName < entries[j].AgentName
		}
		return entries[i].Engine < entries[j].Engine
	})
	writeJSON(w, http.StatusOK, entries)
}

type clientAddressCandidate struct {
	address string
	source  string
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
				result = append(result, clientAddressCandidate{address: value, source: item.source})
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
			result = append(result, clientAddressCandidate{address: address, source: source})
		}
	}
	// Default-route interface addresses are the next fallback per family. Only
	// actual globally routable unicast addresses are accepted; private, CGNAT,
	// documentation, reserved, link-local, zoned and invalid values are dropped
	// so a non-routable interface can never be surfaced as a node address.
	for _, item := range publicInterfaceAddresses(agent.Metrics.NetworkInterfaces) {
		if _, exists := seen[item.address]; !exists {
			seen[item.address] = struct{}{}
			result = append(result, clientAddressCandidate{address: item.address, source: "Agent 默认路由接口 " + item.name})
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
				result = append(result, clientAddressCandidate{address: address, source: "已验证连接来源 · " + family})
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
