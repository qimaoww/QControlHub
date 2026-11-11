package agent

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// The task lifecycle lock serializes policy changes with the entire operation,
// including rollback. The websocket reader keeps heartbeats flowing while an
// operation finishes, and the newest pending snapshot survives reconnects.
func (c *Client) receiveTrafficPolicies(ctx context.Context, policies []core.PortTrafficPolicy) error {
	c.trafficPolicyMu.Lock()
	c.pendingTrafficPolicies = slices.Clone(policies)
	c.trafficPoliciesPending = true
	c.trafficPolicyMu.Unlock()
	return c.tryApplyTrafficPolicies(ctx)
}

func (c *Client) tryApplyTrafficPolicies(ctx context.Context) error {
	if !c.taskLifecycleMu.TryLock() {
		return nil
	}
	defer c.taskLifecycleMu.Unlock()
	return c.applyPendingTrafficPolicies(ctx)
}

// Caller holds taskLifecycleMu; execution cannot start with an older policy.
func (c *Client) applyPendingTrafficPolicies(ctx context.Context) error {
	c.trafficPolicyMu.Lock()
	defer c.trafficPolicyMu.Unlock()
	if !c.trafficPoliciesPending {
		return nil
	}
	policies := c.pendingTrafficPolicies
	err := c.traffic.SetPolicies(ctx, policies, c.creds.AgentID)
	reports := map[string]core.ProxyIngressReport{}
	for _, p := range policies {
		if q := p.SharedQuota; q != nil && q.ProxyIngressRevision > 0 {
			r := core.ProxyIngressReport{ShareID: q.ID, Revision: q.ProxyIngressRevision, Revoked: q.Revoked}
			if err != nil {
				r.Error = "节点入口清理失败"
			}
			reports[q.ID] = r
		}
	}
	c.proxyIngressReports = nil
	for _, r := range reports {
		c.proxyIngressReports = append(c.proxyIngressReports, r)
	}
	if err == nil {
		c.trafficPoliciesPending = false
	}
	return err
}

func (c *Client) ingressReports() []core.ProxyIngressReport {
	c.trafficPolicyMu.Lock()
	defer c.trafficPolicyMu.Unlock()
	return slices.Clone(c.proxyIngressReports)
}

func sharedIngressFrontends(manager *ServiceManager, engine core.Engine, shareID string) ([]core.TLSFrontend, bool, error) {
	spec, err := sharedInstanceSpec(engine, DefaultSpecsForServiceManager(manager.Kind())[engine], shareID, manager.Kind())
	if err != nil {
		return nil, false, err
	}
	tlsFrontendMutex.Lock()
	defer tlsFrontendMutex.Unlock()
	m := &tlsFrontendManager{root: tlsFrontendRoot, state: tlsFrontendState, services: manager}
	deployments, err := m.load()
	if err != nil {
		return nil, false, err
	}
	d := deployments[spec.Service]
	return d.Frontends, d.Stopped, nil
}

func (manager *TrafficManager) authorizeProxyIngresses(shareID string, frontends []core.TLSFrontend) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, f := range frontends {
		if f.SharedIngressGeneration == 0 {
			continue
		}
		allowed := false
		for _, r := range manager.records {
			if q := r.Policy.SharedQuota; q != nil && q.ID == shareID && !q.Revoked && core.ProxyIngressAuthorized(q.ProxyIngresses, f) {
				allowed = true
				break
			}
		}
		if !allowed {
			return errors.New("托管入口授权已撤销或变更，请重新部署本人配置")
		}
	}
	return nil
}

func (c *Client) finishTrafficPolicyTransition() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.applyPendingTrafficPolicies(ctx); err != nil {
		// Keep pending state for the bounded reconnect/heartbeat retry path.
		slog.Warn("apply pending ingress policy", "error", core.DiagnosticText(err.Error()))
	}
	select {
	case c.runtimeRefresh <- struct{}{}:
	default:
	}
}

func proxyIngressRevocationReason() string {
	return "托管入口授权已撤销，实例已停止；重新获授权后请重新部署"
}
