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

func (c *Client) buildHeartbeatMessage(ctx context.Context) core.WireMessage {
	runtimeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	runtimeState := c.executor.Runtime(runtimeContext)
	cancel()
	if c.logs != nil {
		for engine, logState := range c.logs.Status() {
			state := runtimeState[engine]
			state.CoreLogStatus = logState.Status
			state.CoreLogError = logState.Error
			state.CoreLogSource = logState.Source
			state.CoreLogLastAt = logState.LastAt
			runtimeState[engine] = state
		}
	}
	metrics := core.HostMetrics{ClockSync: c.cachedClockSync(ctx), BBR: c.bbr.Collect(ctx)}
	sharedInstances := c.executor.SharedInstanceStatuses(ctx)
	if c.logs != nil {
		logStates := c.logs.InstanceStatuses()
		for i := range sharedInstances {
			if state, ok := logStates[string(sharedInstances[i].Engine)+"/"+sharedInstances[i].ShareID]; ok {
				sharedInstances[i].CoreLogStatus = state.CoreLogStatus
				sharedInstances[i].CoreLogError = state.CoreLogError
				sharedInstances[i].CoreLogSource = state.CoreLogSource
				sharedInstances[i].CoreLogLastAt = state.CoreLogLastAt
			}
		}
	}
	// An inactive instance can release its reserved ports only after the
	// counter sample includes traffic received before the service stopped.
	for _, instance := range sharedInstances {
		if c.traffic != nil && (instance.Status == "inactive" || instance.Status == "failed") {
			c.traffic.collect(ctx, false)
			break
		}
	}
	c.diagnosticCapabilitiesMu.RLock()
	diagnostics := c.diagnosticCapabilities
	c.diagnosticCapabilitiesMu.RUnlock()
	heartbeat := &core.HeartbeatRequest{
		Diagnostics: &diagnostics, Metrics: &metrics,
		Version: c.config.Version, OS: operatingSystemPlatform(), Arch: runtime.GOARCH, Runtime: runtimeState,
		Features: c.advertisedFeatures(), TrafficUsage: c.traffic.Snapshot(),
		SharedInstances:     sharedInstances,
		ProxyIngressReports: c.ingressReports(), ManagedProxies: readProxyReports(),
	}
	return core.WireMessage{Type: core.WireHeartbeat, Heartbeat: heartbeat}
}

func (c *Client) collectHostMetrics(ctx context.Context) *core.HostMetrics {
	sample, err := c.metrics.Collect(ctx)
	if err != nil {
		slog.Debug("host metrics collection was partial", "error", err)
	}
	return &sample
}

// The writer uses one complete host sample for both telemetry frame types.
// A delayed heartbeat or queued metrics push cannot replay an earlier peak.
// Keep the sample timestamp and network counters together even after clock steps.
func (c *Client) prepareTelemetry(message *core.WireMessage, host *core.HostMetrics) {
	var metrics **core.HostMetrics
	switch message.Type {
	case core.WireHeartbeat:
		metrics = &message.Heartbeat.Metrics
	case core.WireMetrics:
		metrics = &message.Metrics
	default:
		return
	}
	sample := *host
	if extra := *metrics; extra != nil {
		sample.BBR, sample.ClockSync = extra.BBR, extra.ClockSync
	}
	c.publicIP.applySnapshot(&sample)
	if message.Type == core.WireHeartbeat && !metricsHaveData(sample) && sample.BBR == nil {
		*metrics = nil
		return
	}
	// Even an entirely unavailable metrics tick must clear a previous CPU peak.
	*metrics = &sample
}

// advertisedFeatures is identical across service managers: OpenRC nodes
// stream managed core logs from supervise-daemon output_log files while
// systemd nodes stream them from the volatile journal namespace.
func (c *Client) advertisedFeatures() []string {
	features := []string{
		core.AgentFeatureNetworkDiagnostics,
		core.AgentFeatureSelfUpgrade,
		core.AgentFeatureManagedTLS,
		core.AgentFeatureSharedProxyIngress,
		core.AgentFeatureSharedProxyIngressLegacy,
		core.AgentFeatureManagedTLSCertificates,
		core.AgentFeatureCertificates,
		core.AgentFeatureCaddyCertificates,
		core.AgentFeatureNodeCertificates,
		core.AgentFeatureManagedTLSH2,
		core.AgentFeatureXHTTP,
		core.AgentFeatureXHTTPTLS,
		core.AgentFeatureACMEFallback,
		core.AgentFeatureNginxACME,
		"managed-tls-nginx-acme-v1", // Remain compatible with older panels.
		core.AgentFeaturePortTraffic,
		core.AgentFeatureSharedTraffic,
		core.AgentFeatureSharedEngines,
		core.AgentFeatureIndependentEgress,
		core.AgentFeatureCoreLogs,
		core.AgentFeatureCoreLogStatus,
		core.AgentFeatureCoreLogInstances,
		core.AgentFeatureMihomoDevelopmentSource,
		core.AgentFeatureManagedPublicIPProbe,
		core.AgentFeatureManagedPolicy,
		core.AgentFeatureProcessTimeZone,
		core.AgentFeatureManagedConfigRead,
		core.AgentFeatureImportIsolation,
		core.AgentFeatureImportDependencies,
		core.AgentFeatureConfigFiles,
		core.AgentFeaturePairedConfigFiles,
		core.AgentFeaturePresetAutoInstall,
		core.AgentFeatureCoreUninstall,
		core.AgentFeatureCNIPSource,
		core.AgentFeatureTLSMigration, core.AgentFeatureSystemBBR, core.AgentFeatureManagedProxies,
		core.AgentFeatureIPQuality,
		core.AgentFeatureInstanceStatus,
		core.AgentFeatureCompactTelemetry,
	}
	if hostPortObservationSupported() {
		features = append(features, core.AgentFeatureHostPorts)
	}
	if sharedCoreTemplatesSupported(c.executor.Specs, c.executor.serviceManager()) {
		features = append(features, core.AgentFeatureSharedProcessTraffic, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare, core.AgentFeatureSharedCoreLifecycle, core.AgentFeatureSharedCoreRecovery)
	}
	if c.publicIP.Enabled() {
		features = append(features, core.AgentFeaturePublicIPProbe)
	}
	return features
}
