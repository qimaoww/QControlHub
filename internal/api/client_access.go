package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"golang.org/x/sync/errgroup"
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
	Address           string          `json:"address,omitempty"`
	AddressMode       string          `json:"address_mode,omitempty"`
	AddressOverridden bool            `json:"address_overridden,omitempty"`
	Outbound          json.RawMessage `json:"outbound,omitempty"`
	OutboundError     string          `json:"outbound_error,omitempty"`
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

func (s *Server) listClientAccess(w http.ResponseWriter, request *http.Request) {
	outboundEngine := core.Engine(request.URL.Query().Get("outbound_engine"))
	if outboundEngine != "" && outboundEngine != core.EngineXray && outboundEngine != core.EngineSingBox {
		writeError(w, http.StatusBadRequest, "outbound_engine must be xray or sing-box")
		return
	}
	entries, err := s.clientAccessEntries(request.Context(), outboundEngine)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) clientAccessEntries(ctx context.Context, outboundEngine ...core.Engine) ([]clientAccessEntry, error) {
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
		if len(outboundEngine) > 0 && outboundEngine[0] != "" {
			// Reuse the same permission-scoped deployed snapshot and per-port
			// address overrides; do not read arbitrary node workspaces.
			for index := range profiles {
				profile := &profiles[index]
				for _, input := range inputs {
					if input.Tag != profile.Tag || input.Port != profile.Port {
						continue
					}
					outbound, err := serverconfig.BuildClientOutbound(outboundEngine[0], input, profile.Address, serverName)
					if err != nil {
						profile.OutboundError = err.Error()
					} else {
						profile.Outbound = outbound
					}
					break
				}
			}
		}
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
