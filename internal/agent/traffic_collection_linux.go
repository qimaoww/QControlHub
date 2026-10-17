package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (manager *TrafficManager) collectLocked(ctx context.Context, forceRules bool) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		// A WSS reconnect or Agent shutdown cancels its sampling request, not
		// the installed nftables rules. Verify them with a bounded fresh sample
		// before deciding that accounting is unavailable and stopping instances.
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Second)
		defer cancel()
		return manager.collectLocked(recovery, forceRules)
	}
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
		if errors.Is(ctx.Err(), context.Canceled) {
			return manager.collectLocked(ctx, forceRules)
		}
		return manager.setUnavailableLocked(err)
	}
	now := manager.now().UTC()
	if tableExists && !trafficRuleSetComplete(counters, manager.records) {
		manager.rulesDirty = true
	}
	desired := make(map[string]*trafficRecord, len(manager.records))
	type sourceKey struct {
		engine  core.Engine
		shareID string
	}
	snapshots := map[sourceKey]nativeAccountingSnapshot{}
	sourceErrors := map[sourceKey]error{}
	for _, record := range manager.records {
		key := sourceKey{engine: record.Policy.Engine, shareID: sharedQuotaID(record.Policy.SharedQuota)}
		if _, checked := sourceErrors[key]; checked {
			continue
		}
		var snapshot nativeAccountingSnapshot
		var err error
		if key.shareID != "" {
			if manager.sharedSource == nil {
				continue
			}
			snapshot, err = manager.sharedSource(ctx, key.engine, key.shareID)
		} else {
			if manager.nativeSource == nil {
				continue
			}
			snapshot, err = manager.nativeSource(ctx, key.engine)
		}
		snapshots[key], sourceErrors[key] = snapshot, err
	}
	for _, id := range sortedTrafficRecordIDs(manager.records) {
		record := manager.records[id]
		shared := record.Policy.SharedQuota != nil
		key := sourceKey{record.Policy.Engine, sharedQuotaID(record.Policy.SharedQuota)}
		snapshot, sourceErr := snapshots[key], sourceErrors[key]
		if shared && errors.Is(sourceErr, errSharedAccountingTransition) {
			// Existing nft rules keep protecting the allocation while its
			// configuration and deployment digest are committed atomically.
			// Retain every baseline; the next sample includes the full tail.
			next := *record
			desired[id] = &next
			continue
		}
		hasSource := manager.nativeSource != nil && !shared || shared && manager.sharedSource != nil
		nativeAllowed := hasSource && nativeAccountingScopeAllowed(record.Policy, snapshot)
		// A transient source failure must freeze an established native baseline,
		// not change its scope or substitute listener bytes.
		if hasSource && sourceErr != nil && record.Accounting != nil && record.Accounting.Source != "listener" {
			nativeAllowed = true
		}
		// Core counters and per-port marks do not distinguish the originating
		// listener transport. A TCP-only policy must not bill another UDP flow
		// (nor mistake a QUIC outbound carrying TCP for a UDP listener flow).
		if hasSource && !nativeAllowed && record.Accounting != nil && record.Accounting.Source != "listener" {
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
		if hasSource && (nativeAllowed || shared) {
			err := sourceErr
			if err == nil {
				previousEpoch := record.CounterEpoch
				if snapshot.Plan.Source == "nft-dual" {
					err = prepareMarkedAccounting(record, snapshot)
					if err == nil {
						if shared && (record.Accounting == nil || record.Accounting.Source != "nft-dual") {
							// A reserved future listener is only firewall protection.
							// Its bytes cannot be attributed to the deployed instance.
							receivedDelta, sentDelta = 0, 0
						}
						if shared && previousEpoch != record.CounterEpoch &&
							(previousRecord.Accounting == nil || previousRecord.Accounting.Source == "listener") {
							// A restored checkpoint has no in-memory collection time.
							// Reconcile its last listener interval against the server's
							// acknowledged lifetime, including any replay overlap.
							quota := record.Policy.SharedQuota
							if quota.CounterEpoch != previousRecord.CounterEpoch {
								receivedDelta, sentDelta = 0, 0
							} else {
								if quota.LifetimeReceivedBytes > previousRecord.LifetimeReceivedBytes {
									overlap := quota.LifetimeReceivedBytes - previousRecord.LifetimeReceivedBytes
									receivedDelta -= min(receivedDelta, overlap)
								}
								if quota.LifetimeSentBytes > previousRecord.LifetimeSentBytes {
									overlap := quota.LifetimeSentBytes - previousRecord.LifetimeSentBytes
									sentDelta -= min(sentDelta, overlap)
								}
							}
							// Target counters start at zero; no historical exit is estimated.
							record.Accounting.ClientReceived = saturatedTrafficAdd(record.Accounting.ClientReceived, receivedDelta)
							record.Accounting.ClientSent = saturatedTrafficAdd(record.Accounting.ClientSent, sentDelta)
						} else if record.Accounting != nil {
							receivedDelta, sentDelta = collectMarkedAccounting(counters, record, receivedDelta, sentDelta, previousEpoch != record.CounterEpoch)
						}
					}
				} else {
					receivedDelta, sentDelta, err = collectNativeAccounting(record, snapshot)
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
				if shared || record.Accounting != nil && record.Accounting.Source != "listener" {
					// Preserve native baselines and do not substitute network bytes.
					message := record.AccountingError
					*record = previousRecord
					record.AccountingError = message
					if record.PeriodStart.IsZero() || record.PeriodEnd.IsZero() {
						record.PeriodStart, record.PeriodEnd = periodStart, periodEnd
					}
					record.ReceiveBPS, record.SendBPS = 0, 0
					// Invalid attribution stops only this allocation. Stopped or not-yet
					// deployed services remain deployable under listener quota protection.
					if shared && manager.haltShare != nil && !errors.Is(err, os.ErrNotExist) {
						manager.haltReason = "共享出口计量不可用，实例已保护性停止；请检查统计状态并重新部署"
						if stopErr := manager.haltShare(ctx, record.Policy.Engine, record.Policy.SharedQuota.ID); stopErr != nil {
							record.AccountingError += "; stop instance: " + stopErr.Error()
						}
					}
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
		if !shared && manager.nativeSource != nil && !nativeAllowed {
			record.AccountingError = "single-protocol policy uses listener-only accounting; exclusive listener transport could not be verified"
			if sourceErr != nil {
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
			} else if errors.Is(ctx.Err(), context.Canceled) {
				return manager.collectLocked(ctx, forceRules)
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
