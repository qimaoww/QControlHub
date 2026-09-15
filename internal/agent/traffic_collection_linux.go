package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (manager *TrafficManager) collectLocked(ctx context.Context, forceRules bool) error {
	if manager.awaitingPolicies {
		return manager.setUnavailableLocked(errors.New("traffic checkpoint unavailable; waiting for authenticated policies"))
	}
	manager.rulesDirty = manager.rulesDirty || forceRules
	if len(manager.records) == 0 && !manager.rulesDirty {
		manager.snapshot = nil
		return manager.saveLocked()
	}
	if manager.backend == nil {
		return manager.setUnavailableLocked(errors.New("nftables backend is unavailable"))
	}
	counters, tableExists, err := manager.backend.Counters(ctx)
	if err != nil {
		return manager.setUnavailableLocked(err)
	}
	now := manager.now().UTC()
	if tableExists && !trafficRuleSetComplete(counters, manager.records) {
		manager.rulesDirty = true
	}
	desired := make(map[string]*trafficRecord, len(manager.records))
	nativeSnapshots := map[core.Engine]nativeAccountingSnapshot{}
	nativeErrors := map[core.Engine]error{}
	if manager.nativeSource != nil {
		for _, record := range manager.records {
			if record.Policy.SharedQuota != nil {
				continue // Shared allocations use ingress network bytes only.
			}
			engine := record.Policy.Engine
			if _, checked := nativeErrors[engine]; checked {
				continue
			}
			snapshot, err := manager.nativeSource(ctx, engine)
			nativeSnapshots[engine], nativeErrors[engine] = snapshot, err
		}
	}
	for _, id := range sortedTrafficRecordIDs(manager.records) {
		record := manager.records[id]
		shared := record.Policy.SharedQuota != nil
		nativeAllowed := !shared && nativeAccountingScopeAllowed(record.Policy, nativeSnapshots[record.Policy.Engine])
		// A transient source failure must freeze an established native baseline,
		// not change its scope or substitute listener bytes.
		if !shared && nativeErrors[record.Policy.Engine] != nil && record.Accounting != nil && record.Accounting.Source != "listener" {
			nativeAllowed = true
		}
		// Core counters and per-port marks do not distinguish the originating
		// listener transport. A TCP-only policy must not bill another UDP flow
		// (nor mistake a QUIC outbound carrying TCP for a UDP listener flow).
		if (manager.nativeSource != nil || shared) && !nativeAllowed && record.Accounting != nil && record.Accounting.Source != "listener" {
			epoch, err := randomSuffix(16)
			if err != nil {
				return manager.setUnavailableLocked(err)
			}
			record.QuotaBaselineBytes = saturatedTrafficAdd(record.QuotaBaselineBytes, trafficUsed(record))
			record.CounterEpoch, record.Accounting = epoch, nil
			record.KernelCounters = make(map[string]uint64)
			record.ReceivedBytes, record.SentBytes, record.LifetimeReceivedBytes, record.LifetimeSentBytes = 0, 0, 0, 0
			manager.dirty, manager.rulesDirty = true, true
		}
		if record.CounterEpoch == "" {
			epoch, err := randomSuffix(16)
			if err != nil {
				return manager.setUnavailableLocked(err)
			}
			record.CounterEpoch = epoch
			record.LifetimeReceivedBytes, record.LifetimeSentBytes = record.ReceivedBytes, record.SentBytes
			manager.dirty, manager.rulesDirty = true, true
		}
		periodStart, periodEnd, periodErr := core.TrafficPeriodAt(record.Policy.CycleAnchor, record.Policy.Cycle, now)
		if periodErr != nil {
			return manager.setUnavailableLocked(periodErr)
		}
		// Sampling is transactional: a failed source must not consume the
		// listener delta before the marked target counters can be collected.
		previousRecord := *record
		previousRecord.KernelCounters = maps.Clone(record.KernelCounters)
		previousRecord.Accounting = cloneTrafficAccounting(record.Accounting)
		receivedDelta, sentDelta := collectTrafficCounterDeltas(counters, record)
		if manager.nativeSource != nil && nativeAllowed {
			err := nativeErrors[record.Policy.Engine]
			if err == nil {
				previousEpoch := record.CounterEpoch
				if nativeSnapshots[record.Policy.Engine].Plan.Source == "nft-dual" {
					err = prepareMarkedAccounting(record, nativeSnapshots[record.Policy.Engine])
					if err == nil {
						receivedDelta, sentDelta = collectMarkedAccounting(counters, record, receivedDelta, sentDelta, previousEpoch != record.CounterEpoch)
					}
				} else {
					receivedDelta, sentDelta, err = collectNativeAccounting(record, nativeSnapshots[record.Policy.Engine])
				}
				if previousEpoch != record.CounterEpoch {
					manager.rulesDirty = true
				}
				manager.dirty = true
			}
			record.AccountingError = ""
			if err != nil {
				record.AccountingError = err.Error()
				if len(record.AccountingError) > 400 {
					record.AccountingError = record.AccountingError[:400]
				}
				if record.Accounting != nil && record.Accounting.Source != "listener" {
					// Preserve native baselines and do not substitute network bytes.
					message := record.AccountingError
					*record = previousRecord
					record.AccountingError = message
					record.ReceiveBPS, record.SendBPS = 0, 0
					next := *record
					// Explicit quota relaxation and calendar expiry do not depend
					// on a working stats API. Keep accounting timestamps intact
					// so recovery still commits every unreported byte.
					if !record.Policy.AutoBlock || !record.PeriodEnd.After(now) ||
						saturatedTrafficAdd(trafficUsed(record), record.QuotaBaselineBytes) < record.Policy.LimitBytes {
						next.Blocked = false
					}
					if next.Blocked != record.Blocked {
						manager.rulesDirty = true
					}
					desired[id] = &next
					continue
				}
			}
		}
		if shared {
			record.AccountingError = ""
		} else if manager.nativeSource != nil && !nativeAllowed {
			record.AccountingError = "single-protocol policy uses listener-only accounting; exclusive listener transport could not be verified"
			if sourceErr := nativeErrors[record.Policy.Engine]; sourceErr != nil {
				record.AccountingError = sourceErr.Error()
				if len(record.AccountingError) > 400 {
					record.AccountingError = record.AccountingError[:400]
				}
			}
		}
		if !record.PeriodStart.Equal(periodStart) || !record.PeriodEnd.Equal(periodEnd) {
			record.ReceivedBytes, record.SentBytes = 0, 0
			record.QuotaBaselineBytes = 0
			// Keep the crossing sample in the new period instead of silently
			// discarding it. An initial baseline is not historical usage.
			if record.PeriodStart.IsZero() || (record.Blocked && !hasNamedTrafficCounters(counters, record)) {
				receivedDelta, sentDelta = 0, 0
			}
			record.ReceiveBPS, record.SendBPS = 0, 0
			record.PeriodStart, record.PeriodEnd = periodStart, periodEnd
			manager.dirty = true
		} else if record.Blocked && record.Accounting == nil && !hasNamedTrafficCounters(counters, record) {
			// Only legacy anonymous counters count rejected attempts. Named
			// counters stop at the actual drop rule; their final increment is
			// still valid even when it arrived during the rule transaction.
			receivedDelta, sentDelta = 0, 0
		}
		record.ReceivedBytes = saturatedTrafficAdd(record.ReceivedBytes, receivedDelta)
		record.SentBytes = saturatedTrafficAdd(record.SentBytes, sentDelta)
		record.LifetimeReceivedBytes = saturatedTrafficAdd(record.LifetimeReceivedBytes, receivedDelta)
		record.LifetimeSentBytes = saturatedTrafficAdd(record.LifetimeSentBytes, sentDelta)
		if shared {
			record.ShareUsedBytes = saturatedTrafficAdd(record.ShareUsedBytes, saturatedTrafficAdd(receivedDelta, sentDelta))
		}
		record.ReceiveBPS, record.SendBPS = 0, 0
		if !record.LastCollectedAt.IsZero() && now.After(record.LastCollectedAt) {
			seconds := now.Sub(record.LastCollectedAt).Seconds()
			record.ReceiveBPS = trafficRate(receivedDelta, seconds)
			record.SendBPS = trafficRate(sentDelta, seconds)
		}
		record.LastCollectedAt = now
		manager.dirty = manager.dirty || receivedDelta > 0 || sentDelta > 0
		blocked := record.Policy.AutoBlock && saturatedTrafficAdd(trafficUsed(record), record.QuotaBaselineBytes) >= record.Policy.LimitBytes
		next := *record
		next.Blocked = blocked
		desired[id] = &next
		if blocked != record.Blocked {
			manager.rulesDirty = true
		}
	}
	// One aggregate decision after sampling every port. Billing a full quota
	// independently to each port would multiply the user's allowance.
	for id, record := range desired {
		if quota := record.Policy.SharedQuota; quota != nil {
			record.Blocked = !quota.AllowsEngine(record.Policy.Engine) || (quota.LimitBytes > 0 && sharedTrafficUsed(desired, quota.ID) >= quota.LimitBytes)
			if record.Blocked != manager.records[id].Blocked {
				manager.rulesDirty = true
			}
		}
	}

	if manager.rulesDirty || (len(manager.records) > 0 && !tableExists) || (len(manager.records) == 0 && tableExists) {
		manager.rulesDirty = true
		script := renderTrafficRules(desired, tableExists, counters)
		if err := manager.backend.Replace(ctx, script); err != nil {
			// Counting can still succeed while enforcement is broken. Keep
			// those observed bytes durable even through repeated apply errors.
			if saveErr := manager.saveLocked(); saveErr != nil {
				err = errors.Join(err, fmt.Errorf("persist traffic counters: %w", saveErr))
			}
			return manager.setUnavailableLocked(err)
		}
		for id, record := range manager.records {
			record.Blocked = desired[id].Blocked
		}
		manager.rulesDirty = false
		manager.dirty = true
	}
	manager.refreshSnapshotLocked(true, "")
	if err := manager.saveLocked(); err != nil {
		return manager.setUnavailableLocked(fmt.Errorf("persist traffic counters: %w", err))
	}
	return nil
}
