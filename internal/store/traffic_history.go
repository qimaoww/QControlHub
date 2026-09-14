package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) ListPortTrafficDailyUsage(ctx context.Context, agentID, policyID string, month time.Time) ([]core.PortTrafficDailyUsage, error) {
	start := time.Date(month.UTC().Year(), month.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	where := "usage_date >= $1::date AND usage_date < $2::date"
	args := []any{start, end}
	for _, filter := range []struct{ column, value string }{{"agent_id", strings.TrimSpace(agentID)}, {"policy_id", strings.TrimSpace(policyID)}} {
		if filter.value != "" {
			args = append(args, filter.value)
			where += fmt.Sprintf(" AND %s=$%d", filter.column, len(args))
		}
	}
	where += trafficAccessClause(ctx, "usage.agent_id", "usage.policy_id", &args)
	if !scopeForConfig(ctx).Admin {
		args = append(args, scopeForConfig(ctx).OwnerID)
		where += fmt.Sprintf(` AND (starts_with($%[1]d::text,'token_')
			OR EXISTS(SELECT 1 FROM agents a WHERE a.id=usage.agent_id AND a.owner_id=$%[1]d)
			OR EXISTS(SELECT 1 FROM port_traffic_policies p WHERE p.id=usage.policy_id AND usage.reset_generation>=p.share_generation))`, len(args))
	}
	// Aggregate generations and select their latest metadata in one windowed
	// scan, avoiding the second scan and join of materialized intermediate
	// results that caused timeout spikes in the mixed-load regression test.
	rows, err := s.pool.Query(ctx, `
		WITH daily AS (
			SELECT DISTINCT ON (policy_id,usage_date)
			       policy_id,agent_id,name,engine,port,protocol,usage_date,
			       LEAST(9223372036854775807::numeric,SUM(received_bytes) OVER day)::bigint AS received_bytes,
			       LEAST(9223372036854775807::numeric,SUM(sent_bytes) OVER day)::bigint AS sent_bytes,
			       LEAST(9223372036854775807::numeric,SUM(used_bytes) OVER day)::bigint AS used_bytes,
			       MAX(peak_receive_bps) OVER day AS peak_receive_bps,MAX(peak_send_bps) OVER day AS peak_send_bps,
			       LEAST(9223372036854775807::numeric,SUM(sample_count) OVER day)::bigint AS sample_count,
			       MIN(first_reported_at) OVER day AS first_reported_at,MAX(last_reported_at) OVER day AS last_reported_at
			FROM port_traffic_daily_usage AS usage WHERE `+where+`
			WINDOW day AS (PARTITION BY policy_id,usage_date)
			ORDER BY policy_id,usage_date,usage.last_reported_at DESC,reset_generation DESC
		)
		SELECT * FROM daily ORDER BY usage_date,agent_id,port,policy_id LIMIT 100000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.PortTrafficDailyUsage, 0)
	for rows.Next() {
		var item core.PortTrafficDailyUsage
		var day time.Time
		if err := rows.Scan(
			&item.PolicyID, &item.AgentID, &item.Name, &item.Engine, &item.Port, &item.Protocol, &day,
			&item.ReceivedBytes, &item.SentBytes, &item.UsedBytes, &item.PeakReceiveBPS, &item.PeakSendBPS,
			&item.SampleCount, &item.FirstReportedAt, &item.LastReportedAt,
		); err != nil {
			return nil, err
		}
		item.Day = day.UTC().Format(time.DateOnly)
		result = append(result, item)
	}
	return result, rows.Err()
}
