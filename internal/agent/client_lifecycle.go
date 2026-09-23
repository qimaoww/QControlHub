package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// ErrIdentityRejected means the control plane rejected the persisted Agent
// identity. It is classified separately from transport failures so the
// reconnect loop can log actionable guidance, but it is retried like any other
// failure: clock skew outside the signing window, a control plane restored from
// a backup, and a re-enrolled identity all recover without a process restart.
var ErrIdentityRejected = errors.New("agent identity was rejected by the control plane")

// nextReconnectBackoff doubles the reconnect delay without exceeding max.
func nextReconnectBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}

// reconnectBackoffLimit returns the ceiling for the next reconnect delay. A
// rejected identity is retried slowly because it usually needs operator action,
// while a transport failure must recover quickly.
func reconnectBackoffLimit(rejected bool) time.Duration {
	if rejected {
		return identityRejectedMaxBackoff
	}
	return maxReconnectBackoff
}

func (c *Client) Run(ctx context.Context) error {
	if executable, err := os.Executable(); err != nil {
		slog.Warn("locate running Agent executable for upgrade cleanup", "error", err)
	} else if removed, reclaimed, err := cleanupAgentUpgradeBackups(executable); err != nil {
		slog.Warn("clean stale Agent upgrade backups", "error", err)
	} else if removed > 0 {
		slog.Info("cleaned stale Agent upgrade backups", "files", removed, "bytes", reclaimed)
	}
	loaded, err := loadCredentials(c.config.StatePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load agent credentials: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		if c.config.EnrollmentToken == "" {
			return errors.New("QCH_ENROLLMENT_TOKEN is required for first enrollment")
		}
		publicKey, privateKey, keyErr := ed25519.GenerateKey(rand.Reader)
		if keyErr != nil {
			return fmt.Errorf("generate agent identity: %w", keyErr)
		}
		loaded, err = c.enroll(ctx, publicKey, privateKey)
		if err != nil {
			return err
		}
		loaded.Server = c.serverHost
		if err := saveCredentials(c.config.StatePath, loaded); err != nil {
			return fmt.Errorf("save agent credentials: %w", err)
		}
	} else if c.shouldReenroll(loaded) {
		// Migrating to another control plane: re-enroll with the supplied token,
		// keep every installed core/config and traffic state on disk, and only
		// rotate the identity. The old panel's completed-task cache is dropped so
		// the migrated Agent never carries two control planes' state side by side.
		// Re-enrollment on the new panel is idempotent for a reusable token bound
		// to the node name.
		enrolled, enrollErr := c.reenroll(ctx)
		if enrollErr != nil {
			return enrollErr
		}
		loaded = enrolled
	} else if loaded.Server == "" {
		loaded.Server = c.serverHost
		if err := saveCredentials(c.config.StatePath, loaded); err != nil {
			return fmt.Errorf("record control-plane host: %w", err)
		}
	}
	c.creds = loaded
	if err := ensureManagedCoreLogStreaming(ctx, c.executor.Specs, c.executor.serviceManager()); err != nil {
		slog.Warn("prepare volatile managed core logs", "error", err)
	}
	// Reclaim snapshots and oversized live cache files before reconnecting.
	// The panel policy will expand this fail-safe minimum after authentication;
	// until then an Agent restart must not retain a larger prior allocation.
	c.logs.ApplyPolicy(core.AgentPolicy{CoreLogMaxMiB: 1, CoreLogRotateCount: 0})
	go c.logs.Run(ctx)
	go c.publicIP.Run(ctx)
	c.executor.migrateNativeAccounting(ctx)
	if c.traffic != nil {
		c.traffic.nativeSource = c.executor.nativeAccounting
		c.traffic.haltShared = func(ctx context.Context, engine core.Engine) error {
			c.executor.specsMu.RLock()
			spec, exists := c.executor.Specs[engine]
			c.executor.specsMu.RUnlock()
			if !exists {
				return errors.New("shared core service is not configured")
			}
			return stopSharedInstances(ctx, c.executor.serviceManager(), engine, spec)
		}
		c.traffic.haltLegacyShared = func(ctx context.Context, engine core.Engine, ports []int) error {
			c.executor.specsMu.RLock()
			spec, exists := c.executor.Specs[engine]
			c.executor.specsMu.RUnlock()
			if !exists {
				return errors.New("shared core service is not configured")
			}
			return stopLegacySharedBase(ctx, c.executor.serviceManager(), engine, spec, ports)
		}
		c.traffic.haltShare = func(ctx context.Context, engine core.Engine, shareID string) error {
			c.executor.specsMu.RLock()
			base, exists := c.executor.Specs[engine]
			c.executor.specsMu.RUnlock()
			if !exists {
				return errors.New("shared core service is not configured")
			}
			spec, err := sharedInstanceSpec(engine, base, shareID, c.executor.serviceManager().Kind())
			if err != nil {
				return err
			}
			if _, err := os.Lstat(filepath.Dir(spec.ConfigPath)); errors.Is(err, os.ErrNotExist) {
				return nil
			} else if err != nil {
				return err
			}
			_, err = c.executor.serviceManager().command(ctx, spec.Service, core.ActionStop)
			return err
		}
		if c.traffic.awaitingPolicies {
			for engine, spec := range c.executor.Specs {
				if err := stopSharedInstances(ctx, c.executor.serviceManager(), engine, spec); err != nil {
					return fmt.Errorf("stop unmetered shared instances before reconnect: %w", err)
				}
			}
		}
	}
	trafficContext, stopTraffic := context.WithCancel(ctx)
	trafficDone := c.traffic.Start(trafficContext)
	defer func() {
		stopTraffic()
		<-trafficDone
	}()
	if c.mainland != nil {
		restoreContext, restoreCancel := context.WithTimeout(ctx, 30*time.Second)
		if err := c.mainland.Restore(restoreContext, c.creds.AgentID); err != nil {
			slog.Warn("restore deployed mainland access policies", "error", err)
		}
		restoreCancel()
	}
	slog.Info("agent identity loaded", "agent_id", c.creds.AgentID, "server", c.websocketURL)
	backoff := time.Second
	// A rejected identity can recover without a restart, so the first rejection
	// of a streak drops local policies and logs actionable guidance; later retries
	// stay quieter until a healthy session resets the streak.
	identityRejectionHandled := false
	for {
		started := time.Now()
		err := c.runWebSocket(ctx)
		if ctx.Err() != nil {
			return nil
		}
		rejected := errors.Is(err, ErrIdentityRejected)
		if !rejected {
			identityRejectionHandled = false
		}
		if rejected && !identityRejectionHandled {
			// A rejected signature is not necessarily permanent: clock skew
			// outside the signing window, a control plane restored from a backup,
			// and a temporarily unavailable identity store all recover without
			// operator intervention. Retry instead of exiting so the node returns
			// to service on its own; local policies are dropped once per rejection
			// streak so a revoked identity stops enforcing stale rules, and the next
			// authenticated session re-applies the panel's policies.
			identityRejectionHandled = true
			slog.Error("control plane rejected the agent identity; retrying with backoff (if this persists, remove the state file and enroll again)",
				"error", err, "state_path", c.config.StatePath)
			cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if cleanupErr := c.traffic.ClearPolicies(cleanupContext); cleanupErr != nil {
				slog.Warn("remove traffic rules for rejected Agent identity", "error", cleanupErr)
			}
			if c.mainland != nil {
				if cleanupErr := c.mainland.Deploy(cleanupContext, nil, c.creds.AgentID); cleanupErr != nil {
					slog.Warn("remove mainland rules for rejected Agent identity", "error", cleanupErr)
				}
			}
			cleanupCancel()
		} else if rejected {
			slog.Warn("control plane still rejects the agent identity; retrying", "error", err, "reconnect_in", backoff)
		}
		if rejected && !c.reenrollAttempted && c.config.EnrollmentToken != "" && time.Since(c.lastReenrollAt) >= minReenrollInterval {
			// A replacement panel serving the same hostname but a fresh identity
			// store rejects the old identity. When an enrollment token is still
			// present, rotate the identity and reconnect instead of giving up; the
			// local cores, configs, and traffic state are untouched. Rotation is
			// rate limited because repeated failed enrollments block the control
			// plane's address, which would also block other nodes behind NAT.
			c.lastReenrollAt = time.Now()
			slog.Warn("control plane rejected identity; attempting one re-enroll", "error", err)
			reenrolled, reenrollErr := c.reenroll(ctx)
			if reenrollErr != nil {
				// A temporary panel outage or an already-consumed token must not
				// kill the process; the backoff below retries the rotation.
				slog.Warn("re-enroll after identity rejection failed", "error", reenrollErr)
			} else {
				c.reenrollAttempted = true
				c.creds = reenrolled
				continue
			}
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		maxBackoff := reconnectBackoffLimit(rejected)
		if !rejected && backoff > maxBackoff {
			// A delay grown during a rejection streak must not slow down an
			// ordinary transport reconnect.
			backoff = maxBackoff
		}
		if !rejected {
			slog.Warn("WSS connection lost", "error", err, "reconnect_in", backoff)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff = nextReconnectBackoff(backoff, maxBackoff)
	}
}

// shouldReenroll reports whether the persisted identity must be rotated before
// connecting because the Agent is being pointed at a different control-plane
// host. A same-host replacement is detected by its rejected WebSocket identity
// and handled by the one-shot recovery path in Run.
func (c *Client) shouldReenroll(loaded credentials) bool {
	// Credentials written by releases before the control-plane migration
	// feature have no Server field. Treat them as belonging to the currently
	// configured panel and persist that host in Run instead of rotating a valid
	// identity during an ordinary in-place Agent update. A forced migration to a
	// replacement panel on the same host first tries the existing identity; the
	// 401 recovery path below then re-enrolls exactly once if that identity does
	// not exist on the replacement panel.
	return c.config.EnrollmentToken != "" && loaded.Server != "" && loaded.Server != c.serverHost
}

func (c *Client) reenroll(ctx context.Context) (credentials, error) {
	if c.config.EnrollmentToken == "" {
		return credentials{}, errors.New("QCH_ENROLLMENT_TOKEN is required to migrate control planes")
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return credentials{}, fmt.Errorf("generate agent identity: %w", err)
	}
	enrolled, err := c.enroll(ctx, publicKey, privateKey)
	if err != nil {
		return credentials{}, err
	}
	// Drop the prior panel's completed-task cache so a migrated Agent does not
	// carry state from two control planes side by side.
	enrolled.Server = c.serverHost
	if err := saveCredentials(c.config.StatePath, enrolled); err != nil {
		return credentials{}, fmt.Errorf("save migrated agent credentials: %w", err)
	}
	return enrolled, nil
}

func (c *Client) enroll(ctx context.Context, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) (credentials, error) {
	request := core.EnrollRequest{
		Name:         c.config.Name,
		Version:      c.config.Version,
		OS:           operatingSystemPlatform(),
		Arch:         runtime.GOARCH,
		Capabilities: c.config.Capabilities,
		Features:     c.advertisedFeatures(),
		Labels:       c.config.Labels,
		PublicKey:    authn.EncodePublicKey(publicKey),
	}
	var response core.EnrollResponse
	if err := c.doJSON(ctx, http.MethodPost, "/agent/v1/enroll", c.config.EnrollmentToken, "", request, &response); err != nil {
		return credentials{}, fmt.Errorf("enroll agent: %w", err)
	}
	if response.AgentID == "" {
		return credentials{}, errors.New("control plane returned incomplete enrollment credentials")
	}
	return credentials{AgentID: response.AgentID, PrivateKey: authn.EncodePrivateKey(privateKey)}, nil
}

func (c *Client) runWebSocket(ctx context.Context) error {
	// A managed probe choice is scoped to the current authenticated WSS
	// session. Clear it before reconnect so an older/downgraded control plane
	// that omits the capability-gated config cannot leave stale addresses behind. A local
	// Agent override is intentionally unaffected.
	if err := c.publicIP.ApplyManagedConfig(core.PublicIPProbeConfig{}); err != nil {
		return err
	}
	privateKey, err := authn.DecodePrivateKey(c.creds.PrivateKey)
	if err != nil {
		return err
	}
	// Bound the entire handshake, including a proxy/server that accepts TCP
	// but never returns HTTP headers. The established session keeps ctx, not
	// this short-lived context, so healthy long-running connections survive.
	handshakeContext, handshakeCancel := context.WithTimeout(ctx, webSocketHandshakeTimeout)
	defer handshakeCancel()
	handshake, err := http.NewRequestWithContext(handshakeContext, http.MethodGet, c.websocketURL, nil)
	if err != nil {
		return err
	}
	if err := authn.SignRequest(handshake, nil, c.creds.AgentID, privateKey, time.Now().UTC()); err != nil {
		return err
	}
	connection, response, err := websocket.Dial(handshakeContext, c.websocketURL, &websocket.DialOptions{
		HTTPClient:      c.http,
		HTTPHeader:      handshake.Header,
		CompressionMode: websocket.CompressionDisabled,
		Subprotocols:    []string{"qcontrolhub.agent.v1"},
	})
	handshakeCancel()
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			contents, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
			if response.StatusCode == http.StatusUnauthorized {
				return fmt.Errorf("%w: WSS handshake returned %s: %s", ErrIdentityRejected, response.Status, strings.TrimSpace(string(contents)))
			}
			return fmt.Errorf("WSS handshake returned %s: %s", response.Status, strings.TrimSpace(string(contents)))
		}
		return explainTLSError(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "agent reconnecting")
	if connection.Subprotocol() != "qcontrolhub.agent.v1" {
		return errors.New("control plane did not negotiate the QControlHub Agent subprotocol")
	}
	connection.SetReadLimit(core.MaxConfigEnvelopeBytes)
	slog.Info("WSS session established", "agent_id", c.creds.AgentID)

	sessionContext, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	incoming := make(chan core.WireMessage, 1)
	outgoing := make(chan core.WireMessage, 16)
	// The main loop may be blocked enqueueing a heartbeat or metrics. Signal
	// failures through cancellation so that producer also wakes immediately;
	// an error channel consumed only by the main loop can deadlock here.
	go func() {
		for {
			var message core.WireMessage
			if err := wsjson.Read(sessionContext, connection, &message); err != nil {
				cancel(err)
				return
			}
			select {
			case incoming <- message:
			case <-sessionContext.Done():
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case <-sessionContext.Done():
				return
			case message := <-outgoing:
				writeContext, writeCancel := context.WithTimeout(sessionContext, 15*time.Second)
				err := wsjson.Write(writeContext, connection, message)
				writeCancel()
				if err != nil {
					cancel(err)
					return
				}
			}
		}
	}()

	heartbeatTicker := time.NewTicker(c.config.HeartbeatEvery)
	defer heartbeatTicker.Stop()
	metricsTicker := time.NewTicker(c.config.MetricsEvery)
	defer metricsTicker.Stop()
	logTicker := time.NewTicker(500 * time.Millisecond)
	defer logTicker.Stop()
	if err := c.queueHeartbeat(sessionContext, outgoing); err != nil {
		return err
	}
	var activeTask string
	var sentLogBatch string
	for {
		select {
		case <-sessionContext.Done():
			if ctx.Err() != nil {
				return nil
			}
			return context.Cause(sessionContext)
		case <-heartbeatTicker.C:
			if err := c.queueHeartbeat(sessionContext, outgoing); err != nil {
				return err
			}
			if c.committedUpgradeAwaitingRestart() {
				go c.reexecAfterUpgrade()
			}
		case <-c.runtimeRefresh:
			if err := c.queueHeartbeat(sessionContext, outgoing); err != nil {
				return err
			}
		case <-metricsTicker.C:
			if err := c.queueMetrics(sessionContext, outgoing); err != nil {
				return err
			}
		case <-logTicker.C:
			if sentLogBatch == "" {
				if batch := c.logs.NextBatch(); batch != nil {
					select {
					case outgoing <- core.WireMessage{Type: core.WireCoreLogs, CoreLogs: batch}:
						sentLogBatch = batch.ID
					default:
					}
				}
			}
		case message := <-incoming:
			switch message.Type {
			case core.WireHello:
				// The result ACK can be lost after the control plane committed
				// it. A subsequent authenticated connection must not leave the
				// old process running forever with a new binary on disk.
				if c.committedUpgradeAwaitingRestart() {
					go c.reexecAfterUpgrade()
				}
				if err := c.traffic.SetPolicies(sessionContext, message.TrafficPolicies, c.creds.AgentID); err != nil {
					return fmt.Errorf("apply control-plane traffic policies: %w", err)
				}
				continue
			case core.WirePublicIPProbe:
				if message.PublicIPProbe == nil {
					return errors.New("control plane returned an invalid public IP probe configuration")
				}
				if err := c.publicIP.ApplyManagedConfig(*message.PublicIPProbe); err != nil {
					return fmt.Errorf("control plane supplied invalid public IP probe configuration: %w", err)
				}
				continue
			case core.WireAgentPolicy:
				if message.AgentPolicy == nil {
					return errors.New("control plane returned an invalid agent policy")
				}
				if err := c.applyAgentPolicy(sessionContext, *message.AgentPolicy); err != nil {
					return fmt.Errorf("apply control-plane agent policy: %w", err)
				}
				heartbeatTicker.Reset(time.Duration(message.AgentPolicy.HeartbeatIntervalSeconds) * time.Second)
				metricsTicker.Reset(time.Duration(message.AgentPolicy.MetricsIntervalSeconds) * time.Second)
				continue
			case core.WireTask:
				if message.Task == nil || activeTask != "" || !c.validTask(*message.Task) {
					return errors.New("control plane returned an invalid or concurrent task envelope")
				}
				task := *message.Task
				activeTask = task.ID
				// A transient WSS disconnect must not terminate an in-flight system
				// operation. Execution follows the Agent process lifetime; only result
				// delivery follows this individual websocket session.
				go c.executeTaskForSession(ctx, sessionContext, task, outgoing)
			case core.WireResultAck:
				if message.TaskID == "" || message.TaskID != activeTask {
					return errors.New("control plane acknowledged an unexpected task")
				}
				slog.Info("task result acknowledged", "task_id", message.TaskID)
				c.acknowledgeTaskResult(message.TaskID)
				activeTask = ""
			case core.WireCoreLogsAck:
				if message.BatchID == "" || message.BatchID != sentLogBatch || !c.logs.Acknowledge(message.BatchID) {
					return errors.New("control plane acknowledged an unexpected core log batch")
				}
				sentLogBatch = ""
			case core.WireError:
				return fmt.Errorf("control plane WSS error: %s", message.Error)
			default:
				return fmt.Errorf("unsupported control-plane message type %q", message.Type)
			}
		}
	}
}
