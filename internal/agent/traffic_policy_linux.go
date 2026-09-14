package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (manager *TrafficManager) settledSnapshot(ctx context.Context) ([]core.PortTrafficUsage, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	err := manager.collectLocked(ctx, false)
	return append([]core.PortTrafficUsage(nil), manager.snapshot...), err == nil
}

func (manager *TrafficManager) refreshSnapshotLocked(available bool, message string) {
	result := make([]core.PortTrafficUsage, 0, len(manager.records))
	for _, id := range sortedTrafficRecordIDs(manager.records) {
		record := manager.records[id]
		accounting := cloneTrafficAccounting(record.Accounting)
		healthMessage := message
		if record.AccountingError != "" {
			if healthMessage != "" {
				healthMessage += "; "
			}
			healthMessage += "dual accounting unavailable: " + record.AccountingError
		}
		if len(healthMessage) > 500 {
			healthMessage = string([]rune(healthMessage)[:min(500, len([]rune(healthMessage)))])
		}
		result = append(result, core.PortTrafficUsage{
			PolicyID: id, ResetGeneration: record.Policy.ResetGeneration,
			ReceivedBytes: record.ReceivedBytes, SentBytes: record.SentBytes,
			UsedBytes: trafficUsed(record), ReceiveBPS: record.ReceiveBPS, SendBPS: record.SendBPS,
			PeriodStart: record.PeriodStart, PeriodEnd: record.PeriodEnd, Blocked: record.Blocked,
			EnforcementAvailable: available && !(record.AccountingError != "" && accounting != nil && accounting.Source != "listener"), EnforcementError: healthMessage,
			CollectedAt: record.LastCollectedAt, CounterEpoch: record.CounterEpoch,
			LifetimeReceivedBytes: record.LifetimeReceivedBytes, LifetimeSentBytes: record.LifetimeSentBytes,
			Accounting: accounting,
			ShareID:    sharedQuotaID(record.Policy.SharedQuota), ShareUsedBytes: record.ShareUsedBytes,
		})
	}
	manager.snapshot = result
}

func cloneTrafficAccounting(accounting *core.TrafficAccounting) *core.TrafficAccounting {
	if accounting == nil {
		return nil
	}
	clone := *accounting
	clone.Counters = maps.Clone(accounting.Counters)
	clone.Outbounds = slices.Clone(accounting.Outbounds)
	return &clone
}

func sameTrafficCounter(left, right core.PortTrafficPolicy) bool {
	return left.Engine == right.Engine && left.Port == right.Port && left.Protocol == right.Protocol && left.Cycle == right.Cycle &&
		core.UTCDate(left.CycleAnchor).Equal(core.UTCDate(right.CycleAnchor)) && left.ResetGeneration == right.ResetGeneration
}

func sharedQuotaID(quota *core.SharedTrafficQuota) string {
	if quota == nil {
		return ""
	}
	return quota.ID
}

func sameSharedQuota(left, right *core.SharedTrafficQuota) bool {
	return left == nil && right == nil || left != nil && right != nil &&
		left.ID == right.ID && left.LimitBytes == right.LimitBytes && left.UsedBytes == right.UsedBytes &&
		left.PortUsedBytes == right.PortUsedBytes && left.Revoked == right.Revoked && slices.Equal(left.Engines, right.Engines)
}

func sharedTrafficUsed(records map[string]*trafficRecord, shareID string) uint64 {
	var acknowledged, pending uint64
	for _, record := range records {
		quota := record.Policy.SharedQuota
		if quota == nil || quota.ID != shareID {
			continue
		}
		acknowledged = max(acknowledged, quota.UsedBytes)
		if record.ShareUsedBytes > quota.PortUsedBytes {
			pending = saturatedTrafficAdd(pending, record.ShareUsedBytes-quota.PortUsedBytes)
		}
	}
	return saturatedTrafficAdd(acknowledged, pending)
}

func (manager *TrafficManager) authorizeSharedDeployment(ctx context.Context, shareID string, endpoints []core.PortTrafficEndpoint) error {
	if manager == nil || !core.ValidAgentShareID(shareID) || len(endpoints) == 0 {
		return errors.New("shared traffic manager or allocation is unavailable")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := manager.collectLocked(ctx, false); err != nil {
		return fmt.Errorf("shared traffic enforcement is unavailable: %w", err)
	}
	for _, endpoint := range endpoints {
		var matched bool
		for _, record := range manager.records {
			quota := record.Policy.SharedQuota
			if quota != nil && quota.ID == shareID && record.Policy.Port == endpoint.Port && record.Policy.Engine == endpoint.Engine {
				if !quota.AllowsEngine(endpoint.Engine) || record.Blocked || (quota.LimitBytes > 0 && sharedTrafficUsed(manager.records, shareID) >= quota.LimitBytes) {
					return errors.New("shared Agent access is revoked or its cumulative allowance is exhausted")
				}
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("shared port %d has no enforced allocation", endpoint.Port)
		}
	}
	return nil
}

func sortedTrafficRecordIDs(records map[string]*trafficRecord) []string {
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func policyKernelCounters(counters map[string]uint64, policy core.PortTrafficPolicy) (uint64, uint64) {
	var received, sent uint64
	for _, protocol := range trafficProtocols(policy.Protocol) {
		received = saturatedTrafficAdd(received, counters[trafficRuleComment(policy.ID, "in", protocol)])
		sent = saturatedTrafficAdd(sent, counters[trafficRuleComment(policy.ID, "out", protocol)])
	}
	return received, sent
}
