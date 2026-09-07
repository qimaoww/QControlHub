package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	trafficTableName       = "qcontrolhub"
	trafficCollectionEvery = time.Second
	trafficStateMaxBytes   = 2 << 20
)

type trafficCounterBackend interface {
	Counters(context.Context) (map[string]uint64, bool, error)
	Replace(context.Context, string) error
}

type trafficRecord struct {
	Accounting            *core.TrafficAccounting `json:"accounting,omitempty"`
	AccountingError       string                  `json:"-"`
	Policy                core.PortTrafficPolicy  `json:"policy"`
	ReceivedBytes         uint64                  `json:"received_bytes"`
	SentBytes             uint64                  `json:"sent_bytes"`
	LastKernelReceived    uint64                  `json:"last_kernel_received"`
	LastKernelSent        uint64                  `json:"last_kernel_sent"`
	PeriodStart           time.Time               `json:"period_start"`
	PeriodEnd             time.Time               `json:"period_end"`
	Blocked               bool                    `json:"blocked"`
	ReceiveBPS            uint64                  `json:"-"`
	SendBPS               uint64                  `json:"-"`
	LastCollectedAt       time.Time               `json:"-"`
	CounterEpoch          string                  `json:"counter_epoch,omitempty"`
	LifetimeReceivedBytes uint64                  `json:"lifetime_received_bytes,omitempty"`
	LifetimeSentBytes     uint64                  `json:"lifetime_sent_bytes,omitempty"`
	QuotaBaselineBytes    uint64                  `json:"quota_baseline_bytes,omitempty"`
	KernelCounters        map[string]uint64       `json:"kernel_counters,omitempty"`
}

type trafficState struct {
	Records map[string]*trafficRecord `json:"records"`
	BootID  string                    `json:"boot_id,omitempty"`
}

type TrafficManager struct {
	nativeSource func(context.Context, core.Engine) (nativeAccountingSnapshot, error)
	mu           sync.Mutex
	statePath    string
	backend      trafficCounterBackend
	records      map[string]*trafficRecord
	snapshot     []core.PortTrafficUsage
	dirty        bool
	rulesDirty   bool
	bootID       string
	now          func() time.Time
}

func NewTrafficManager(agentStatePath string) *TrafficManager {
	return NewTrafficManagerForServiceManager(agentStatePath, defaultSystemdServiceManager())
}

func NewTrafficManagerForServiceManager(agentStatePath string, serviceManager *ServiceManager) *TrafficManager {
	manager := &TrafficManager{
		statePath: filepath.Join(filepath.Dir(agentStatePath), "traffic-state.json"),
		backend:   newNFTBackendForServiceManager(serviceManager), records: make(map[string]*trafficRecord), now: time.Now,
	}
	if bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		manager.bootID = strings.TrimSpace(string(bootID))
	}
	state, err := loadTrafficState(manager.statePath)
	if err == nil {
		if err := validateLoadedTrafficState(state, manager.now().UTC()); err == nil {
			manager.records = state.Records
			if state.BootID != "" && state.BootID != manager.bootID {
				for _, record := range manager.records {
					record.LastKernelReceived, record.LastKernelSent = 0, 0
					record.KernelCounters = nil
				}
			}
		} else {
			slog.Warn("ignore invalid port traffic state", "error", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("load port traffic state", "error", err)
	}
	if manager.records == nil {
		manager.records = make(map[string]*trafficRecord)
	}
	return manager
}

func (manager *TrafficManager) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if manager == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		// nftables is a required Agent runtime dependency, even when no quota
		// exists yet. Older nodes may have received binary-only Agent upgrades
		// that never reran install-agent.sh, so repair the package at every Agent
		// start instead of waiting for the first traffic policy.
		if backend, ok := manager.backend.(*nftBackend); ok {
			if err := backend.ensureAvailable(ctx); err != nil {
				slog.Warn("ensure required nftables dependency", "error", err)
			}
		}
		manager.collect(ctx, true)
		ticker := time.NewTicker(trafficCollectionEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				// The parent context is already cancelled. Finish one bounded
				// collection and checkpoint without removing enforcement rules.
				shutdown, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				manager.mu.Lock()
				_ = manager.collectLocked(shutdown, false)
				if err := manager.saveLocked(); err != nil {
					slog.Warn("persist final port traffic counters", "error", err)
				}
				manager.mu.Unlock()
				cancel()
				return
			case <-ticker.C:
				manager.collect(ctx, false)
			}
		}
	}()
	return done
}

func (manager *TrafficManager) SetPolicies(ctx context.Context, policies []core.PortTrafficPolicy, agentID string) error {
	if manager == nil {
		return errors.New("traffic manager is unavailable")
	}
	if len(policies) > 256 {
		return errors.New("control plane returned too many traffic policies")
	}
	seenIDs := make(map[string]struct{}, len(policies))
	seenPorts := make(map[int]struct{}, len(policies))
	now := manager.now().UTC()
	for index := range policies {
		policy := &policies[index]
		if !core.ValidPortTrafficPolicyID(policy.ID) || policy.AgentID != agentID {
			return errors.New("control plane returned a traffic policy for an invalid identity")
		}
		if _, exists := seenIDs[policy.ID]; exists {
			return errors.New("control plane returned duplicate traffic policy IDs")
		}
		if _, exists := seenPorts[policy.Port]; exists {
			return errors.New("control plane returned duplicate traffic policy ports")
		}
		seenIDs[policy.ID] = struct{}{}
		seenPorts[policy.Port] = struct{}{}
		autoBlock := policy.AutoBlock
		normalized, err := core.NormalizePortTrafficPolicyRequest(core.PortTrafficPolicyRequest{
			AgentID: policy.AgentID, Name: policy.Name, Engine: policy.Engine, Port: policy.Port,
			Protocol: policy.Protocol, Cycle: policy.Cycle, CycleAnchor: policy.CycleAnchor, LimitBytes: policy.LimitBytes,
			AutoBlock: &autoBlock,
		}, now)
		if err != nil {
			return fmt.Errorf("control plane returned an invalid traffic policy: %w", err)
		}
		if policy.ResetGeneration == 0 || policy.ResetGeneration > math.MaxInt64 {
			return errors.New("control plane returned an invalid traffic policy generation")
		}
		policy.Name, policy.CycleAnchor = normalized.Name, normalized.CycleAnchor
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	unchanged := len(policies) == len(manager.records)
	for _, policy := range policies {
		record := manager.records[policy.ID]
		if record == nil || !sameTrafficCounter(record.Policy, policy) || record.Policy.AgentID != policy.AgentID ||
			record.Policy.Name != policy.Name || record.Policy.Engine != policy.Engine ||
			record.Policy.LimitBytes != policy.LimitBytes || record.Policy.AutoBlock != policy.AutoBlock {
			unchanged = false
			break
		}
	}
	if unchanged && !manager.rulesDirty {
		return nil
	}
	_ = manager.collectLocked(ctx, false)
	next := make(map[string]*trafficRecord, len(policies))
	for _, policy := range policies {
		record := manager.records[policy.ID]
		if record == nil || !sameTrafficCounter(record.Policy, policy) {
			record = &trafficRecord{KernelCounters: make(map[string]uint64)}
			// The control plane already billed these bytes. Use them only for
			// recovered quota enforcement, never as new lifetime increments.
			start, end, _ := core.TrafficPeriodAt(policy.CycleAnchor, policy.Cycle, now)
			if policy.PeriodStart != nil && policy.PeriodEnd != nil && policy.PeriodStart.Equal(start) && policy.PeriodEnd.Equal(end) {
				record.QuotaBaselineBytes = saturatedTrafficAdd(policy.ReceivedBytes, policy.SentBytes)
				record.PeriodStart, record.PeriodEnd = start, end
			}
		}
		record.Policy = policy
		next[policy.ID] = record
	}
	manager.records = next
	manager.dirty = true
	if err := manager.collectLocked(ctx, true); err != nil {
		slog.Warn("apply port traffic policies", "error", err)
	}
	return nil
}

func (manager *TrafficManager) ClearPolicies(ctx context.Context) error {
	if manager == nil {
		return errors.New("traffic manager is unavailable")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	_ = manager.collectLocked(ctx, false)
	manager.records = make(map[string]*trafficRecord)
	manager.dirty = true
	return manager.collectLocked(ctx, true)
}

func (manager *TrafficManager) Snapshot() []core.PortTrafficUsage {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return append([]core.PortTrafficUsage(nil), manager.snapshot...)
}

func (manager *TrafficManager) collect(ctx context.Context, forceRules bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := manager.collectLocked(ctx, forceRules); err != nil && len(manager.records) > 0 {
		slog.Debug("collect port traffic", "error", err)
	}
}

func (manager *TrafficManager) collectLocked(ctx context.Context, forceRules bool) error {
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
		receivedDelta, sentDelta := collectTrafficCounterDeltas(counters, record)
		if manager.nativeSource != nil {
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
					record.ReceiveBPS, record.SendBPS = 0, 0
					next := *record
					desired[id] = &next
					continue
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
		manager.refreshSnapshotLocked(false, "persist traffic counters: "+err.Error())
		return err
	}
	return nil
}

func (manager *TrafficManager) saveLocked() error {
	if !manager.dirty {
		return nil
	}
	if err := saveTrafficState(manager.statePath, trafficState{Records: manager.records, BootID: manager.bootID}); err != nil {
		return err
	}
	manager.dirty = false
	return nil
}

func (manager *TrafficManager) setUnavailableLocked(err error) error {
	message := strings.TrimSpace(err.Error())
	if len(message) > 500 {
		message = message[:500]
	}
	now := manager.now().UTC()
	for _, record := range manager.records {
		record.ReceiveBPS, record.SendBPS = 0, 0
		if record.PeriodStart.IsZero() || record.PeriodEnd.IsZero() {
			if start, end, periodErr := core.TrafficPeriodAt(record.Policy.CycleAnchor, record.Policy.Cycle, now); periodErr == nil {
				record.PeriodStart, record.PeriodEnd = start, end
			}
		}
	}
	manager.refreshSnapshotLocked(false, message)
	return err
}

func (manager *TrafficManager) refreshSnapshotLocked(available bool, message string) {
	result := make([]core.PortTrafficUsage, 0, len(manager.records))
	for _, id := range sortedTrafficRecordIDs(manager.records) {
		record := manager.records[id]
		accounting := record.Accounting
		if accounting != nil {
			data, _ := json.Marshal(accounting)
			accounting = nil
			_ = json.Unmarshal(data, &accounting)
		}
		healthMessage := message
		if record.AccountingError != "" {
			healthMessage = strings.TrimSpace(healthMessage + "; dual accounting unavailable: " + record.AccountingError)
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
		})
	}
	manager.snapshot = result
}

func sameTrafficCounter(left, right core.PortTrafficPolicy) bool {
	return left.Engine == right.Engine && left.Port == right.Port && left.Protocol == right.Protocol && left.Cycle == right.Cycle &&
		core.UTCDate(left.CycleAnchor).Equal(core.UTCDate(right.CycleAnchor)) && left.ResetGeneration == right.ResetGeneration
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

func trafficCounterName(record *trafficRecord, direction, protocol string) string {
	return "qch_" + record.Policy.ID + "_" + record.CounterEpoch + "_" + direction + "_" + protocol
}

func hasNamedTrafficCounters(counters map[string]uint64, record *trafficRecord) bool {
	for _, protocol := range trafficProtocols(record.Policy.Protocol) {
		if _, ok := counters[trafficCounterName(record, "in", protocol)]; ok {
			return true
		}
		if _, ok := counters[trafficCounterName(record, "out", protocol)]; ok {
			return true
		}
	}
	return false
}

func collectTrafficCounterDeltas(counters map[string]uint64, record *trafficRecord) (uint64, uint64) {
	if !hasNamedTrafficCounters(counters, record) && record.KernelCounters == nil {
		// One-time migration of the original aggregated anonymous baseline.
		received, sent := policyKernelCounters(counters, record.Policy)
		rx, tx := counterDelta(received, record.LastKernelReceived), counterDelta(sent, record.LastKernelSent)
		record.LastKernelReceived, record.LastKernelSent = received, sent
		return rx, tx
	}
	if record.KernelCounters == nil {
		record.KernelCounters = make(map[string]uint64)
	}
	var received, sent uint64
	for _, protocol := range trafficProtocols(record.Policy.Protocol) {
		for _, direction := range []string{"in", "out"} {
			key := trafficCounterName(record, direction, protocol)
			value, exists := counters[key]
			if !exists {
				delete(record.KernelCounters, key)
				delete(record.KernelCounters, "handle:"+key)
				continue
			}
			previous := record.KernelCounters[key]
			handle := counters["handle:"+key]
			if handle != record.KernelCounters["handle:"+key] {
				previous = 0
			}
			delta := counterDelta(value, previous)
			record.KernelCounters[key], record.KernelCounters["handle:"+key] = value, handle
			if direction == "in" {
				received = saturatedTrafficAdd(received, delta)
			} else {
				sent = saturatedTrafficAdd(sent, delta)
			}
		}
	}
	return received, sent
}

func trafficProtocols(protocol core.TrafficProtocol) []string {
	if protocol == core.TrafficProtocolBoth {
		return []string{"tcp", "udp"}
	}
	return []string{string(protocol)}
}

func trafficRuleComment(policyID, direction, protocol string) string {
	return "qch:" + policyID + ":" + direction + ":" + protocol
}

func trafficRuleSetComplete(counters map[string]uint64, records map[string]*trafficRecord) bool {
	for _, record := range records {
		for _, protocol := range trafficProtocols(record.Policy.Protocol) {
			for _, direction := range []string{"in", "out"} {
				name := trafficCounterName(record, direction, protocol)
				if _, exists := counters[name]; !exists || counters["rule:"+name] != 1 {
					return false
				}
				wantDrops := uint64(0)
				if record.Blocked {
					wantDrops = 1
				}
				if counters["drop:"+name] != wantDrops {
					return false
				}
			}
		}
		if record.Accounting != nil && record.Accounting.Source == "nft-dual" {
			for _, protocol := range []string{"tcp", "udp"} {
				for _, direction := range []string{"targetin", "targetout"} {
					name := trafficCounterName(record, direction, protocol)
					if _, exists := counters[name]; !exists || counters["rule:"+name] != 1 {
						return false
					}
				}
			}
		}
	}
	return true
}

func counterDelta(current, previous uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	return current
}

func saturatedTrafficAdd(left, right uint64) uint64 {
	if left >= math.MaxInt64 || right > uint64(math.MaxInt64)-left {
		return math.MaxInt64
	}
	return left + right
}

func trafficUsed(record *trafficRecord) uint64 {
	return saturatedTrafficAdd(record.ReceivedBytes, record.SentBytes)
}

func trafficRate(delta uint64, seconds float64) uint64 {
	if seconds <= 0 || delta == 0 {
		return 0
	}
	rate := float64(delta) / seconds
	if rate >= math.MaxInt64 {
		return math.MaxInt64
	}
	return uint64(rate)
}

func renderTrafficRules(records map[string]*trafficRecord, tableExists bool, counters map[string]uint64) string {
	var script strings.Builder
	if len(records) == 0 {
		if tableExists {
			script.WriteString("delete table inet " + trafficTableName + "\n")
		}
		return script.String()
	}
	script.WriteString("add table inet " + trafficTableName + "\n")
	script.WriteString("add chain inet " + trafficTableName + " input { type filter hook input priority -10; policy accept; }\n")
	script.WriteString("add chain inet " + trafficTableName + " output { type filter hook output priority -10; policy accept; }\n")
	if tableExists {
		// Flush rules only, atomically with their replacements. Named counter
		// objects survive, including bytes arriving after the preceding read.
		script.WriteString("flush chain inet " + trafficTableName + " input\n")
		script.WriteString("flush chain inet " + trafficTableName + " output\n")
	}
	active := make(map[string]bool)
	for _, id := range sortedTrafficRecordIDs(records) {
		record := records[id]
		if record.Accounting != nil && record.Accounting.Source == "nft-dual" {
			mark := record.Accounting.Mark
			for _, protocol := range []string{"tcp", "udp"} {
				for _, direction := range []string{"targetin", "targetout"} {
					name := trafficCounterName(record, direction, protocol)
					active[name] = true
					if _, exists := counters[name]; !exists {
						fmt.Fprintf(&script, "add counter inet %s %s\n", trafficTableName, name)
					}
					chain, match := "input", fmt.Sprintf("ct direction reply ct mark %d", mark)
					if direction == "targetout" {
						chain, match = "output", fmt.Sprintf("ct direction original meta mark %d ct mark set meta mark", mark)
					}
					fmt.Fprintf(&script, "add rule inet %s %s meta l4proto %s %s counter name %s comment %s\n", trafficTableName, chain, protocol, match, name, strconv.Quote(trafficRuleComment(id, direction, protocol)))
				}
			}
		}
		for _, protocol := range trafficProtocols(record.Policy.Protocol) {
			for _, direction := range []string{"in", "out"} {
				name := trafficCounterName(record, direction, protocol)
				active[name] = true
				if _, exists := counters[name]; !exists {
					fmt.Fprintf(&script, "add counter inet %s %s\n", trafficTableName, name)
				}
				chain, portField := "input", "dport"
				if direction == "out" {
					chain, portField = "output", "sport"
				}
				if record.Blocked {
					// Rejected packets never reach the accounting statement.
					fmt.Fprintf(&script, "add rule inet %s %s meta l4proto %s %s %s %d drop comment %s\n",
						trafficTableName, chain, protocol, protocol, portField, record.Policy.Port, strconv.Quote("qch:block:"+name))
				}
				fmt.Fprintf(&script, "add rule inet %s %s meta l4proto %s %s %s %d counter name %s comment %s\n",
					trafficTableName, chain, protocol, protocol, portField, record.Policy.Port, name,
					strconv.Quote(trafficRuleComment(id, direction, protocol)))
			}
		}
	}
	var obsolete []string
	for name := range counters {
		if validTrafficCounterName(name) && !active[name] {
			obsolete = append(obsolete, name)
		}
	}
	sort.Strings(obsolete)
	for _, name := range obsolete {
		fmt.Fprintf(&script, "delete counter inet %s %s\n", trafficTableName, name)
	}
	return script.String()
}

func validTrafficCounterName(name string) bool {
	parts := strings.Split(name, "_")
	return len(parts) == 6 && parts[0] == "qch" && core.ValidPortTrafficPolicyID(parts[1]+"_"+parts[2]) &&
		core.ValidTrafficCounterEpoch(parts[3]) && (parts[4] == "in" || parts[4] == "out" || parts[4] == "targetin" || parts[4] == "targetout") && (parts[5] == "tcp" || parts[5] == "udp")
}

type nftBackend struct {
	nftPath        string
	systemdRunPath string
	direct         bool
	initialization error
	missing        bool
	installMu      sync.Mutex
	installTried   bool
	installer      func(context.Context, *nftBackend) error
}

var (
	nftExecutablePath = "/usr/sbin/nft"
	nftAPTGetPath     = "/usr/bin/apt-get"
	nftAPKPaths       = []string{"/usr/sbin/apk", "/sbin/apk"}
)

func newNFTBackend() *nftBackend {
	return newNFTBackendForServiceManager(defaultSystemdServiceManager())
}

func newNFTBackendForServiceManager(serviceManager *ServiceManager) *nftBackend {
	serviceManager = selectedServiceManager(serviceManager)
	backend := &nftBackend{nftPath: nftExecutablePath, direct: processHasCapability(12), installer: installNFTablesPackage}
	if serviceManager.Kind() == ServiceManagerSystemd {
		backend.systemdRunPath = "/usr/bin/systemd-run"
	}
	if err := validatePrivilegedExecutable(backend.nftPath); err != nil {
		backend.initialization = fmt.Errorf("nftables is unavailable: %w", err)
		_, statErr := os.Lstat(backend.nftPath)
		backend.missing = errors.Is(statErr, os.ErrNotExist)
	} else if !backend.direct && serviceManager.Kind() == ServiceManagerOpenRC {
		backend.initialization = errors.New("nftables CAP_NET_ADMIN is unavailable in the OpenRC Agent service; update the QAgent OpenRC service and restart it")
	} else if !backend.direct {
		if err := validatePrivilegedExecutable(backend.systemdRunPath); err != nil {
			backend.initialization = fmt.Errorf("nftables privilege runner is unavailable: %w", err)
		}
	}
	return backend
}

func (backend *nftBackend) Counters(ctx context.Context) (map[string]uint64, bool, error) {
	if err := backend.ensureAvailable(ctx); err != nil {
		return nil, false, err
	}
	output, err := backend.run(ctx, nil, "-j", "list", "table", "inet", trafficTableName)
	if err != nil {
		if strings.Contains(err.Error(), "No such file or directory") {
			return map[string]uint64{}, false, nil
		}
		return nil, false, fmt.Errorf("read nftables traffic counters: %w", err)
	}
	counters, err := parseNFTTrafficCounters(output)
	if err != nil {
		return nil, true, err
	}
	return counters, true, nil
}

func (backend *nftBackend) Replace(ctx context.Context, script string) error {
	if err := backend.ensureAvailable(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(script) == "" {
		return nil
	}
	if _, err := backend.run(ctx, []byte(script), "-f", "-"); err != nil {
		return fmt.Errorf("apply nftables traffic rules: %w", err)
	}
	return nil
}

func (backend *nftBackend) ensureAvailable(ctx context.Context) error {
	if backend == nil {
		return errors.New("nftables backend is unavailable")
	}
	backend.installMu.Lock()
	defer backend.installMu.Unlock()
	if backend.initialization == nil {
		return nil
	}
	if !backend.missing || backend.installTried {
		return backend.initialization
	}
	backend.installTried = true
	installer := backend.installer
	if installer == nil {
		installer = installNFTablesPackage
	}
	installContext, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := installer(installContext, backend); err != nil {
		backend.initialization = fmt.Errorf("nftables is unavailable and automatic installation failed: %w", err)
		return backend.initialization
	}
	if err := validatePrivilegedExecutable(backend.nftPath); err != nil {
		backend.initialization = fmt.Errorf("nftables package was installed but the executable is unavailable: %w", err)
		return backend.initialization
	}
	if !backend.direct && backend.systemdRunPath == "" {
		backend.initialization = errors.New("nftables CAP_NET_ADMIN is unavailable in the OpenRC Agent service; update the QAgent OpenRC service and restart it")
		return backend.initialization
	}
	if !backend.direct {
		if err := validatePrivilegedExecutable(backend.systemdRunPath); err != nil {
			backend.initialization = fmt.Errorf("nftables privilege runner is unavailable: %w", err)
			return backend.initialization
		}
	}
	backend.missing = false
	backend.initialization = nil
	return nil
}

func installNFTablesPackage(ctx context.Context, backend *nftBackend) error {
	if backend == nil {
		return errors.New("nftables backend is unavailable")
	}
	if err := validatePrivilegedExecutable(nftAPTGetPath); err == nil {
		if err := runNFTPackageCommand(ctx, backend, []string{"DEBIAN_FRONTEND=noninteractive"}, nftAPTGetPath, "update", "-qq"); err != nil {
			return fmt.Errorf("update APT metadata: %w", err)
		}
		if err := runNFTPackageCommand(ctx, backend, []string{"DEBIAN_FRONTEND=noninteractive"}, nftAPTGetPath,
			"install", "-y", "--no-install-recommends", "nftables"); err != nil {
			return fmt.Errorf("install nftables with APT: %w", err)
		}
		return nil
	}
	for _, apkPath := range nftAPKPaths {
		if err := validatePrivilegedExecutable(apkPath); err != nil {
			continue
		}
		if err := runNFTPackageCommand(ctx, backend, nil, apkPath, "add", "--no-cache", "nftables"); err != nil {
			return fmt.Errorf("install nftables with apk: %w", err)
		}
		return nil
	}
	return errors.New("no supported Debian apt-get or Alpine apk package manager was found")
}

func runNFTPackageCommand(ctx context.Context, backend *nftBackend, environment []string, executable string, arguments ...string) error {
	commandName := executable
	commandArguments := append([]string(nil), arguments...)
	if backend != nil && backend.systemdRunPath != "" {
		if _, err := os.Lstat("/run/systemd/system"); err == nil {
			if err := validatePrivilegedExecutable(backend.systemdRunPath); err != nil {
				return fmt.Errorf("validate systemd package runner: %w", err)
			}
			commandName = backend.systemdRunPath
			commandArguments = []string{
				"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec",
				"--property=User=root", "--property=NoNewPrivileges=no",
				"--property=RuntimeMaxSec=40s", "--property=TimeoutStopSec=5s",
			}
			for _, value := range environment {
				commandArguments = append(commandArguments, "--setenv="+value)
			}
			commandArguments = append(commandArguments, "--", executable)
			commandArguments = append(commandArguments, arguments...)
			environment = nil
		}
	}
	command := exec.CommandContext(ctx, commandName, commandArguments...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	for _, value := range environment {
		command.Env = append(command.Env, value)
	}
	configureCommand(command)
	output := &boundedOutput{limit: 64 << 10}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func (backend *nftBackend) run(ctx context.Context, input []byte, nftArguments ...string) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	commandName := backend.nftPath
	arguments := nftArguments
	if !backend.direct {
		commandName = backend.systemdRunPath
		arguments = []string{
			"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec", "--setenv=LC_ALL=C",
			"--property=User=root", "--property=NoNewPrivileges=yes",
			"--property=CapabilityBoundingSet=CAP_NET_ADMIN", "--property=AmbientCapabilities=CAP_NET_ADMIN",
			"--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=PrivateTmp=yes",
			"--property=PrivateDevices=yes", "--property=RestrictAddressFamilies=AF_NETLINK",
			"--", backend.nftPath,
		}
		arguments = append(arguments, nftArguments...)
	}
	command := exec.CommandContext(commandContext, commandName, arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return stdout.Bytes(), nil
}

func processHasCapability(bit uint) bool {
	contents, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(contents), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), 16, 64)
		return err == nil && value&(uint64(1)<<bit) != 0
	}
	return false
}

func parseNFTTrafficCounters(contents []byte) (map[string]uint64, error) {
	var document struct {
		NFTables []struct {
			Counter *struct {
				Name   string `json:"name"`
				Bytes  uint64 `json:"bytes"`
				Handle uint64 `json:"handle"`
			} `json:"counter"`
			Rule *struct {
				Comment string `json:"comment"`
				Expr    []struct {
					Counter json.RawMessage `json:"counter"`
				} `json:"expr"`
			} `json:"rule"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		return nil, fmt.Errorf("parse nftables traffic counters: %w", err)
	}
	result := make(map[string]uint64)
	for _, item := range document.NFTables {
		if item.Counter != nil && validTrafficCounterName(item.Counter.Name) {
			result[item.Counter.Name] = item.Counter.Bytes
			result["handle:"+item.Counter.Name] = item.Counter.Handle
		}
		if item.Rule != nil && strings.HasPrefix(item.Rule.Comment, "qch:block:") {
			name := strings.TrimPrefix(item.Rule.Comment, "qch:block:")
			if validTrafficCounterName(name) {
				result["drop:"+name]++
			}
		}
		if item.Rule == nil || !strings.HasPrefix(item.Rule.Comment, "qch:trf_") {
			continue
		}
		for _, expression := range item.Rule.Expr {
			if len(expression.Counter) == 0 {
				continue
			}
			var name string
			if json.Unmarshal(expression.Counter, &name) == nil {
				if validTrafficCounterName(name) {
					result["rule:"+name]++
				}
				continue
			}
			var counter struct {
				Bytes uint64 `json:"bytes"`
			}
			if err := json.Unmarshal(expression.Counter, &counter); err != nil {
				return nil, fmt.Errorf("parse nftables counter expression: %w", err)
			}
			result[item.Rule.Comment] = saturatedTrafficAdd(result[item.Rule.Comment], counter.Bytes)
		}
	}
	return result, nil
}

func loadTrafficState(path string) (trafficState, error) {
	directory := filepath.Dir(path)
	if err := validateStateDirectory(directory); err != nil {
		return trafficState{}, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return trafficState{}, err
	}
	defer root.Close()
	baseName := filepath.Base(path)
	linkInfo, err := root.Lstat(baseName)
	if err != nil {
		return trafficState{}, err
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return trafficState{}, errors.New("traffic state must not be a symlink")
	}
	file, err := root.Open(baseName)
	if err != nil {
		return trafficState{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return trafficState{}, errors.New("traffic state must be a private regular file")
	}
	if err := validateOwner(info, "traffic state file"); err != nil {
		return trafficState{}, err
	}
	contents, err := io.ReadAll(io.LimitReader(file, trafficStateMaxBytes+1))
	if err != nil {
		return trafficState{}, err
	}
	if len(contents) > trafficStateMaxBytes {
		return trafficState{}, errors.New("traffic state exceeds 2 MiB")
	}
	var state trafficState
	if err := json.Unmarshal(contents, &state); err != nil {
		return trafficState{}, err
	}
	return state, nil
}

func validateLoadedTrafficState(state trafficState, now time.Time) error {
	if len(state.Records) > 256 {
		return errors.New("traffic state contains too many policies")
	}
	for id, record := range state.Records {
		if record == nil || id != record.Policy.ID || !core.ValidPortTrafficPolicyID(id) || record.Policy.ResetGeneration == 0 {
			return errors.New("traffic state contains an invalid policy identity")
		}
		if _, err := core.NormalizePortTrafficPolicyRequest(core.PortTrafficPolicyRequest{
			AgentID: record.Policy.AgentID, Name: record.Policy.Name, Engine: record.Policy.Engine,
			Port: record.Policy.Port, Protocol: record.Policy.Protocol, Cycle: record.Policy.Cycle,
			CycleAnchor: record.Policy.CycleAnchor, LimitBytes: record.Policy.LimitBytes, AutoBlock: &record.Policy.AutoBlock,
		}, now); err != nil {
			return fmt.Errorf("traffic state contains an invalid policy: %w", err)
		}
		if record.ReceivedBytes > math.MaxInt64 || record.SentBytes > math.MaxInt64 || record.LifetimeReceivedBytes > math.MaxInt64 || record.LifetimeSentBytes > math.MaxInt64 || record.QuotaBaselineBytes > math.MaxInt64 ||
			record.LastKernelReceived > math.MaxInt64 || record.LastKernelSent > math.MaxInt64 {
			return errors.New("traffic state contains an out-of-range counter")
		}
		if record.CounterEpoch != "" && !core.ValidTrafficCounterEpoch(record.CounterEpoch) {
			return errors.New("traffic state contains an invalid counter epoch")
		}
		if !record.Accounting.Valid() {
			return errors.New("traffic state contains invalid accounting metadata")
		}
		if len(record.KernelCounters) > 16 {
			return errors.New("traffic state contains too many kernel counters")
		}
		for key := range record.KernelCounters {
			name := strings.TrimPrefix(key, "handle:")
			if !validTrafficCounterName(name) || !strings.HasPrefix(name, "qch_"+id+"_"+record.CounterEpoch+"_") {
				return errors.New("traffic state contains an invalid kernel counter")
			}
		}
	}
	return nil
}

func saveTrafficState(path string, state trafficState) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := validateStateDirectory(directory); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	suffix, err := randomSuffix(10)
	if err != nil {
		return err
	}
	tempName := ".traffic-state-" + suffix + ".tmp"
	temp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(tempName)
	if err := json.NewEncoder(temp).Encode(state); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := root.Rename(tempName, filepath.Base(path)); err != nil {
		return err
	}
	return syncRootDirectory(root)
}
