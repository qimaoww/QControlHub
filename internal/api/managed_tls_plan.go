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
func (s *Server) sharedManagedTLSPlanPort(ctx context.Context, owner, agentID string, plan serverconfig.Input, current *serverconfig.Input) (int, error) {
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
			if engine == core.EngineSingBox {
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
			if port == current.Port {
				return port, nil
			}
		}
	}
	return 0, fmt.Errorf("%w: 已授权端口中没有可用的内部监听端口；请节点所有者分配公网 HTTPS 端口之外的内部端口", store.ErrInvalid)
}
