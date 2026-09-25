package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
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
	ShareUsedBytes        uint64                  `json:"share_used_bytes,omitempty"`
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
	nativeSource     func(context.Context, core.Engine) (nativeAccountingSnapshot, error)
	haltShared       func(context.Context, core.Engine) error
	haltLegacyShared func(context.Context, core.Engine, []int) error
	haltShare        func(context.Context, core.Engine, string) error
	mu               sync.Mutex
	statePath        string
	backend          trafficCounterBackend
	records          map[string]*trafficRecord
	snapshot         []core.PortTrafficUsage
	dirty            bool
	rulesDirty       bool
	bootID           string
	now              func() time.Time
	awaitingPolicies bool
	recoveryEngines  []core.Engine
	recoveryPorts    map[core.Engine][]int
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
	validState := false
	if err == nil {
		if err := validateLoadedTrafficState(state, manager.now().UTC()); err == nil {
			validState = true
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
	guard, guardErr := loadTrafficState(manager.sharedGuardPath())
	validGuard := guardErr == nil && validateLoadedTrafficState(guard, manager.now().UTC()) == nil
	if !validState || (validGuard && !sharedTrafficGuardMatches(state, guard)) || (!validGuard && !errors.Is(guardErr, os.ErrNotExist)) {
		// The activation guard is written before changing enforcement. A
		// missing, corrupt, or older checkpoint must not undo that policy
		// transition while the control plane is unreachable.
		manager.awaitingPolicies = true
		if validState {
			manager.recoverSharedEngines(manager.records)
		}
		if validGuard {
			manager.recoverSharedEngines(guard.Records)
		} else if !errors.Is(guardErr, os.ErrNotExist) || (err != nil && !errors.Is(err, os.ErrNotExist)) || (err == nil && !validState) {
			manager.recoveryEngines = []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust}
		}
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
	policies = append([]core.PortTrafficPolicy(nil), policies...)
	groups := make(map[string]core.SharedTrafficQuota)
	now := manager.now().UTC()
	for index := range policies {
		policy := &policies[index]
		if !policy.SharedQuota.Valid() {
			return errors.New("control plane returned an invalid shared traffic allocation")
		}
		if policy.SharedQuota != nil {
			quota := *policy.SharedQuota
			quota.Engines = append([]core.Engine(nil), quota.Engines...)
			policy.SharedQuota = &quota
			if policy.Protocol != core.TrafficProtocolBoth {
				return errors.New("shared traffic requires both listener transports")
			}
			group := quota
			group.PortUsedBytes = 0
			if previous, exists := groups[quota.ID]; exists && !sameSharedQuota(&previous, &group) {
				return errors.New("control plane returned inconsistent shared allocation totals")
			}
			groups[quota.ID] = group
			// The wire carries zero/auto-block for fail-closed compatibility
			// with old Agents. Shared quotas replace per-port calendar limits.
			policy.LimitBytes, policy.AutoBlock = math.MaxInt64, false
		}
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
			record.Policy.LimitBytes != policy.LimitBytes || record.Policy.AutoBlock != policy.AutoBlock ||
			!sameSharedQuota(record.Policy.SharedQuota, policy.SharedQuota) {
			unchanged = false
			break
		}
	}
	if unchanged && !manager.rulesDirty && !manager.awaitingPolicies {
		return nil
	}
	_ = manager.collectLocked(ctx, false)
	next := make(map[string]*trafficRecord, len(policies))
	for _, policy := range policies {
		record := manager.records[policy.ID]
		sharedUsed := uint64(0)
		if record != nil && record.Policy.SharedQuota != nil && policy.SharedQuota != nil && record.Policy.SharedQuota.ID == policy.SharedQuota.ID {
			sharedUsed = record.ShareUsedBytes
		}
		if record == nil || !sameTrafficCounter(record.Policy, policy) {
			record = &trafficRecord{KernelCounters: make(map[string]uint64)}
			// The control plane already billed these bytes. Use them only for
			// recovered quota enforcement, never as new lifetime increments.
			start, end, _ := core.TrafficPeriodAt(policy.CycleAnchor, policy.Cycle, now)
			if policy.PeriodStart != nil && policy.PeriodEnd != nil && policy.PeriodStart.Equal(start) && policy.PeriodEnd.Equal(end) {
				record.QuotaBaselineBytes = saturatedTrafficAdd(policy.ReceivedBytes, policy.SentBytes)
				record.PeriodStart, record.PeriodEnd = start, end
			}
		} else {
			// Do not partially mutate the current policy snapshot if writing
			// the activation guard fails.
			copy := *record
			record = &copy
		}
		if policy.SharedQuota != nil {
			record.ShareUsedBytes = max(sharedUsed, policy.SharedQuota.PortUsedBytes)
		} else {
			record.ShareUsedBytes = 0
		}
		record.Policy = policy
		next[policy.ID] = record
	}
	guard := trafficState{Records: make(map[string]*trafficRecord)}
	for id, record := range next {
		if record.Policy.SharedQuota != nil {
			guard.Records[id] = record
		}
	}
	if len(guard.Records) > 0 || len(manager.recoveryEngines) > 0 || hasSharedTraffic(manager.records) {
		if err := saveTrafficState(manager.sharedGuardPath(), guard); err != nil {
			manager.awaitingPolicies = true
			manager.recoverSharedEngines(next)
			return manager.setUnavailableLocked(fmt.Errorf("persist shared traffic activation guard: %w", err))
		}
	}
	type instanceKey struct {
		shareID string
		engine  core.Engine
	}
	authorized := map[instanceKey]map[int]bool{}
	toStop := map[instanceKey]struct{}{}
	legacyStopPorts := map[core.Engine][]int{}
	addLegacyPort := func(engine core.Engine, port int) {
		if !slices.Contains(legacyStopPorts[engine], port) {
			legacyStopPorts[engine] = append(legacyStopPorts[engine], port)
		}
	}
	for _, record := range next {
		if quota := record.Policy.SharedQuota; quota != nil {
			key := instanceKey{quota.ID, record.Policy.Engine}
			if quota.AllowsEngine(record.Policy.Engine) &&
				(quota.LimitBytes == 0 || quota.UsedBytes < quota.LimitBytes) {
				if authorized[key] == nil {
					authorized[key] = map[int]bool{}
				}
				authorized[key][record.Policy.Port] = true
			} else {
				toStop[key] = struct{}{}
				addLegacyPort(record.Policy.Engine, record.Policy.Port)
			}
		}
	}
	for _, record := range manager.records {
		if quota := record.Policy.SharedQuota; quota != nil {
			key := instanceKey{quota.ID, record.Policy.Engine}
			if !authorized[key][record.Policy.Port] {
				toStop[key] = struct{}{}
				addLegacyPort(record.Policy.Engine, record.Policy.Port)
			}
		}
	}
	// Stop removed, revoked, or exhausted instances while the old nftables
	// rules still protect their listener ports.
	if manager.haltShare != nil {
		for instance := range toStop {
			if err := manager.haltShare(ctx, instance.engine, instance.shareID); err != nil {
				manager.awaitingPolicies = true
				manager.recoverSharedEngines(next)
				return manager.setUnavailableLocked(fmt.Errorf("stop revoked shared instance: %w", err))
			}
		}
	}
	if manager.haltLegacyShared != nil {
		for engine, ports := range legacyStopPorts {
			if err := manager.haltLegacyShared(ctx, engine, ports); err != nil {
				manager.awaitingPolicies = true
				manager.recoverSharedEngines(next)
				return manager.setUnavailableLocked(fmt.Errorf("stop legacy shared core before changing enforcement: %w", err))
			}
		}
	}
	manager.records = next
	manager.awaitingPolicies = false
	manager.recoveryEngines = nil
	manager.recoveryPorts = nil
	manager.dirty = true
	if err := manager.collectLocked(ctx, true); err != nil {
		slog.Warn("apply port traffic policies", "error", err)
		if len(groups) > 0 {
			return err // Never execute a shared task without working enforcement.
		}
	}
	return nil
}

func (manager *TrafficManager) ClearPolicies(ctx context.Context) error {
	if manager == nil {
		return errors.New("traffic manager is unavailable")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	engines := make(map[core.Engine]bool)
	for _, engine := range manager.recoveryEngines {
		engines[engine] = true
	}
	for _, record := range manager.records {
		if record.Policy.SharedQuota != nil {
			engines[record.Policy.Engine] = true
		}
	}
	for engine := range engines {
		if manager.haltShared == nil {
			return errors.New("cannot clear shared enforcement before stopping its core")
		}
		stopErr := manager.haltShared(ctx, engine)
		if manager.haltLegacyShared != nil {
			stopErr = errors.Join(stopErr, manager.haltLegacyShared(ctx, engine, manager.sharedPortsForEngine(engine)))
		}
		if stopErr != nil {
			return fmt.Errorf("stop shared core before clearing enforcement: %w", stopErr)
		}
	}
	_ = manager.collectLocked(ctx, false)
	if len(engines) > 0 {
		if err := saveTrafficState(manager.sharedGuardPath(), trafficState{Records: map[string]*trafficRecord{}}); err != nil {
			manager.awaitingPolicies = true
			return manager.setUnavailableLocked(fmt.Errorf("clear shared traffic activation guard: %w", err))
		}
	}
	manager.awaitingPolicies = false
	manager.recoveryEngines = nil
	manager.recoveryPorts = nil
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
