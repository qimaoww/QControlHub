package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

func (s *Store) GetAgent(ctx context.Context, id string) (core.Agent, error) {
	var agent core.Agent
	var capabilities, features, labels, runtimeState, metricsState []byte
	var observedPublicIP string
	var offlineThresholdSeconds int
	args := []any{id}
	where := agentAccessClause(ctx, "agents.id", &args)
	query := scopedAgentsSQL(ctx, &args)
	err := s.pool.QueryRow(ctx, query+` AND id=$1`+where, args...).Scan(
		&agent.ID, &agent.Name, &agent.Version, &agent.OS, &agent.Arch, &capabilities, &features, &labels, &runtimeState, &observedPublicIP, &metricsState, &agent.LastSeen, &agent.EnrolledAt, &offlineThresholdSeconds, &agent.SupportedCapabilities, &agent.CapabilityTransitions, &agent.OwnerID, &agent.AdminHidden, &agent.SharedEngines)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Agent{}, ErrNotFound
	}
	if err != nil {
		return core.Agent{}, err
	}
	if err := json.Unmarshal(capabilities, &agent.Capabilities); err != nil {
		return core.Agent{}, err
	}
	if err := json.Unmarshal(features, &agent.Features); err != nil {
		return core.Agent{}, err
	}
	if err := json.Unmarshal(labels, &agent.Labels); err != nil {
		return core.Agent{}, err
	}
	if err := json.Unmarshal(runtimeState, &agent.Runtime); err != nil {
		return core.Agent{}, err
	}
	if err := decodeAgentMetrics(metricsState, observedPublicIP, &agent.Metrics); err != nil {
		return core.Agent{}, err
	}
	if agent.LastSeen.After(time.Now().UTC().Add(-time.Duration(offlineThresholdSeconds) * time.Second)) {
		agent.Status = "online"
	} else {
		agent.Status = "offline"
	}
	scopeAgentPresentation(ctx, &agent)
	return agent, nil
}

func scopeAgentPresentation(ctx context.Context, agent *core.Agent) {
	scope := scopeForConfig(ctx)
	agent.CanManage = scope.Admin || agent.OwnerID == scope.OwnerID || strings.HasPrefix(scope.OwnerID, "token_")
	if requestScope, requestScoped := requestConfigScope(ctx); requestScoped {
		agent.CanHide = agent.OwnerID == requestScope.OwnerID
	} else {
		agent.CanHide = true
	}
	for key := range agent.Labels {
		if strings.HasPrefix(key, "client_profile_") || (!agent.CanManage && key == komariUUIDLabel) {
			delete(agent.Labels, key)
		}
	}
	if !agent.CanManage {
		// Task identities and host log details are not part of a sharing grant.
		agent.Capabilities = core.IntersectEngines(agent.Capabilities, agent.SharedEngines)
		agent.SupportedCapabilities = core.IntersectEngines(agent.SupportedCapabilities, agent.SharedEngines)
		agent.CapabilityTransitions = nil
		agent.Metrics.BBR = nil
		// Private interface addresses map the host's internal topology. A
		// recipient only needs the globally routable address that builds their
		// client profiles, so drop everything else before serialization.
		agent.Metrics.NetworkInterfaces = routableInterfaceAddresses(agent.Metrics.NetworkInterfaces)
		for engine, runtime := range agent.Runtime {
			if !containsEngine(agent.SharedEngines, engine) {
				delete(agent.Runtime, engine)
				continue
			}
			agent.Runtime[engine] = core.RuntimeState{
				Installed: runtime.Installed, Version: runtime.Version, ServiceStatus: runtime.ServiceStatus,
			}
		}
	} else {
		agent.SharedEngines = nil
	}
}

// routableInterfaceAddresses keeps only globally routable addresses. It is the
// store-side counterpart of the API's public connection candidates: a sharing
// recipient may learn the address that builds their client profiles, never the
// host's private interface topology.
func routableInterfaceAddresses(interfaces []core.HostNetworkInterface) []core.HostNetworkInterface {
	filtered := make([]core.HostNetworkInterface, 0, len(interfaces))
	for _, item := range interfaces {
		addresses := make([]string, 0, len(item.Addresses))
		for _, raw := range item.Addresses {
			address, err := netip.ParseAddr(strings.TrimSpace(raw))
			if err != nil || !netpolicy.IsPublicAddress(address) {
				continue
			}
			addresses = append(addresses, raw)
		}
		if len(addresses) == 0 {
			continue
		}
		item.Addresses = addresses
		filtered = append(filtered, item)
	}
	return filtered
}
