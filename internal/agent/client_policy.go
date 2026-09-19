package agent

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (c *Client) applyAgentPolicy(ctx context.Context, policy core.AgentPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	c.logs.ApplyPolicy(policy)
	if c.executor.serviceManager().Kind() == ServiceManagerSystemd {
		if err := ensureManagedCoreLogStreamingWithPolicy(ctx, c.executor.Specs, policy, c.executor.serviceManager()); err != nil {
			// Local log tuning is best-effort just like initial journal setup. A
			// host-specific systemd limitation must not put the Agent into a WSS
			// reconnect loop or prevent heartbeat/metrics policy from applying.
			slog.Warn("apply systemd core log limits", "error", err)
		}
	}
	return nil
}

func (c *Client) queueHeartbeat(ctx context.Context, outgoing chan<- core.WireMessage) error {
	runtimeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	runtimeState := c.executor.Runtime(runtimeContext)
	cancel()
	if c.logs != nil {
		for engine, logState := range c.logs.Status() {
			state := runtimeState[engine]
			state.CoreLogStatus = logState.Status
			state.CoreLogError = logState.Error
			runtimeState[engine] = state
		}
	}
	metrics, metricsErr := c.metrics.Collect(ctx)
	if metricsErr != nil {
		slog.Debug("host metrics collection was partial", "error", metricsErr)
	}
	metrics.PublicIPv4, metrics.PublicIPv6, metrics.PublicIPv4Source, metrics.PublicIPv6Source = c.publicIP.SnapshotWithSources()
	metrics.BBR = c.bbr.Collect(ctx)
	heartbeat := &core.HeartbeatRequest{
		Version: c.config.Version, OS: operatingSystemPlatform(), Arch: runtime.GOARCH, Runtime: runtimeState,
		Features: c.advertisedFeatures(), TrafficUsage: c.traffic.Snapshot(),
		ClientConnections: c.clientConnectionReport(ctx),
	}
	if metricsHaveData(metrics) || metrics.BBR != nil {
		heartbeat.Metrics = &metrics
	}
	message := core.WireMessage{Type: core.WireHeartbeat, Heartbeat: heartbeat}
	select {
	case outgoing <- message:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// queueMetrics sends a lightweight metrics-only push between full heartbeats
// so the panel's live resource values refresh at the configured cadence
// instead of waiting for the heartbeat cycle. Samples without a CPU reading
// are skipped so one partial collection never overwrites the last complete
// snapshot stored on the control plane.
func (c *Client) queueMetrics(ctx context.Context, outgoing chan<- core.WireMessage) error {
	metrics, metricsErr := c.metrics.Collect(ctx)
	if metricsErr != nil {
		slog.Debug("host metrics collection was partial", "error", metricsErr)
	}
	if !metricsHaveData(metrics) || !metrics.CPUAvailable {
		return nil
	}
	metrics.PublicIPv4, metrics.PublicIPv6, metrics.PublicIPv4Source, metrics.PublicIPv6Source = c.publicIP.SnapshotWithSources()
	metrics.BBR = c.bbr.Cached()
	message := core.WireMessage{Type: core.WireMetrics, Metrics: &metrics, TrafficUsage: c.traffic.Snapshot()}
	select {
	case outgoing <- message:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// advertisedFeatures is identical across service managers: OpenRC nodes
// stream managed core logs from supervise-daemon output_log files while
// systemd nodes stream them from the volatile journal namespace.
func (c *Client) advertisedFeatures() []string {
	features := []string{
		core.AgentFeatureSelfUpgrade,
		core.AgentFeatureClientConnections,
		core.AgentFeaturePortTraffic,
		core.AgentFeatureSharedTraffic,
		core.AgentFeatureSharedEngines,
		core.AgentFeatureIndependentEgress,
		core.AgentFeatureCoreLogs,
		core.AgentFeatureCoreLogStatus,
		core.AgentFeatureMihomoDevelopmentSource,
		core.AgentFeatureManagedPublicIPProbe,
		core.AgentFeatureManagedPolicy,
		core.AgentFeatureManagedConfigRead,
		core.AgentFeatureConfigFiles,
		core.AgentFeaturePairedConfigFiles,
		core.AgentFeaturePresetAutoInstall,
		core.AgentFeatureCNIPSource,
		core.AgentFeatureSystemBBR,
	}
	if c.publicIP.Enabled() {
		features = append(features, core.AgentFeaturePublicIPProbe)
	}
	return features
}
