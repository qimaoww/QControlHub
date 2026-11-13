package api

import (
	"context"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// This suggests a port from the caller's allocation. Saving/deploying still
// performs the transactional ownership and reservation checks.
func (s *Server) sharedManagedTLSPlanPort(ctx context.Context, owner, agentID string, targetEngine core.Engine, plan serverconfig.Input, current *serverconfig.Input, excluded ...int) (int, error) {
	access, err := s.store.UserAgentAccess(ctx, owner)
	if err != nil {
		return 0, err
	}
	var ports []int
	for _, share := range access.Shares {
		if share.AgentID != agentID || !share.Enabled || share.Status != core.AgentShareAccepted {
			continue
		}
		for _, engine := range share.Engines {
			if engine == targetEngine {
				ports = share.Ports
			}
		}
	}
	if core.UnrestrictedSharedPorts(ports) {
		return plan.Port, nil
	}
	configs, err := s.store.AgentConfigs(ctx, agentID)
	if err != nil {
		return 0, err
	}
	used := map[int]bool{plan.TLSPublicPort: true, 80: true}
	for _, port := range excluded {
		used[port] = true
	}
	if current != nil {
		used[current.Port] = true
	}
	for _, config := range configs {
		for _, endpoint := range serverconfig.DiscoverTrafficPorts(config.Engine, config.Content) {
			used[endpoint.Port] = true
		}
		metadata, err := s.store.ConfigClientMetadata(ctx, config.ID, config.Version)
		if err != nil {
			return 0, err
		}
		frontends, err := serverconfig.ManagedTLSFrontends(config.Engine, config.Content, metadata)
		if err != nil {
			return 0, err
		}
		for _, frontend := range frontends {
			used[frontend.PublicPort] = true
		}
	}
	for _, port := range ports {
		if !used[port] {
			return port, nil
		}
	}
	// A single allocated backend can keep its port while rotating credentials.
	if current != nil && current.Port != plan.TLSPublicPort && current.Port != 80 {
		for _, port := range ports {
			if port == current.Port && !containsPlanPort(excluded, port) {
				return port, nil
			}
		}
	}
	return 0, fmt.Errorf("%w: 已授权端口中没有可用的内部监听端口；请节点所有者分配公网 HTTPS 端口之外的内部端口", store.ErrInvalid)
}

func containsPlanPort(ports []int, port int) bool {
	for _, value := range ports {
		if value == port {
			return true
		}
	}
	return false
}

func (s *Server) sharedXHTTPGroupPlan(ctx context.Context, owner, agentID string, engine core.Engine, plan *serverconfig.Input, current *serverconfig.Input) error {
	previousPublic := plan.Port
	selected := []int{}
	for index, field := range []*int{&plan.Port, &plan.XHTTPBackendPort, &plan.XHTTPTLSPort} {
		candidate := *plan
		candidate.Port, candidate.TLSPublicPort = *field, 0
		var prior *serverconfig.Input
		if current != nil {
			copy := *current
			copy.Port = []int{current.Port, current.XHTTPBackendPort, current.XHTTPTLSPort}[index]
			prior = &copy
		}
		port, err := s.sharedManagedTLSPlanPort(ctx, owner, agentID, engine, candidate, prior, selected...)
		if err != nil {
			return err
		}
		*field = port
		selected = append(selected, port)
	}
	if plan.ClientPort == previousPublic {
		plan.ClientPort = plan.Port
	}
	return nil
}

// Default only from the chosen provider's real configuration, otherwise HTTPS
// uses 443. Backend allocations and former grant ports are never suggestions.
func (s *Server) sharedManagedTLSPublicPort(ctx context.Context, owner, agentID string, engine core.Engine, plan *serverconfig.Input) (int, error) {
	access, err := s.store.UserAgentAccess(ctx, owner)
	if err != nil {
		return 0, err
	}
	for _, share := range access.Shares {
		if share.AgentID != agentID || !share.Enabled || share.Status != core.AgentShareAccepted {
			continue
		}
		matched := false
		for _, allowed := range share.Engines {
			matched = matched || allowed == engine
		}
		if !matched {
			continue
		}
		modes := map[string]bool{}
		for _, grant := range share.ProxyIngresses {
			modes[grant.Mode] = true
		}
		if !modes[plan.TLSMode] {
			if modes["caddy"] {
				plan.TLSMode = "caddy"
			} else if modes["nginx"] {
				plan.TLSMode = "nginx"
			} else {
				break
			}
		}
		configs, err := s.store.AgentConfigs(ctx, agentID)
		if err != nil {
			return 0, err
		}
		for _, config := range configs {
			metadata, err := s.store.ConfigClientMetadata(ctx, config.ID, config.Version)
			if err != nil {
				return 0, err
			}
			frontends, err := serverconfig.ManagedTLSFrontends(config.Engine, config.Content, metadata)
			if err != nil {
				return 0, err
			}
			for _, f := range frontends {
				if !f.LocalOnly && f.Mode == plan.TLSMode {
					return f.PublicPort, nil
				}
			}
		}
		return 443, nil
	}
	return 0, fmt.Errorf("%w: 未获授权使用 Nginx/Caddy，请联系节点所有者", store.ErrInvalid)
}
