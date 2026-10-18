package store

import (
	"context"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (c trafficConfiguration) endpoints(content string, version int) []core.PortTrafficEndpoint {
	endpoints := configurationTrafficEndpoints(c.Config, content, version, c.ShareID)
	for i := range endpoints {
		endpoints[i].TrafficOwnership = c.Ownership
		endpoints[i].ServiceStatus = c.ServiceStatus
	}
	return endpoints
}

// The current page and every mutation target use the same confirmed instance
// identity. ListPortTrafficPolicies remains the historical/accounting read.
func (s *Store) CurrentPortTrafficPolicies(ctx context.Context) ([]core.PortTrafficPolicy, error) {
	policies, err := s.ListPortTrafficPolicies(ctx)
	if err != nil {
		return nil, err
	}
	endpoints, err := s.AppliedTrafficEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]core.PortTrafficPolicy, 0, len(policies))
	for _, p := range policies {
		for _, ep := range endpoints {
			if trafficPolicyMatchesEndpoint(p, ep) {
				p.TrafficOwnership = ep.TrafficOwnership
				p.ServiceStatus = ep.ServiceStatus
				p.DeploymentConfirmed = true
				result = append(result, p)
				break
			}
		}
	}
	return result, nil
}

func trafficPolicyMatchesEndpoint(p core.PortTrafficPolicy, ep core.PortTrafficEndpoint) bool {
	return p.AgentID == ep.AgentID && p.Engine == ep.Engine && p.Port == ep.Port && p.ShareID == ep.ShareID &&
		(p.ConfigID == "" || p.ConfigID == ep.ConfigID && p.ConfigVersion == ep.ConfigVersion)
}
