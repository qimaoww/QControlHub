package store

import (
	"context"
	"fmt"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// RecordAgentMetricSamples snapshots the current metrics of every active
// agent into metric_samples in a single statement. It is meant to run on a
// low-frequency schedule (about once a minute) so the history stays cheap
// while still showing meaningful trends.
func (s *Store) RecordAgentMetricSamples(ctx context.Context, sampledAt time.Time) (int64, error) {
	result, err := s.pool.Exec(ctx, `
		INSERT INTO metric_samples (
			agent_id, sampled_at, cpu_percent, memory_percent, rx_rate_bps, tx_rate_bps
		)
		SELECT id, $1,
			CASE WHEN metrics->>'cpu_available' = 'true' THEN (metrics->>'cpu_percent')::real ELSE 0 END,
			CASE WHEN metrics->>'memory_available' = 'true' AND (metrics->>'memory_total_bytes')::bigint > 0
				THEN round(100.0 * (metrics->>'memory_used_bytes')::bigint / (metrics->>'memory_total_bytes')::bigint)
				ELSE 0 END,
			CASE WHEN metrics->>'network_available' = 'true' THEN (metrics->>'network_rx_bps')::bigint ELSE 0 END,
			CASE WHEN metrics->>'network_available' = 'true' THEN (metrics->>'network_tx_bps')::bigint ELSE 0 END
		FROM agents
		WHERE revoked_at IS NULL AND metrics IS NOT NULL AND metrics <> '{}'::jsonb
		ON CONFLICT (agent_id, sampled_at) DO NOTHING`, sampledAt)
	if err != nil {
		return 0, fmt.Errorf("record metric samples: %w", err)
	}
	return result.RowsAffected(), nil
}

// MetricSamples returns historical samples for one agent ordered oldest first.
// The caller picks the window (for example the last 24 hours) and a limit so
// chart rendering stays bounded.
func (s *Store) MetricSamples(ctx context.Context, agentID string, since time.Time, limit int) ([]core.MetricSample, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sampled_at, cpu_percent, memory_percent, rx_rate_bps, tx_rate_bps
		FROM metric_samples
		WHERE agent_id=$1 AND sampled_at >= $2
		ORDER BY sampled_at ASC
		LIMIT $3`, agentID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("read metric samples: %w", err)
	}
	defer rows.Close()
	samples := make([]core.MetricSample, 0, 32)
	for rows.Next() {
		var sample core.MetricSample
		var rxRate, txRate int64
		if err := rows.Scan(&sample.SampledAt, &sample.CPUPercent, &sample.MemoryPercent, &rxRate, &txRate); err != nil {
			return nil, fmt.Errorf("scan metric sample: %w", err)
		}
		sample.RXRateBPS = uint64(rxRate)
		sample.TXRateBPS = uint64(txRate)
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metric samples: %w", err)
	}
	return samples, nil
}

// PruneMetricSamples deletes samples older than the retention window and
// returns the number of removed rows.
func (s *Store) PruneMetricSamples(ctx context.Context, olderThan time.Time) (int64, error) {
	result, err := s.pool.Exec(ctx, `DELETE FROM metric_samples WHERE sampled_at < $1`, olderThan)
	if err != nil {
		return 0, fmt.Errorf("prune metric samples: %w", err)
	}
	return result.RowsAffected(), nil
}
