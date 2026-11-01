package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) agentConnect(w http.ResponseWriter, request *http.Request) {
	// A proxy chain that cannot be unambiguously resolved (for example a CDN
	// edge followed by another untrusted hop) must never be stored or shown as
	// the Agent public address; VerifiedAgentPublicIP returns "" then, which
	// clears any stale observation.
	observedPublicIP := authn.VerifiedAgentPublicIP(request, s.trustedProxies)
	connection, err := websocket.Accept(w, request, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
		Subprotocols:    []string{"qcontrolhub.agent.v1"},
	})
	if err != nil {
		slog.Warn("accept agent websocket", "agent_id", agentID(request), "error", err)
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "connection closed")
	if connection.Subprotocol() != "qcontrolhub.agent.v1" {
		_ = connection.Close(websocket.StatusPolicyViolation, "required subprotocol was not negotiated")
		return
	}
	connection.SetReadLimit(core.MaxCoreLogWireBytes)

	id := agentID(request)
	panelSettings, settingsErr := s.store.AgentPanelSettings(request.Context(), id)
	if settingsErr != nil {
		slog.Warn("load agent policy settings", "agent_id", id, "error", settingsErr)
		panelSettings = core.DefaultPanelSettings()
	}
	agentPolicy := core.AgentPolicy{
		HeartbeatIntervalSeconds: uint32(panelSettings.AgentHeartbeatIntervalSeconds),
		MetricsIntervalSeconds:   uint32(panelSettings.AgentMetricsIntervalSeconds),
		CoreLogMaxMiB:            uint32(panelSettings.AgentCoreLogMaxMiB),
		CoreLogRotateCount:       uint32(panelSettings.AgentCoreLogRotateCount),
	}
	effectivePublicIPProbe := s.publicIPProbe
	effectivePublicIPProbe.IntervalSeconds = uint32(panelSettings.PublicIPProbeIntervalSeconds)
	ctx, cancelConnection := context.WithCancel(request.Context())
	defer cancelConnection()
	connectionID, err := core.NewID("wss")
	if err != nil {
		return
	}
	trafficRefresh := s.registerConnection(id, connectionID, cancelConnection)
	timeZoneRefresh := s.agentTimeZoneRefreshChannel(id, connectionID)
	defer s.unregisterConnection(id, connectionID)
	if err := s.store.UpdateAgentObservedPublicIP(ctx, id, observedPublicIP); err != nil {
		// Address discovery is best-effort and must never take an authenticated
		// Agent offline. Do not log the observed address.
		slog.Warn("update agent WSS address observation", "agent_id", id, "error", err)
	}
	incoming := make(chan core.WireMessage, 1)
	readErrors := make(chan error, 1)
	go func() {
		for {
			var message core.WireMessage
			if err := wsjson.Read(ctx, connection, &message); err != nil {
				readErrors <- err
				return
			}
			select {
			case incoming <- message:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Listener accounting is created from saved node configurations, not from
	// quota forms. Reconcile before every session hello so a freshly installed
	// or long-offline Agent starts monitoring immediately after it connects.
	s.refreshPortTrafficMonitoring(ctx, id)
	trafficPolicies, err := s.store.AgentPortTrafficPolicies(ctx, id)
	if err != nil {
		slog.Error("load agent traffic policies", "agent_id", id, "error", err)
		return
	}
	if err := writeWire(ctx, connection, trafficPolicyHello(trafficPolicies, false)); err != nil {
		return
	}
	taskTicker := time.NewTicker(2 * time.Second)
	defer taskTicker.Stop()
	heartbeatTimeout := time.Duration(panelSettings.AgentOfflineThresholdSeconds+5) * time.Second
	heartbeatDeadline := time.NewTimer(heartbeatTimeout)
	defer heartbeatDeadline.Stop()
	resetHeartbeatDeadline := func() {
		if !heartbeatDeadline.Stop() {
			select {
			case <-heartbeatDeadline.C:
			default:
			}
		}
		heartbeatDeadline.Reset(heartbeatTimeout)
	}
	var inFlightTask string
	var inFlightAction core.Action
	// Dispatch is deferred until this connection has supplied a heartbeat that
	// was persisted. Reading the stored features before the first heartbeat
	// could observe a stale mihomo-development-source-v1 left by a previous
	// session and deliver a mirror task to an older Agent that ignores the
	// unknown core_source. Once the heartbeat is committed, ClaimTask and
	// RunningTask see the connection's real features and gate mirror work.
	var heartbeatReceived bool
	var agentHasFeatureFromStore bool
	var agentHasManagedCertificates bool
	var lastManagedScan time.Time
	var managedPublicIPProbe bool
	var managedAgentPolicy bool
	var managedProcessTimeZone bool
	var sharedEnginesSupported, independentEgressSupported bool
	var timeZoneAckRevision int64
	var timeZoneAckDeadline time.Time
	var lastTimeZoneAddressHash string
	defer func() {
		if timeZoneAckDeadline.IsZero() {
			return
		}
		cleanupContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.store.ResetAgentTimeZoneApplication(cleanupContext, id, timeZoneAckRevision)
	}()
	publicIPProbeTrust := func() store.PublicIPProbeTrust {
		if !heartbeatReceived || !managedPublicIPProbe {
			return store.PublicIPProbeTrust{}
		}
		return store.PublicIPProbeTrust{
			ControlPlaneIPv4: strings.TrimSpace(effectivePublicIPProbe.IPv4Endpoint) != "",
			ControlPlaneIPv6: strings.TrimSpace(effectivePublicIPProbe.IPv6Endpoint) != "",
		}
	}
	sendAgentTimeZone := func(force bool) error {
		if !heartbeatReceived {
			return nil
		}
		state, err := s.store.GetAgentTimeZone(ctx, id)
		if err != nil {
			return err
		}
		if !managedProcessTimeZone {
			if state.TimeZone != "" {
				_ = s.store.SetAgentTimeZoneStatus(ctx, id, state.Revision, "unsupported", "请升级 Agent 后再应用时区")
			}
			return nil
		}
		if state.Status == "unsupported" {
			if err := s.store.SetAgentTimeZoneStatus(ctx, id, state.Revision, "pending", ""); err != nil {
				return err
			}
			state.Status = "pending"
		}
		if !agentTimeZoneCommandNeeded(state, timeZoneAckRevision, force) {
			return nil
		}
		if err := s.store.SetAgentTimeZoneStatus(ctx, id, state.Revision, "applying", ""); err != nil {
			return err
		}
		command := core.AgentTimeZoneCommand{Revision: state.Revision, TimeZone: state.TimeZone}
		// Record the in-flight revision before writing. If the socket write
		// fails, the connection cleanup can move applying back to pending.
		timeZoneAckRevision = state.Revision
		timeZoneAckDeadline = time.Now().Add(15 * time.Second)
		if err := writeWire(ctx, connection, core.WireMessage{Type: core.WireAgentTimeZone, AgentTimeZone: &command}); err != nil {
			return err
		}
		return nil
	}
	reconcileTimeZoneMetrics := func(metrics *core.HostMetrics) error {
		if !heartbeatReceived || metrics == nil {
			return nil
		}
		addressHash := timeZoneAddressHash(agentTimeZoneAddress(core.Agent{Metrics: *metrics}))
		if addressHash == lastTimeZoneAddressHash {
			return nil
		}
		if _, err := s.resolveAgentTimeZone(ctx, id, false); err != nil {
			slog.Warn("reconcile agent process time zone", "agent_id", id, "error", err)
			return nil
		}
		lastTimeZoneAddressHash = addressHash
		return sendAgentTimeZone(false)
	}
	resumeRunning := true
	dispatchTask := func() error {
		if inFlightTask != "" || !heartbeatReceived {
			return nil
		}
		var task *core.Task
		var err error
		if resumeRunning {
			resumeRunning = false
			task, err = s.store.RunningTask(ctx, id)
		}
		if err == nil && task == nil {
			task, err = s.store.ClaimTask(ctx, id)
		}
		if err != nil {
			return err
		}
		if task == nil {
			return nil
		}
		if task.SharedTrafficID != "" {
			// Send the reservation and its current allowance before execution,
			// even when task-ready won the select race with policy-refresh.
			policies, err := s.store.AgentPortTrafficPolicies(ctx, id)
			if err != nil {
				return err
			}
			if err := writeWire(ctx, connection, trafficPolicyHello(policies, heartbeatReceived)); err != nil {
				return err
			}
		}
		if err := writeWire(ctx, connection, core.WireMessage{Type: core.WireTask, Task: task}); err != nil {
			return err
		}
		inFlightTask = task.ID
		inFlightAction = task.Action
		return nil
	}
	taskReady := s.store.TaskReady(id)
	if err := dispatchTask(); err != nil {
		slog.Error("dispatch initial task for websocket", "agent_id", id, "error", err)
		return
	}

	for {
		select {
		case <-trafficRefresh:
			policies, err := s.store.AgentPortTrafficPolicies(ctx, id)
			if err != nil {
				return
			}
			if err := writeWire(ctx, connection, trafficPolicyHello(policies, heartbeatReceived)); err != nil {
				return
			}
		case <-timeZoneRefresh:
			if err := sendAgentTimeZone(false); err != nil {
				return
			}
		case <-ctx.Done():
			return
		case err := <-readErrors:
			status := websocket.CloseStatus(err)
			if status != websocket.StatusNormalClosure && status != websocket.StatusGoingAway && !errors.Is(err, context.Canceled) {
				slog.Warn("agent websocket read failed", "agent_id", id, "error", err)
			}
			return
		case <-heartbeatDeadline.C:
			_ = connection.Close(websocket.StatusPolicyViolation, "heartbeat timeout")
			return
		case message := <-incoming:
			switch message.Type {
			case core.WireManagedCertificateReport:
				if !heartbeatReceived || message.ManagedCertificateReport == nil {
					return
				}
				// Never log the envelope or its key material. Failed imports are
				// persisted as sanitized source status and retried on next scan.
				_ = s.store.ImportManagedCertificate(ctx, id, *message.ManagedCertificateReport)
			case core.WireHeartbeat:
				if message.Heartbeat == nil {
					_ = connection.Close(websocket.StatusPolicyViolation, "invalid heartbeat")
					return
				}
				if message.Heartbeat.Metrics != nil {
					message.Heartbeat.Metrics.ObservedPublicIP = observedPublicIP
				}
				agentHasFeatureFromStore = agentHasFeature(message.Heartbeat.Features, core.AgentFeatureCertificates)
				agentHasManagedCertificates = agentHasFeature(message.Heartbeat.Features, core.AgentFeatureCaddyCertificates)
				reportedManagedPublicIPProbe := agentHasFeature(message.Heartbeat.Features, core.AgentFeatureManagedPublicIPProbe)
				reportedManagedAgentPolicy := agentHasFeature(message.Heartbeat.Features, core.AgentFeatureManagedPolicy)
				reportedProcessTimeZone := agentHasFeature(message.Heartbeat.Features, core.AgentFeatureProcessTimeZone)
				reportedSharedEngines := agentHasFeature(message.Heartbeat.Features, core.AgentFeatureSharedEngines)
				reportedIndependentEgress := agentHasFeature(message.Heartbeat.Features, core.AgentFeatureIndependentEgress)
				capabilityChanged := heartbeatReceived && (reportedManagedPublicIPProbe != managedPublicIPProbe ||
					reportedManagedAgentPolicy != managedAgentPolicy || reportedProcessTimeZone != managedProcessTimeZone || reportedSharedEngines != sharedEnginesSupported ||
					reportedIndependentEgress != independentEgressSupported)
				trust := publicIPProbeTrust()
				if !heartbeatReceived || capabilityChanged {
					trust = store.PublicIPProbeTrust{}
				}
				if err := s.store.HeartbeatWithPublicIPProbeTrust(ctx, id, *message.Heartbeat, trust); err != nil {
					slog.Error("store agent heartbeat", "agent_id", id, "error", err)
					return
				}
				resetHeartbeatDeadline()
				if capabilityChanged {
					_ = connection.Close(websocket.StatusPolicyViolation, "agent features changed during session")
					return
				}
				if !heartbeatReceived {
					heartbeatReceived = true
					managedPublicIPProbe = reportedManagedPublicIPProbe
					managedAgentPolicy = reportedManagedAgentPolicy
					managedProcessTimeZone = reportedProcessTimeZone
					sharedEnginesSupported = reportedSharedEngines
					independentEgressSupported = reportedIndependentEgress
					// Until this heartbeat, cached features could have belonged
					// to a newer Agent. Only now may shared ports be unblocked.
					policies, err := s.store.AgentPortTrafficPolicies(ctx, id)
					if err != nil {
						return
					}
					// Send even an empty policy set: a share may have been revoked
					// while this Agent was disconnected.
					if hasSharedTrafficPolicies(trafficPolicies) || hasSharedTrafficPolicies(policies) {
						if err := writeWire(ctx, connection, trafficPolicyHello(policies, true)); err != nil {
							return
						}
					}
					if managedPublicIPProbe {
						probeConfig := effectivePublicIPProbe
						if err := writeWire(ctx, connection, core.WireMessage{Type: core.WirePublicIPProbe, PublicIPProbe: &probeConfig}); err != nil {
							return
						}
					}
					if managedAgentPolicy {
						if err := writeWire(ctx, connection, core.WireMessage{Type: core.WireAgentPolicy, AgentPolicy: &agentPolicy}); err != nil {
							return
						}
					}
					if _, err := s.resolveAgentTimeZone(ctx, id, false); err != nil {
						slog.Warn("reconcile agent process time zone", "agent_id", id, "error", err)
					}
					if agent, err := s.store.GetAgent(ctx, id); err == nil {
						lastTimeZoneAddressHash = timeZoneAddressHash(agentTimeZoneAddress(agent))
					}
					if err := sendAgentTimeZone(true); err != nil {
						return
					}
					if err := dispatchTask(); err != nil {
						slog.Error("dispatch after first heartbeat", "agent_id", id, "error", err)
						return
					}
				} else if err := reconcileTimeZoneMetrics(message.Heartbeat.Metrics); err != nil {
					return
				}
			case core.WireMetrics:
				if message.Metrics == nil {
					_ = connection.Close(websocket.StatusPolicyViolation, "invalid metrics")
					return
				}
				message.Metrics.ObservedPublicIP = observedPublicIP
				if err := s.store.UpdateAgentMetricsWithPublicIPProbeTrust(ctx, id, *message.Metrics, publicIPProbeTrust()); err != nil {
					slog.Error("store agent metrics", "agent_id", id, "error", err)
					return
				}
				if err := reconcileTimeZoneMetrics(message.Metrics); err != nil {
					return
				}
				if err := s.store.UpdatePortTrafficUsage(ctx, id, message.TrafficUsage, time.Now().UTC()); err != nil {
					slog.Error("store agent traffic metrics", "agent_id", id, "error", err)
					return
				}
				resetHeartbeatDeadline()
			case core.WireCoreLogs:
				if message.CoreLogs == nil {
					_ = connection.Close(websocket.StatusPolicyViolation, "invalid core log batch")
					return
				}
				if err := s.store.StoreCoreLogs(ctx, id, *message.CoreLogs); err != nil {
					slog.Warn("store core log batch", "agent_id", id, "batch_id", message.CoreLogs.ID, "error", err)
					_ = connection.Close(websocket.StatusPolicyViolation, "core log batch rejected")
					return
				}
				if err := writeWire(ctx, connection, core.WireMessage{Type: core.WireCoreLogsAck, BatchID: message.CoreLogs.ID}); err != nil {
					return
				}
			case core.WireResult:
				if message.Result == nil || message.Result.TaskID == "" || message.Result.TaskID != inFlightTask {
					_ = connection.Close(websocket.StatusPolicyViolation, "unexpected task result")
					return
				}
				if len(message.TrafficUsage) > 0 {
					if err := s.store.UpdatePortTrafficUsage(ctx, id, message.TrafficUsage, time.Now().UTC()); err != nil {
						slog.Warn("flush task traffic result", "agent_id", id, "error", err)
						return
					}
				}
				if err := s.completeAgentTask(ctx, id, message.Result.TaskID, &message.Result.Result); err != nil {
					slog.Warn("store task result", "agent_id", id, "task_id", message.Result.TaskID, "error", err)
					_ = connection.Close(websocket.StatusPolicyViolation, "task result rejected")
					return
				}
				if message.Result.Result.Success && (inFlightAction == core.ActionDeploy || inFlightAction == core.ActionImportExisting) {
					s.refreshAgentTrafficPolicies(id)
				}
				s.notifyTaskFailure(id, message.Result.TaskID, message.Result.Result)
				if err := writeWire(ctx, connection, core.WireMessage{Type: core.WireResultAck, TaskID: message.Result.TaskID}); err != nil {
					return
				}
				inFlightTask = ""
				if err := dispatchTask(); err != nil {
					slog.Error("dispatch next task for websocket", "agent_id", id, "error", err)
					return
				}
			case core.WireAgentTimeZoneResult:
				if !heartbeatReceived || !managedProcessTimeZone || message.AgentTimeZoneResult == nil {
					_ = connection.Close(websocket.StatusPolicyViolation, "unexpected Agent time zone result")
					return
				}
				result := *message.AgentTimeZoneResult
				if result.Revision < 1 || !core.ValidTimeZone(result.TimeZone) {
					_ = connection.Close(websocket.StatusPolicyViolation, "invalid Agent time zone result")
					return
				}
				if result.Revision != timeZoneAckRevision {
					continue
				}
				s.handleAgentTimeZoneResult(ctx, id, result)
				if result.Revision == timeZoneAckRevision {
					timeZoneAckDeadline = time.Time{}
					if result.Error == "" {
						timeZoneAckRevision = result.Revision
					}
				}
			default:
				_ = connection.Close(websocket.StatusUnsupportedData, "unsupported message type")
				return
			}
		case <-taskReady:
			if err := dispatchTask(); err != nil {
				slog.Error("dispatch signaled task for websocket", "agent_id", id, "error", err)
				return
			}
		case <-taskTicker.C:
			if !timeZoneAckDeadline.IsZero() && !time.Now().Before(timeZoneAckDeadline) {
				_ = s.store.SetAgentTimeZoneStatus(ctx, id, timeZoneAckRevision, "failed", "Agent 响应超时，请重试")
				timeZoneAckDeadline = time.Time{}
			}
			if heartbeatReceived {
				if agentHasManagedCertificates && time.Since(lastManagedScan) > 30*time.Second {
					sources, sourceErr := s.store.AgentManagedCertificateSources(ctx, id)
					if sourceErr == nil {
						if err := writeWire(ctx, connection, core.WireMessage{Type: core.WireManagedCertificateSources, ManagedCertificateSources: sources}); err != nil {
							return
						}
					}
					lastManagedScan = time.Now()
				}
				routes, routeErr := s.store.AgentCertificateChallenges(ctx, id)
				if routeErr != nil {
					return
				}
				if agentHasFeatureFromStore {
					if err := writeWire(ctx, connection, core.WireMessage{Type: core.WireCertificateChallenges, CertificateChallenges: routes}); err != nil {
						return
					}
				}
			}
			if err := dispatchTask(); err != nil {
				slog.Error("claim fallback task for websocket", "agent_id", id, "error", err)
				return
			}
		}
	}
}

func writeWire(parent context.Context, connection *websocket.Conn, message core.WireMessage) error {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	return wsjson.Write(ctx, connection, message)
}
