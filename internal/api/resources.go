package api

import (
	"errors"
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"golang.org/x/sync/errgroup"
)

type configCatalogResource struct {
	Catalog   configschema.Catalog    `json:"catalog"`
	Protocols []serverconfig.Protocol `json:"protocols"`
}

type configInboundTargetResource struct {
	Tag  string `json:"tag"`
	Port int    `json:"port"`
	Kind string `json:"kind"`
}

type agentConfigWorkspaceResource struct {
	AccountingPlan  *serverconfig.AccountingPlan  `json:"accounting_plan,omitempty"`
	AccountingError string                        `json:"accounting_error,omitempty"`
	Agent           core.Agent                    `json:"agent"`
	Config          *core.Config                  `json:"config,omitempty"`
	Catalog         configschema.Catalog          `json:"catalog"`
	Protocols       []serverconfig.Protocol       `json:"protocols"`
	Inbounds        []serverconfig.Input          `json:"inbounds"`
	InboundTargets  []configInboundTargetResource `json:"inbound_targets"`
	PresentFields   map[string]bool               `json:"present_fields"`
	RealityPresets  []string                      `json:"reality_presets"`
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
		InboundTargets: []configInboundTargetResource{},
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
		// Selection for restrictions/deletion also includes native named
		// listeners that cannot be reconstructed as a preset form.
		for _, inbound := range serverconfig.DiscoverMainlandAccessPolicies(engine, config.Content) {
			result.InboundTargets = append(result.InboundTargets, configInboundTargetResource{
				Tag: inbound.Tag, Port: inbound.Port, Kind: inbound.Kind,
			})
		}
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
