package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) UpdatePortTrafficUsage(ctx context.Context, agentID string, usages []core.PortTrafficUsage, reportedAt time.Time) error {
	if len(usages) > 256 {
		return fmt.Errorf("%w: too many traffic usage records", ErrInvalid)
	}
	if len(usages) == 0 {
		return nil
	}
	// PostgreSQL stores microseconds. Normalize before deduplication so the
	// nanosecond tail of the same wire sample cannot look newer on retry.
	reportedAt = reportedAt.UTC().Truncate(time.Microsecond)
	usages = slices.Clone(usages)
	for i := range usages {
		usages[i].CollectedAt = usages[i].CollectedAt.UTC().Truncate(time.Microsecond)
	}
	seen := make(map[string]struct{}, len(usages))
	ids := make([]string, 0, len(usages))
	for _, usage := range usages {
		if (usage.ShareID != "" && !core.ValidAgentShareID(usage.ShareID)) ||
			usage.ShareUsedBytes > math.MaxInt64 || (usage.ShareID == "" && usage.ShareUsedBytes != 0) {
			return fmt.Errorf("%w: invalid shared traffic usage", ErrInvalid)
		}
		if !usage.ProcessUsage.Valid() || usage.ProcessUsage != nil && usage.ShareID == "" {
			return fmt.Errorf("%w: invalid shared process sample", ErrInvalid)
		}
		if !usage.Accounting.Valid() || usage.Accounting != nil && usage.CounterEpoch == "" {
			return fmt.Errorf("%w: invalid traffic accounting metadata", ErrInvalid)
		}
		if _, exists := seen[usage.PolicyID]; exists {
			return fmt.Errorf("%w: duplicate traffic policy usage", ErrInvalid)
		}
		seen[usage.PolicyID] = struct{}{}
		if !core.ValidPortTrafficPolicyID(usage.PolicyID) || usage.ResetGeneration == 0 || usage.ResetGeneration > math.MaxInt64 ||
			usage.ReceivedBytes > math.MaxInt64 || usage.SentBytes > math.MaxInt64 ||
			usage.UsedBytes > math.MaxInt64 || usage.ReceiveBPS > math.MaxInt64 || usage.SendBPS > math.MaxInt64 ||
			usage.UsedBytes != saturatedStoredTrafficAdd(usage.ReceivedBytes, usage.SentBytes) ||
			usage.LifetimeReceivedBytes > math.MaxInt64 || usage.LifetimeSentBytes > math.MaxInt64 ||
			(usage.CounterEpoch != "" && (!core.ValidTrafficCounterEpoch(usage.CounterEpoch) ||
				usage.LifetimeReceivedBytes < usage.ReceivedBytes || usage.LifetimeSentBytes < usage.SentBytes)) ||
			(usage.EnforcementAvailable && ((usage.CounterEpoch == "") != usage.CollectedAt.IsZero())) ||
			usage.CollectedAt.After(reportedAt.Add(5*time.Minute)) ||
			usage.PeriodStart.IsZero() || !usage.PeriodEnd.After(usage.PeriodStart) || usage.PeriodEnd.Sub(usage.PeriodStart) > 367*24*time.Hour ||
			utf8.RuneCountInString(usage.EnforcementError) > 500 || strings.ContainsRune(usage.EnforcementError, '\x00') {
			return fmt.Errorf("%w: invalid traffic usage record", ErrInvalid)
		}
		ids = append(ids, usage.PolicyID)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Grant -> policy is the allocation-update lock order. Only shared
	// reports need these extra locks; ordinary monitoring keeps its fast path.
	var shareIDs []string
	for _, usage := range usages {
		if usage.ShareID != "" {
			shareIDs = append(shareIDs, usage.ShareID)
		}
	}
	if len(shareIDs) > 0 {
		locked, err := tx.Query(ctx, `SELECT id FROM agent_shares WHERE agent_id=$1 AND id=ANY($2::text[]) ORDER BY id FOR NO KEY UPDATE`, agentID, shareIDs)
		if err != nil {
			return err
		}
		locked.Close()
		if err := locked.Err(); err != nil {
			return err
		}
	}
	// Read and lock all baselines together, in a consistent order even when
	// concurrent reports list ports differently. Ownership and monitoring
	// checks stay inside the transaction, including for skipped generations.
	rows, err := tx.Query(ctx, `
		SELECT id,reset_generation,received_bytes,sent_bytes,reported_received_bytes,reported_sent_bytes,
		       traffic_history_initialized,period_start,period_end,last_reported_at,last_collected_at,counter_epoch,
		       reported_lifetime_received_bytes,reported_lifetime_sent_bytes,cycle,cycle_anchor,created_at,accounting,COALESCE(share_id,''),port,engine,
               share_id IS NOT NULL AND EXISTS(SELECT 1 FROM agents a WHERE a.id=$2 AND a.features ? 'shared-process-traffic-v1'),
               (share_id IS NULL OR EXISTS(SELECT 1 FROM agent_shares sh JOIN agent_share_ports sp ON sp.share_id=sh.id
                 WHERE sh.id=port_traffic_policies.share_id AND sh.agent_id=port_traffic_policies.agent_id
                 AND sp.agent_id=sh.agent_id AND sp.port=port_traffic_policies.port)
                 OR EXISTS(SELECT 1 FROM agent_shares sh WHERE sh.id=port_traffic_policies.share_id
                   AND sh.agent_id=port_traffic_policies.agent_id AND sh.ports_unrestricted))
		FROM port_traffic_policies
		WHERE id=ANY($1::text[]) AND agent_id=$2 AND monitoring_enabled=true
		ORDER BY id FOR UPDATE`, ids, agentID)
	if err != nil {
		return err
	}
	type trafficBaseline struct {
		processRequired                bool
		engine                         core.Engine
		port                           int
		shareID                        string
		bindingValid                   bool
		accounting                     *core.TrafficAccounting
		generation                     uint64
		received, sent                 uint64
		reportedReceived, reportedSent uint64
		historyInitialized             bool
		periodStart, periodEnd         *time.Time
		lastReported                   *time.Time
		lastCollected                  *time.Time
		epoch                          string
		lifetimeReceived, lifetimeSent uint64
		cycle                          core.TrafficCycle
		anchor, created                time.Time
	}
	baselines := make(map[string]trafficBaseline, len(ids))
	for rows.Next() {
		var id string
		var current trafficBaseline
		if err := rows.Scan(&id,
			&current.generation, &current.received, &current.sent, &current.reportedReceived, &current.reportedSent,
			&current.historyInitialized,
			&current.periodStart, &current.periodEnd, &current.lastReported,
			&current.lastCollected, &current.epoch, &current.lifetimeReceived, &current.lifetimeSent, &current.cycle, &current.anchor, &current.created,
			&current.accounting, &current.shareID, &current.port, &current.engine, &current.processRequired, &current.bindingValid,
		); err != nil {
			rows.Close()
			return err
		}
		baselines[id] = current
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// A restored checkpoint can return to an older epoch with a NEW sample
	// timestamp. Consult the durable per-epoch baseline, not only the latest
	// policy epoch. The policy locks above serialize all writes to these rows.
	type epochKey struct {
		id         string
		generation uint64
		epoch      string
	}
	type epochBaseline struct {
		collectedAt    time.Time
		received, sent uint64
		accounting     *core.TrafficAccounting
	}
	history := make(map[epochKey]epochBaseline)
	var historyIDs, historyEpochs []string
	var historyGenerations []int64
	for _, usage := range usages {
		if _, ok := baselines[usage.PolicyID]; ok && usage.CounterEpoch != "" {
			historyIDs = append(historyIDs, usage.PolicyID)
			historyEpochs = append(historyEpochs, usage.CounterEpoch)
			historyGenerations = append(historyGenerations, int64(usage.ResetGeneration))
		}
	}
	if len(historyIDs) > 0 {
		rows, err := tx.Query(ctx, `SELECT e.policy_id,e.reset_generation,e.counter_epoch,e.lifetime_received_bytes,e.lifetime_sent_bytes,e.accounting,e.last_collected_at
			FROM port_traffic_accounting_epochs e JOIN unnest($1::text[],$2::bigint[],$3::text[]) AS wanted(id,generation,epoch)
			ON e.policy_id=wanted.id AND e.reset_generation=wanted.generation AND e.counter_epoch=wanted.epoch`, historyIDs, historyGenerations, historyEpochs)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key epochKey
			var value epochBaseline
			if err := rows.Scan(&key.id, &key.generation, &key.epoch, &value.received, &value.sent, &value.accounting, &value.collectedAt); err != nil {
				rows.Close()
				return err
			}
			history[key] = value
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	type processKey struct {
		share  string
		engine core.Engine
		epoch  string
	}
	processSamples := make(map[processKey]core.SharedProcessUsage)
	writes := make([]trafficUsageWrite, 0, len(baselines))
	reportedAt = reportedAt.UTC()
	for _, usage := range usages {
		current, exists := baselines[usage.PolicyID]
		if !exists {
			continue
		}
		if usage.ShareID != current.shareID || !current.bindingValid {
			return fmt.Errorf("%w: traffic allocation does not match reserved policy", ErrInvalid)
		}
		if current.shareID != "" && (usage.EnforcementAvailable && (usage.CounterEpoch == "" || usage.CollectedAt.IsZero()) ||
			usage.Accounting != nil && usage.Accounting.Source != "listener" && (usage.Accounting.Source != "nft-dual" || usage.Accounting.Mark != uint32(0x51430000)|uint32(current.port))) {
			return fmt.Errorf("%w: shared traffic requires timestamped counters with an allocated per-port outbound mark", ErrInvalid)
		}
		if current.shareID != "" && usage.Accounting != nil && usage.Accounting.Source == "nft-dual" &&
			(saturatedStoredTrafficAdd(usage.Accounting.ClientReceived, usage.Accounting.TargetReceived) != usage.LifetimeReceivedBytes ||
				saturatedStoredTrafficAdd(usage.Accounting.ClientSent, usage.Accounting.TargetSent) != usage.LifetimeSentBytes) {
			return fmt.Errorf("%w: shared traffic legs do not match lifetime counters", ErrInvalid)
		}
		if current.generation != usage.ResetGeneration {
			continue
		}
		if usage.CounterEpoch != "" && usage.CounterEpoch == current.epoch && !sameAccountingScope(current.accounting, usage.Accounting) {
			return fmt.Errorf("%w: accounting scope changed without a new counter epoch", ErrInvalid)
		}
		if current.lastReported != nil && !reportedAt.After(current.lastReported.UTC()) {
			continue
		}
		// A failed sample updates health only. It must not advance the byte
		// baseline, create history, or turn an old sample into a live rate.
		if !usage.EnforcementAvailable && (current.shareID != "" || usage.CounterEpoch != "" || !usage.CollectedAt.IsZero()) {
			// A restored Agent can report health before its first fresh sample.
			// Keep its checkpoint bytes out of all ledgers while allowing the
			// heartbeat handshake to deliver authenticated policies and recover.
			if !usage.CollectedAt.IsZero() && current.lastCollected != nil && usage.CollectedAt.Before(*current.lastCollected) {
				continue
			}
			writes = append(writes, trafficUsageWrite{ID: usage.PolicyID, Generation: usage.ResetGeneration,
				Blocked: usage.Blocked, EnforcementError: strings.TrimSpace(usage.EnforcementError)})
			continue
		}
		sampledAt := reportedAt
		previousSample := current.lastReported
		if !usage.CollectedAt.IsZero() {
			sampledAt = usage.CollectedAt.UTC()
			previousSample = current.lastCollected
			if previousSample != nil && !sampledAt.After(*previousSample) {
				continue
			}
			start, end, err := core.TrafficPeriodAt(current.anchor, current.cycle, sampledAt)
			if err != nil || !start.Equal(usage.PeriodStart) || !end.Equal(usage.PeriodEnd) {
				return fmt.Errorf("%w: traffic sample does not match policy calendar", ErrInvalid)
			}
		} else if current.lastCollected != nil {
			// Do not let a timestamp-less replay roll back a modern baseline.
			continue
		}
		if current.periodStart != nil && usage.PeriodStart.Before(*current.periodStart) {
			continue
		}
		// A full heartbeat and a metrics push can contain the same snapshot at
		// the same ticker boundary. Treat sub-second arrivals as duplicates so
		// they neither zero a valid live rate nor manufacture a short-interval
		// spike. The unchanged raw baseline means any real increment remains in
		// the next accepted sample.
		if usage.CollectedAt.IsZero() && current.lastReported != nil && reportedAt.Sub(current.lastReported.UTC()) < 500*time.Millisecond {
			continue
		}
		receivedDelta, sentDelta := uint64(0), uint64(0)
		newReceived, newSent := usage.ReceivedBytes, usage.SentBytes
		receiveBPS, sendBPS := uint64(0), uint64(0)
		samePeriod := current.periodStart != nil && current.periodEnd != nil &&
			current.periodStart.UTC().Equal(usage.PeriodStart.UTC()) && current.periodEnd.UTC().Equal(usage.PeriodEnd.UTC())
		periodUnknownAfterUpgrade := !current.historyInitialized && current.periodStart == nil && current.periodEnd == nil
		if samePeriod || periodUnknownAfterUpgrade {
			receivedDelta = trafficCounterDelta(usage.ReceivedBytes, current.reportedReceived)
			sentDelta = trafficCounterDelta(usage.SentBytes, current.reportedSent)
			newReceived = saturatedStoredTrafficAdd(current.received, receivedDelta)
			newSent = saturatedStoredTrafficAdd(current.sent, sentDelta)
		} else {
			// A new calendar period starts at zero on the Agent. Its first
			// report is both the new total and the first daily increment.
			receivedDelta, sentDelta = usage.ReceivedBytes, usage.SentBytes
		}
		dayBaseline := previousSample
		if usage.CounterEpoch != "" && dayBaseline == nil && !current.created.After(sampledAt) {
			dayBaseline = &current.created
		}
		rateBaseline := previousSample
		if usage.CounterEpoch != "" {
			if current.epoch == usage.CounterEpoch {
				// Within one epoch lifetime totals cannot decrease. An older
				// state/snapshot is not evidence of a fresh counter restart.
				if usage.LifetimeReceivedBytes < current.lifetimeReceived || usage.LifetimeSentBytes < current.lifetimeSent {
					continue
				}
				receivedDelta = usage.LifetimeReceivedBytes - current.lifetimeReceived
				sentDelta = usage.LifetimeSentBytes - current.lifetimeSent
			} else if prior, known := history[epochKey{usage.PolicyID, usage.ResetGeneration, usage.CounterEpoch}]; known {
				dayBaseline, rateBaseline = &prior.collectedAt, &prior.collectedAt
				accounting := usage.Accounting
				if accounting == nil {
					accounting = &core.TrafficAccounting{Source: "listener"}
				}
				if !sameAccountingScope(prior.accounting, accounting) {
					return fmt.Errorf("%w: historical accounting scope changed", ErrInvalid)
				}
				if usage.LifetimeReceivedBytes < prior.received || usage.LifetimeSentBytes < prior.sent {
					continue
				}
				receivedDelta, sentDelta = usage.LifetimeReceivedBytes-prior.received, usage.LifetimeSentBytes-prior.sent
			} else if current.epoch != "" || (current.periodStart == nil && !periodUnknownAfterUpgrade) {
				rateBaseline = nil
				dayBaseline = &current.created
				if current.created.After(sampledAt) {
					dayBaseline = nil
				}
				receivedDelta, sentDelta = usage.LifetimeReceivedBytes, usage.LifetimeSentBytes
			}
			if samePeriod || periodUnknownAfterUpgrade {
				// A returning epoch may still contain bytes from an earlier period.
				newReceived = saturatedStoredTrafficAdd(current.received, min(receivedDelta, usage.ReceivedBytes))
				newSent = saturatedStoredTrafficAdd(current.sent, min(sentDelta, usage.SentBytes))
			}
		}
		if rateBaseline != nil {
			receiveBPS = trafficAverageRate(receivedDelta, *rateBaseline, sampledAt)
			sendBPS = trafficAverageRate(sentDelta, *rateBaseline, sampledAt)
		}
		if sample := usage.ProcessUsage; sample != nil {
			key := processKey{current.shareID, current.engine, sample.Epoch}
			if sample.Bytes >= processSamples[key].Bytes {
				processSamples[key] = *sample
			}
		}
		usedDelta := saturatedStoredTrafficAdd(receivedDelta, sentDelta)
		// A pre-upgrade sample may still settle its old epoch. It cannot
		// authorize recovery until this Agent has armed its process rules.
		if current.processRequired && usage.ProcessUsage == nil {
			usage.EnforcementAvailable = false
			usage.EnforcementError = "等待共享实例进程计量就绪"
		}
		newUsed := saturatedStoredTrafficAdd(newReceived, newSent)
		writes = append(writes, trafficUsageWrite{
			ID: usage.PolicyID, Generation: usage.ResetGeneration,
			Received: newReceived, Sent: newSent, Used: newUsed, ReceiveBPS: receiveBPS, SendBPS: sendBPS,
			PeriodStart: usage.PeriodStart, PeriodEnd: usage.PeriodEnd,
			Blocked: usage.Blocked, EnforcementAvailable: usage.EnforcementAvailable,
			EnforcementError: strings.TrimSpace(usage.EnforcementError),
			ReportedReceived: usage.ReceivedBytes, ReportedSent: usage.SentBytes,
			Days: trafficSampleDays(usage, dayBaseline, sampledAt, receivedDelta, sentDelta), UsedDelta: usedDelta,
			PeakSampled:  rateBaseline != nil && validTrafficRateInterval(*rateBaseline, sampledAt) && core.UTCDate(*rateBaseline).Equal(core.UTCDate(sampledAt)),
			RecordSample: true, SampledAt: sampledAt, CollectedAt: nilIfZeroTime(usage.CollectedAt), CounterEpoch: usage.CounterEpoch,
			LifetimeReceived: usage.LifetimeReceivedBytes, LifetimeSent: usage.LifetimeSentBytes, Accounting: usage.Accounting,
		})
	}
	// One set-based statement also reduces database executor work on local
	// connections. The daily increments come only from successfully updated
	// rows; all baselines remain locked and any error rolls back the report.
	if len(writes) > 0 {
		payload, err := json.Marshal(writes)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, applyTrafficUsageSQL, agentID, reportedAt, payload); err != nil {
			return err
		}
	}
	// The allocation lock serializes all its engines and listener reports.
	for key, sample := range processSamples {
		_, err := tx.Exec(ctx, `WITH old AS MATERIALIZED (
   SELECT used_bytes FROM shared_process_usage WHERE share_id=$1 AND engine=$2 AND epoch=$3
  ), saved AS (
   INSERT INTO shared_process_usage(share_id,engine,epoch,used_bytes,sampled_at) VALUES($1,$2,$3,$4,$5)
   ON CONFLICT(share_id,engine,epoch) DO UPDATE SET used_bytes=EXCLUDED.used_bytes,sampled_at=EXCLUDED.sampled_at
   WHERE shared_process_usage.used_bytes < EXCLUDED.used_bytes RETURNING used_bytes
  ) UPDATE agent_shares SET used_bytes=LEAST(9223372036854775807::numeric,
    agent_shares.used_bytes::numeric+saved.used_bytes-COALESCE((SELECT used_bytes FROM old),0)),updated_at=now()
    FROM saved WHERE id=$1`, key.share, key.engine, key.epoch, sample.Bytes, reportedAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type trafficUsageWrite struct {
	Accounting           *core.TrafficAccounting `json:"accounting"`
	RecordSample         bool                    `json:"record_sample"`
	SampledAt            time.Time               `json:"sampled_at"`
	CollectedAt          *time.Time              `json:"collected_at"`
	CounterEpoch         string                  `json:"counter_epoch"`
	LifetimeReceived     uint64                  `json:"lifetime_received"`
	LifetimeSent         uint64                  `json:"lifetime_sent"`
	ID                   string                  `json:"id"`
	Generation           uint64                  `json:"generation"`
	Received             uint64                  `json:"received"`
	Sent                 uint64                  `json:"sent"`
	Used                 uint64                  `json:"used"`
	ReceiveBPS           uint64                  `json:"receive_bps"`
	SendBPS              uint64                  `json:"send_bps"`
	PeriodStart          time.Time               `json:"period_start"`
	PeriodEnd            time.Time               `json:"period_end"`
	Blocked              bool                    `json:"blocked"`
	EnforcementAvailable bool                    `json:"enforcement_available"`
	EnforcementError     string                  `json:"enforcement_error"`
	ReportedReceived     uint64                  `json:"reported_received"`
	ReportedSent         uint64                  `json:"reported_sent"`
	Days                 []trafficDayAllocation  `json:"days"`
	PeakSampled          bool                    `json:"peak_sampled"`
	UsedDelta            uint64                  `json:"used_delta"`
}

const applyTrafficUsageSQL = `
	WITH input AS (
		SELECT * FROM jsonb_to_recordset($3::jsonb) AS value(
			id text,generation bigint,received bigint,sent bigint,used bigint,receive_bps bigint,send_bps bigint,
			period_start timestamptz,period_end timestamptz,blocked boolean,enforcement_available boolean,
			enforcement_error text,reported_received bigint,reported_sent bigint,
			days jsonb,peak_sampled boolean,used_delta bigint,record_sample boolean,sampled_at timestamptz,
			collected_at timestamptz,counter_epoch text,lifetime_received bigint,lifetime_sent bigint,accounting jsonb)
	), updated AS (
		UPDATE port_traffic_policies policy SET
			accounting=CASE WHEN input.record_sample THEN NULLIF(input.accounting,'null'::jsonb) ELSE policy.accounting END,
            share_used_bytes=CASE WHEN input.record_sample AND policy.share_id IS NOT NULL
                THEN LEAST(9223372036854775807::numeric,policy.share_used_bytes::numeric+input.used_delta)::bigint
                ELSE policy.share_used_bytes END,
			received_bytes=CASE WHEN input.record_sample THEN input.received ELSE policy.received_bytes END,
			sent_bytes=CASE WHEN input.record_sample THEN input.sent ELSE policy.sent_bytes END,
			used_bytes=CASE WHEN input.record_sample THEN input.used ELSE policy.used_bytes END,
			receive_bps=input.receive_bps,send_bps=input.send_bps,
			period_start=CASE WHEN input.record_sample THEN input.period_start ELSE policy.period_start END,
			period_end=CASE WHEN input.record_sample THEN input.period_end ELSE policy.period_end END,
			blocked=input.blocked,enforcement_available=input.enforcement_available,enforcement_error=input.enforcement_error,
			last_reported_at=$2,traffic_history_initialized=policy.traffic_history_initialized OR input.record_sample,
			last_collected_at=CASE WHEN input.record_sample THEN input.collected_at ELSE policy.last_collected_at END,
			counter_epoch=CASE WHEN input.record_sample THEN input.counter_epoch ELSE policy.counter_epoch END,
			reported_lifetime_received_bytes=CASE WHEN input.record_sample THEN input.lifetime_received ELSE policy.reported_lifetime_received_bytes END,
			reported_lifetime_sent_bytes=CASE WHEN input.record_sample THEN input.lifetime_sent ELSE policy.reported_lifetime_sent_bytes END,
			reported_received_bytes=CASE WHEN input.record_sample THEN input.reported_received ELSE policy.reported_received_bytes END,
			reported_sent_bytes=CASE WHEN input.record_sample THEN input.reported_sent ELSE policy.reported_sent_bytes END
		FROM input WHERE policy.id=input.id AND policy.agent_id=$1 AND policy.reset_generation=input.generation
		RETURNING policy.id,policy.agent_id,policy.name,policy.engine,policy.port,policy.protocol,policy.share_id
    ), share_totals AS (
        SELECT updated.share_id,SUM(input.used_delta) AS delta
        FROM updated JOIN input ON input.id=updated.id
        WHERE updated.share_id IS NOT NULL AND input.record_sample AND input.used_delta>0
        GROUP BY updated.share_id
    ), shares_written AS (
        UPDATE agent_shares sh SET used_bytes=LEAST(9223372036854775807::numeric,sh.used_bytes::numeric+t.delta)::bigint,
            updated_at=now() FROM share_totals t WHERE sh.id=t.share_id
	), allocations AS (
		SELECT updated.*,input.generation,input.accounting,input.receive_bps,input.send_bps,input.peak_sampled,
			part.*,part.day=(input.sampled_at AT TIME ZONE 'UTC')::date AS sampled_today
		FROM updated JOIN input ON input.id=updated.id
		CROSS JOIN LATERAL jsonb_to_recordset(input.days) AS part(day date,since date,received bigint,sent bigint,undated_received bigint,undated_sent bigint)
		WHERE input.record_sample
	), scoped_daily AS (
		INSERT INTO port_traffic_daily_accounting (policy_id,agent_id,reset_generation,usage_date,source,received_bytes,sent_bytes,undated_since,undated_received_bytes,undated_sent_bytes)
		SELECT id,agent_id,generation,day,COALESCE(accounting->>'source','listener'),received,sent,since,undated_received,undated_sent
		FROM allocations
		ON CONFLICT (policy_id,reset_generation,usage_date,source) DO UPDATE SET
			undated_since=LEAST(port_traffic_daily_accounting.undated_since,EXCLUDED.undated_since),
		undated_received_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_accounting.undated_received_bytes::numeric+EXCLUDED.undated_received_bytes)::bigint,
		undated_sent_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_accounting.undated_sent_bytes::numeric+EXCLUDED.undated_sent_bytes)::bigint,
		received_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_accounting.received_bytes::numeric+EXCLUDED.received_bytes)::bigint,
			sent_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_accounting.sent_bytes::numeric+EXCLUDED.sent_bytes)::bigint
	), epochs AS (
		INSERT INTO port_traffic_accounting_epochs (policy_id,agent_id,reset_generation,counter_epoch,accounting,
			lifetime_received_bytes,lifetime_sent_bytes,first_collected_at,last_collected_at)
		SELECT updated.id,updated.agent_id,input.generation,input.counter_epoch,COALESCE(NULLIF(input.accounting,'null'::jsonb),'{"source":"listener"}'::jsonb),input.lifetime_received,input.lifetime_sent,
			input.collected_at,input.collected_at
		FROM updated JOIN input ON input.id=updated.id
		WHERE input.record_sample AND input.collected_at IS NOT NULL AND input.counter_epoch<>''
		ON CONFLICT (policy_id,reset_generation,counter_epoch) DO UPDATE SET
			accounting=EXCLUDED.accounting,lifetime_received_bytes=EXCLUDED.lifetime_received_bytes,
			lifetime_sent_bytes=EXCLUDED.lifetime_sent_bytes,last_collected_at=EXCLUDED.last_collected_at
	)
	INSERT INTO port_traffic_daily_usage (
		policy_id,reset_generation,usage_date,agent_id,name,engine,port,protocol,
		received_bytes,sent_bytes,used_bytes,undated_since,undated_received_bytes,undated_sent_bytes,month_undated_received_bytes,month_undated_sent_bytes,peak_sampled,
		peak_receive_bps,peak_send_bps,sample_count,first_reported_at,last_reported_at)
	SELECT id,generation,day,agent_id,name,engine,port,protocol,received,sent,LEAST(9223372036854775807::numeric,received::numeric+sent)::bigint,
		since,undated_received,undated_sent,
		CASE WHEN since>=date_trunc('month', day::timestamp) THEN undated_received ELSE 0 END,
		CASE WHEN since>=date_trunc('month', day::timestamp) THEN undated_sent ELSE 0 END, peak_sampled AND sampled_today,
		CASE WHEN peak_sampled AND sampled_today THEN receive_bps ELSE 0 END,
		CASE WHEN peak_sampled AND sampled_today THEN send_bps ELSE 0 END,sampled_today::int,$2,$2
	FROM allocations
	ON CONFLICT (policy_id,reset_generation,usage_date) DO UPDATE SET
		agent_id=EXCLUDED.agent_id,name=EXCLUDED.name,engine=EXCLUDED.engine,port=EXCLUDED.port,protocol=EXCLUDED.protocol,
		month_undated_received_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_usage.month_undated_received_bytes::numeric+EXCLUDED.month_undated_received_bytes)::bigint,
		month_undated_sent_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_usage.month_undated_sent_bytes::numeric+EXCLUDED.month_undated_sent_bytes)::bigint,
		undated_since=LEAST(port_traffic_daily_usage.undated_since,EXCLUDED.undated_since),
		undated_received_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_usage.undated_received_bytes::numeric+EXCLUDED.undated_received_bytes)::bigint,
		undated_sent_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_usage.undated_sent_bytes::numeric+EXCLUDED.undated_sent_bytes)::bigint,
		received_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_usage.received_bytes::numeric+EXCLUDED.received_bytes)::bigint,
		sent_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_usage.sent_bytes::numeric+EXCLUDED.sent_bytes)::bigint,
		used_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_usage.used_bytes::numeric+EXCLUDED.used_bytes)::bigint,
		peak_receive_bps=GREATEST(port_traffic_daily_usage.peak_receive_bps,EXCLUDED.peak_receive_bps),
		peak_send_bps=GREATEST(port_traffic_daily_usage.peak_send_bps,EXCLUDED.peak_send_bps),
		peak_sampled=port_traffic_daily_usage.peak_sampled OR EXCLUDED.peak_sampled,
		sample_count=port_traffic_daily_usage.sample_count+EXCLUDED.sample_count,last_reported_at=EXCLUDED.last_reported_at`

func saturatedStoredTrafficAdd(left, right uint64) uint64 {
	if left >= math.MaxInt64 || right > uint64(math.MaxInt64)-left {
		return math.MaxInt64
	}
	return left + right
}

func sameAccountingScope(a, b *core.TrafficAccounting) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Source == b.Source && a.Inbound == b.Inbound && a.Mark == b.Mark && slices.Equal(a.Outbounds, b.Outbounds)
}

func nilIfZeroTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func validTrafficRateInterval(previous, current time.Time) bool {
	elapsed := current.Sub(previous)
	return elapsed > 0 && elapsed <= 2*time.Minute
}

func trafficAverageRate(delta uint64, previous, current time.Time) uint64 {
	elapsed := current.Sub(previous)
	if delta == 0 || !validTrafficRateInterval(previous, current) {
		return 0
	}
	rate := math.Round(float64(delta) / elapsed.Seconds())
	if rate <= 0 {
		return 0
	}
	if rate >= math.MaxInt64 {
		return math.MaxInt64
	}
	return uint64(rate)
}

func trafficCounterDelta(current, previous uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	// A counter may restart inside the same policy generation after local state
	// recovery. Count only the new post-restart bytes instead of losing them.
	return current
}
