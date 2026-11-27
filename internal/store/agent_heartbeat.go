package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

// Heartbeat records a complete authenticated Agent heartbeat. The advertised
// features are authoritative: an empty, omitted, or [] feature list clears any
// stale value (for example a previous session's mihomo-development-source-v1),
// so a legacy Agent that reconnects cannot inherit a stale capability the
// control plane would otherwise use to dispatch a mirror task. Metrics-only
// refreshes go through UpdateAgentMetrics, which never touches features.
func (s *Store) Heartbeat(ctx context.Context, id string, heartbeat core.HeartbeatRequest) error {
	return s.HeartbeatWithPublicIPProbeTrust(ctx, id, heartbeat, PublicIPProbeTrust{})
}

// HeartbeatWithPublicIPProbeTrust records a complete heartbeat while binding
// managed public-IP provenance to capability and configuration established by
// the current authenticated WSS session.
func (s *Store) HeartbeatWithPublicIPProbeTrust(ctx context.Context, id string, heartbeat core.HeartbeatRequest, trust PublicIPProbeTrust) error {
	receivedAt := time.Now().UTC()
	if len(heartbeat.SharedInstances) > 256 {
		return fmt.Errorf("%w: too many shared instance statuses", ErrInvalid)
	}
	for index := range heartbeat.SharedInstances {
		instance := &heartbeat.SharedInstances[index]
		if instance.CoreLogStatus != "" && !core.ValidCoreLogStatus(instance.CoreLogStatus) {
			return fmt.Errorf("%w: invalid instance log status", ErrInvalid)
		}
		if !validCoreLogSource(instance.CoreLogSource) {
			return fmt.Errorf("%w: invalid instance log source", ErrInvalid)
		}
		instance.CoreLogLastAt = normalizeCoreLogLastAt(instance.CoreLogLastAt, receivedAt)
		if len(instance.CoreLogError) > 100 {
			return fmt.Errorf("%w: invalid instance log error", ErrInvalid)
		}
		if err := validateSharedInstanceStatus(*instance); err != nil {
			return err
		}
	}
	// The main instances carry the same agent-reported fields inside the
	// runtime snapshot. Validate them under the same contract so the directory
	// never has to trust a status, transport, or clock it does not understand.
	for engine, state := range heartbeat.Runtime {
		if state.CoreLogStatus != "" && !core.ValidCoreLogStatus(state.CoreLogStatus) {
			return fmt.Errorf("%w: invalid core log status", ErrInvalid)
		}
		if !validCoreLogSource(state.CoreLogSource) {
			return fmt.Errorf("%w: invalid core log source", ErrInvalid)
		}
		state.CoreLogLastAt = normalizeCoreLogLastAt(state.CoreLogLastAt, receivedAt)
		heartbeat.Runtime[engine] = state
	}
	heartbeat.Version = strings.TrimSpace(heartbeat.Version)
	heartbeat.OS = strings.TrimSpace(heartbeat.OS)
	heartbeat.Arch = strings.TrimSpace(heartbeat.Arch)
	if utf8.RuneCountInString(heartbeat.Version) > 100 {
		return fmt.Errorf("%w: agent version exceeds 100 characters", ErrInvalid)
	}
	if utf8.RuneCountInString(heartbeat.OS) > 50 || utf8.RuneCountInString(heartbeat.Arch) > 50 {
		return fmt.Errorf("%w: agent OS and architecture must not exceed 50 characters", ErrInvalid)
	}
	runtimeState, err := json.Marshal(heartbeat.Runtime)
	if err != nil {
		return err
	}
	if heartbeat.Metrics != nil {
		metrics := *heartbeat.Metrics
		applyPublicIPProbeTrust(&metrics, trust)
		heartbeat.Metrics = &metrics
	}
	metricsState, err := encodeHeartbeatMetrics(heartbeat.Metrics, receivedAt)
	if err != nil {
		return err
	}
	featuresState, err := json.Marshal(heartbeat.Features)
	if err != nil {
		return err
	}
	if len(heartbeat.Features) == 0 {
		featuresState = []byte(`[]`)
	}
	// The observed address is control-plane state that a WSS session supplies
	// with the heartbeat, so it is persisted in its own column rather than left
	// inside the snapshot a later metrics push would replace. A heartbeat
	// without metrics carries no observation and must keep the stored value.
	observedPublicIP := ""
	if heartbeat.Metrics != nil {
		observedPublicIP = heartbeat.Metrics.ObservedPublicIP
	}
	if len(heartbeat.ManagedProxies) > 2 {
		return fmt.Errorf("%w: too many managed proxies", ErrInvalid)
	}
	for mode, row := range heartbeat.ManagedProxies {
		if (mode != "nginx" && mode != "caddy") || len(row.State) > 32 || len(row.Installed) > 120 || len(row.Running) > 120 || len(row.Latest) > 120 || len(row.Outcome) > 160 || len(row.CheckError) > 512 || len(row.UpdateError) > 512 || len(row.UpdateState) > 32 {
			return fmt.Errorf("%w: invalid proxy report", ErrInvalid)
		}
	}
	command, err := s.pool.Exec(ctx, `
			UPDATE agents SET last_seen=now(), version=CASE WHEN $2='' THEN version ELSE $2 END, runtime=$3,
			                  features=$4::jsonb, managed_proxies=$8,
			                  os=CASE WHEN $5='' THEN os ELSE $5 END,
			                  arch=CASE WHEN $6='' THEN arch ELSE $6 END,
			                  observed_public_ip_changed_at=CASE WHEN $7<>'' AND observed_public_ip<>'' AND observed_public_ip<>$7 THEN now() ELSE observed_public_ip_changed_at END,
                                  observed_public_ip_at=CASE WHEN $7<>'' THEN now() ELSE observed_public_ip_at END,
                                  observed_public_ip=CASE WHEN $7='' THEN observed_public_ip ELSE $7 END
			WHERE id=$1 AND revoked_at IS NULL`, id, heartbeat.Version, runtimeState, featuresState, heartbeat.OS, heartbeat.Arch, observedPublicIP, heartbeat.ManagedProxies)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	// The Agent-reported snapshot is large and changes on every push, so it is
	// written to its own narrow table where the update can stay on-page. A
	// heartbeat without metrics reports that this Agent can no longer probe.
	if metricsState == nil {
		if err := s.clearAgentLiveSample(ctx, id); err != nil {
			return err
		}
	} else if err := s.recordAgentLiveState(ctx, id, metricsState); err != nil {
		return err
	}
	if err := s.UpdatePortTrafficUsage(ctx, id, heartbeat.TrafficUsage, receivedAt); err != nil {
		return err
	}
	engines, instances, statuses, logErrors, sources, lastAts := []string{}, []string{}, []string{}, []string{}, []string{}, []string{}
	for _, instance := range heartbeat.SharedInstances {
		if instance.CoreLogStatus != "" {
			lastAt := ""
			if instance.CoreLogLastAt != nil {
				lastAt = instance.CoreLogLastAt.UTC().Format(time.RFC3339Nano)
			}
			engines = append(engines, string(instance.Engine))
			instances = append(instances, instance.ShareID)
			statuses = append(statuses, instance.CoreLogStatus)
			logErrors = append(logErrors, coreLogErrorForStatus(instance.CoreLogStatus, instance.CoreLogError))
			sources = append(sources, instance.CoreLogSource)
			lastAts = append(lastAts, lastAt)
		}
	}
	if len(instances) > 0 {
		if _, err := s.pool.Exec(ctx, `INSERT INTO core_log_source_states(agent_id,engine,instance_id,status,error,source,last_at)
 SELECT $1,r.engine,r.instance,r.status,CASE WHEN r.status='failed' THEN r.error ELSE '' END,r.source,NULLIF(r.last_at,'')::timestamptz FROM (
 SELECT DISTINCT ON(engine,instance) engine,instance,status,error,source,last_at
 FROM unnest($2::text[],$3::text[],$4::text[],$5::text[],$6::text[],$7::text[]) WITH ORDINALITY r(engine,instance,status,error,source,last_at,ordinal)
 ORDER BY engine,instance,ordinal DESC) r
 JOIN agent_shared_instance_ownership o ON o.agent_id=$1 AND o.engine=r.engine AND o.share_id=r.instance
 JOIN agent_shares sh ON sh.id=o.share_id AND sh.agent_id=o.agent_id AND sh.user_id=o.owner_id
  AND sh.enabled AND sh.status='accepted'
 ON CONFLICT(agent_id,engine,instance_id) DO UPDATE SET status=EXCLUDED.status,error=EXCLUDED.error,
  source=EXCLUDED.source,last_at=EXCLUDED.last_at,updated_at=now()`, id, engines, instances, statuses, logErrors, sources, lastAts); err != nil {
			return err
		}
	}
	// A revoked, disabled, or reassigned instance must not keep a status row
	// that a later grant could mistake for a fresh observation.
	if _, err := s.pool.Exec(ctx, `DELETE FROM core_log_source_states state
 WHERE state.agent_id=$1 AND NOT EXISTS (
  SELECT 1 FROM agent_shared_instance_ownership o
  JOIN agent_shares sh ON sh.id=o.share_id AND sh.agent_id=o.agent_id AND sh.user_id=o.owner_id
  WHERE o.agent_id=state.agent_id AND o.engine=state.engine AND o.share_id=state.instance_id
   AND sh.enabled AND sh.status='accepted')`, id); err != nil {
		return err
	}
	if len(heartbeat.SharedInstances) > 0 {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, id).Scan(&locked); err != nil {
			return mapError(err)
		}
		var reportedPolicies []string
		var checkedSettlement bool
		for _, instance := range heartbeat.SharedInstances {
			if !checkedSettlement && (instance.Status == "inactive" || instance.Status == "failed") {
				reportedPolicies, err = settledHeartbeatPolicies(ctx, tx, id, heartbeat.TrafficUsage, receivedAt, containsFeature(heartbeat.Features, core.AgentFeatureSharedProcessTraffic))
				if err != nil {
					return err
				}
				checkedSettlement = true
			}
			if err := recordSharedInstanceObservationTx(ctx, tx, id, instance,
				containsFeature(heartbeat.Features, core.AgentFeatureSharedCoreLifecycle), receivedAt, false, reportedPolicies, false); err != nil {
				return err
			}
		}
		var targets []sharedStartTarget
		if len(reportedPolicies) > 0 && containsFeature(heartbeat.Features, core.AgentFeatureSharedCoreRecovery) {
			targets, err = reserveSharedStarts(ctx, tx, id, receivedAt, reportedPolicies)
			if err != nil {
				return err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		for _, target := range targets {
			if err := s.enqueueSharedStart(ctx, id, receivedAt, target); err != nil {
				slog.Warn("shared instance automatic start deferred", "agent", id, "engine", target.engine, "share", target.share, "error", err)
			}
		}
	}
	return s.recordProxyIngressReports(ctx, id, heartbeat.ProxyIngressReports)
}

// UpdateAgentMetrics refreshes only the live metrics snapshot from the
// high-frequency metrics pushes. The push proves liveness, so last_seen is
// refreshed as well, while version, runtime, and features stay untouched.
func (s *Store) UpdateAgentMetrics(ctx context.Context, id string, metrics core.HostMetrics) error {
	return s.UpdateAgentMetricsWithPublicIPProbeTrust(ctx, id, metrics, PublicIPProbeTrust{})
}

// UpdateAgentMetricsWithPublicIPProbeTrust applies the same current-session
// provenance constraint to metrics-only refreshes without changing persisted
// features.
func (s *Store) UpdateAgentMetricsWithPublicIPProbeTrust(ctx context.Context, id string, metrics core.HostMetrics, trust PublicIPProbeTrust) error {
	applyPublicIPProbeTrust(&metrics, trust)
	metricsState, err := encodeHeartbeatMetrics(&metrics, time.Now().UTC())
	if err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, updateAgentMetricsAndLiveStateSQL, id, metricsState)
	if err != nil {
		return fmt.Errorf("store agent live state: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAgentObservedPublicIP stores the authenticated WSS peer address for the
// client address resolver. It is kept in its own column so that the
// high-frequency metrics snapshot, which never carries this key, cannot
// overwrite it. An empty value removes a stale observation so the resolver
// falls back to the current interface snapshot.
func (s *Store) UpdateAgentObservedPublicIP(ctx context.Context, id, address string) error {
	address = strings.TrimSpace(address)
	if address != "" {
		address = authn.NormalizePublicIP(address)
		if address == "" {
			return fmt.Errorf("%w: invalid observed public agent address", ErrInvalid)
		}
		parsed, err := netip.ParseAddr(address)
		if err != nil || netpolicy.IsCloudflareAddress(parsed) {
			address = ""
		}
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE agents SET observed_public_ip=$2,
		observed_public_ip_at=CASE WHEN $2<>'' THEN now() END,
		observed_public_ip_changed_at=CASE WHEN observed_public_ip<>'' AND observed_public_ip<>$2 THEN now() ELSE observed_public_ip_changed_at END
		WHERE id=$1 AND revoked_at IS NULL`, id, address)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
