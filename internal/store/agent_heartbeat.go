package store

import (
	"context"
	"encoding/json"
	"fmt"
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
	command, err := s.pool.Exec(ctx, `
			UPDATE agents SET last_seen=now(), version=CASE WHEN $2='' THEN version ELSE $2 END, runtime=$3,
			                  features=$4::jsonb,
			                  os=CASE WHEN $5='' THEN os ELSE $5 END,
			                  arch=CASE WHEN $6='' THEN arch ELSE $6 END,
			                  observed_public_ip=CASE WHEN $7='' THEN observed_public_ip ELSE $7 END
			WHERE id=$1 AND revoked_at IS NULL`, id, heartbeat.Version, runtimeState, featuresState, heartbeat.OS, heartbeat.Arch, observedPublicIP)
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
		if err := s.clearAgentLiveStateProbes(ctx, id); err != nil {
			return err
		}
	} else if err := s.recordAgentLiveState(ctx, id, metricsState); err != nil {
		return err
	}
	return s.UpdatePortTrafficUsage(ctx, id, heartbeat.TrafficUsage, receivedAt)
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
	command, err := s.pool.Exec(ctx, `
			UPDATE agents SET last_seen=now()
			WHERE id=$1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return s.recordAgentLiveState(ctx, id, metricsState)
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
		UPDATE agents SET observed_public_ip=$2
		WHERE id=$1 AND revoked_at IS NULL`, id, address)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
