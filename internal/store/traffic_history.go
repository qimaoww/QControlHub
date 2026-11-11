package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// trafficHistoryScopeWhere is shared by detail and dashboard reads. In
// particular, share recipients must not recover history from before their
// current allocation generation merely because the Agent itself is visible.
func trafficHistoryScopeWhere(ctx context.Context, agentID, policyID string, args *[]any) string {
	where := ""
	for _, filter := range []struct{ column, value string }{{"usage.agent_id", strings.TrimSpace(agentID)}, {"usage.policy_id", strings.TrimSpace(policyID)}} {
		if filter.value != "" {
			*args = append(*args, filter.value)
			where += fmt.Sprintf(" AND %s=$%d", filter.column, len(*args))
		}
	}
	where += trafficAccessClause(ctx, "usage.agent_id", "usage.policy_id", args)
	if !scopeForConfig(ctx).Admin {
		*args = append(*args, scopeForConfig(ctx).OwnerID)
		where += fmt.Sprintf(` AND (starts_with($%[1]d::text,'token_')
			OR EXISTS(SELECT 1 FROM agents a WHERE a.id=usage.agent_id AND a.owner_id=$%[1]d)
			OR EXISTS(SELECT 1 FROM port_traffic_policies p WHERE p.id=usage.policy_id AND usage.reset_generation>=p.share_generation))`, len(*args))
	}
	return where
}

func (s *Store) ListPortTrafficDailyUsage(ctx context.Context, agentID, policyID string, month time.Time) ([]core.PortTrafficDailyUsage, error) {
	start := time.Date(month.UTC().Year(), month.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	where := "usage.usage_date >= $1::date AND usage.usage_date < $2::date"
	args := []any{start, end}
	where += trafficHistoryScopeWhere(ctx, agentID, policyID, &args)
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

// DashboardPortTrafficUsage performs all byte aggregation in PostgreSQL. The
// result contains at most one row per calendar day and one row per Agent with
// selected-month records; raw policy/generation rows never cross into Go.
func (s *Store) DashboardPortTrafficUsage(ctx context.Context, agentID, policyID string, month, now time.Time) (core.PortTrafficDashboard, error) {
	start := time.Date(month.UTC().Year(), month.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	now = now.UTC()
	currentDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	currentMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	currentWeek := currentDay.AddDate(0, 0, -(int(currentDay.Weekday())+6)%7)
	result := core.PortTrafficDashboard{
		Month: start.Format("2006-01"), GeneratedAt: now, CurrentDay: currentDay.Format(time.DateOnly),
		Timezone: "UTC", Scope: "monitored_ports",
		Days:  make([]core.PortTrafficDashboardDay, 0, int(end.AddDate(0, 0, -1).Day())),
		Nodes: make([]core.PortTrafficDashboardNode, 0),
	}
	dayIndexes := make(map[string]int, cap(result.Days))
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		key := day.Format(time.DateOnly)
		dayIndexes[key] = len(result.Days)
		result.Days = append(result.Days, core.PortTrafficDashboardDay{Day: key})
	}

	args := []any{start, end}
	where := "usage.usage_date >= $1::date AND usage.usage_date < $2::date"
	where += trafficHistoryScopeWhere(ctx, agentID, policyID, &args)
	rows, err := s.pool.Query(ctx, `
		SELECT usage.usage_date,usage.agent_id,current_agent.name,
			LEAST(9223372036854775807::numeric,SUM(usage.received_bytes))::bigint,
			LEAST(9223372036854775807::numeric,SUM(usage.sent_bytes))::bigint,
			LEAST(9223372036854775807::numeric,SUM(usage.used_bytes))::bigint,
			MAX(usage.peak_receive_bps),MAX(usage.peak_send_bps),
			COUNT(*),LEAST(9223372036854775807::numeric,SUM(usage.sample_count))::bigint,
			MAX(usage.last_reported_at)
		FROM port_traffic_daily_usage usage
		JOIN agents current_agent ON current_agent.id=usage.agent_id
		WHERE `+where+`
		GROUP BY GROUPING SETS ((usage.usage_date),(usage.agent_id,current_agent.name))`, args...)
	if err != nil {
		return core.PortTrafficDashboard{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var day *time.Time
		var nodeID, nodeName *string
		var received, sent, total, peakReceive, peakSend, records, samples uint64
		var lastReported *time.Time
		if err := rows.Scan(&day, &nodeID, &nodeName, &received, &sent, &total, &peakReceive, &peakSend, &records, &samples, &lastReported); err != nil {
			return core.PortTrafficDashboard{}, err
		}
		if day != nil {
			index, ok := dayIndexes[day.UTC().Format(time.DateOnly)]
			if !ok {
				return core.PortTrafficDashboard{}, fmt.Errorf("dashboard day outside selected month: %s", day.UTC().Format(time.DateOnly))
			}
			result.Days[index].ReceivedBytes = received
			result.Days[index].SentBytes = sent
			result.Days[index].TotalBytes = total
			result.Days[index].PeakReceiveBPS = peakReceive
			result.Days[index].PeakSendBPS = peakSend
			result.Days[index].RecordCount = records
			result.Days[index].SampleCount = samples
			result.Days[index].LastReportedAt = lastReported
			continue
		}
		if nodeID != nil {
			name := ""
			if nodeName != nil {
				name = *nodeName
			}
			result.Nodes = append(result.Nodes, core.PortTrafficDashboardNode{
				AgentID: *nodeID, Name: name, ReceivedBytes: received, SentBytes: sent, TotalBytes: total,
				RecordCount: records, SampleCount: samples, LastReportedAt: lastReported,
			})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return core.PortTrafficDashboard{}, err
	}
	sort.Slice(result.Nodes, func(left, right int) bool {
		if result.Nodes[left].TotalBytes != result.Nodes[right].TotalBytes {
			return result.Nodes[left].TotalBytes > result.Nodes[right].TotalBytes
		}
		return result.Nodes[left].AgentID < result.Nodes[right].AgentID
	})

	// All current periods end at tomorrow. Keeping access predicates in the ON
	// clause preserves all three period rows when the authorized history is empty.
	args = []any{currentDay.AddDate(0, 0, 1), currentDay, currentWeek, currentMonth}
	on := "usage.usage_date >= periods.start_date AND usage.usage_date < $1::date"
	on += trafficHistoryScopeWhere(ctx, agentID, policyID, &args)
	periods, err := s.pool.Query(ctx, `
		WITH periods(name,start_date) AS (
			VALUES ('today',$2::date),('week',$3::date),('month',$4::date)
		)
		SELECT periods.name,periods.start_date,
			LEAST(9223372036854775807::numeric,COALESCE(SUM(usage.received_bytes),0))::bigint,
			LEAST(9223372036854775807::numeric,COALESCE(SUM(usage.sent_bytes),0))::bigint,
			LEAST(9223372036854775807::numeric,COALESCE(SUM(usage.used_bytes),0))::bigint,
			COUNT(usage.policy_id),
			LEAST(9223372036854775807::numeric,COALESCE(SUM(usage.sample_count),0))::bigint,
			MAX(usage.last_reported_at)
		FROM periods LEFT JOIN port_traffic_daily_usage usage ON `+on+`
		GROUP BY periods.name,periods.start_date`, args...)
	if err != nil {
		return core.PortTrafficDashboard{}, err
	}
	defer periods.Close()
	for periods.Next() {
		var name string
		var periodStart time.Time
		var period core.PortTrafficDashboardPeriod
		if err := periods.Scan(&name, &periodStart, &period.ReceivedBytes, &period.SentBytes, &period.TotalBytes,
			&period.RecordCount, &period.SampleCount, &period.LastReportedAt); err != nil {
			return core.PortTrafficDashboard{}, err
		}
		period.Start = periodStart.UTC().Format(time.DateOnly)
		switch name {
		case "today":
			result.CurrentPeriod.Today = period
		case "week":
			result.CurrentPeriod.Week = period
		case "month":
			result.CurrentPeriod.Month = period
		default:
			return core.PortTrafficDashboard{}, fmt.Errorf("unknown dashboard period: %s", name)
		}
	}
	periods.Close()
	if err := periods.Err(); err != nil {
		return core.PortTrafficDashboard{}, err
	}
	return result, nil
}
