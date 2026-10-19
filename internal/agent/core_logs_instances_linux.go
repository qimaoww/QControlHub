//go:build linux

package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const maxCoreLogInstances = 256

type coreLogInstanceRun struct {
	collector *CoreLogCollector
	cancel    context.CancelFunc
	spec      EngineSpec
	engine    core.Engine
	shareID   string
}

// Separate collectors retain separate cursors and health, while all instances
// share the parent's bounded upload queue and node-wide file retention budget.
func (c *CoreLogCollector) prepareSharedLogSource(ctx context.Context, engine core.Engine, spec EngineSpec) error {
	if c.executor == nil {
		return nil
	}
	share := filepath.Base(filepath.Dir(spec.ConfigPath))
	if !isSharedInstanceSpec(engine, spec, c.executor.serviceManager().Kind()) {
		return fmt.Errorf("invalid shared log source")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(spec.ConfigPath)); err != nil {
		return err
	}
	if c.executor.serviceManager().Kind() == ServiceManagerSystemd {
		if err := validateManagedSharedService(ctx, engine, spec); err != nil {
			return err
		}
	} else {
		path := filepath.Join("/etc/init.d", spec.Service)
		if err := validateManagedUnitFile(path); err != nil {
			return err
		}
		if err := validateOpenRCCoreOverrides(spec.Service); err != nil {
			return err
		}
		want, err := openRCSharedInstanceContent(engine, spec)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, []byte(want)) {
			return fmt.Errorf("shared OpenRC log service identity changed")
		}
	}
	key := string(engine) + "/" + share
	c.instancesMu.Lock()
	c.mu.Lock()
	runCtx, stopped := c.runContext, c.runStopped
	c.mu.Unlock()
	if stopped || runCtx == nil || runCtx.Err() != nil {
		c.instancesMu.Unlock()
		return nil
	}
	if c.instances == nil {
		c.instances = map[string]*coreLogInstanceRun{}
	}
	if c.instances[key] != nil {
		c.instancesMu.Unlock()
		return nil
	}
	if len(c.instances) >= maxCoreLogInstances {
		c.instancesMu.Unlock()
		return fmt.Errorf("shared log collector instance limit reached")
	}
	child := NewCoreLogCollectorForServiceManager(c.executor.serviceManager(), map[core.Engine]EngineSpec{engine: spec})
	if c.executor.serviceManager().Kind() == ServiceManagerSystemd {
		child.sources = nil
	}
	child.parent = c
	child.instanceID = share
	child.instanceService = spec.Service
	childCtx, cancel := context.WithCancel(runCtx)
	c.instances[key] = &coreLogInstanceRun{child, cancel, spec, engine, share}
	c.rebalanceInstanceBudgetsLocked()
	c.runWait.Add(1)
	go func() { defer c.runWait.Done(); child.Run(childCtx) }()
	c.instancesMu.Unlock()
	child.mu.Lock()
	policy := core.AgentPolicy{CoreLogMaxMiB: uint32(child.coreLogMaxBytes >> 20), CoreLogRotateCount: uint32(child.coreLogRotateCount)}
	child.mu.Unlock()
	child.ApplyPolicy(policy)
	c.notifySharedJournals()
	// The fixed journal cursor / file EOF must be armed before a deployment can
	// start the service; otherwise its startup logs disappear in --lines=0.
	waitCtx, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	return child.waitForConsoleSource(waitCtx, engine)
}

func (c *CoreLogCollector) refreshSharedLogSources(ctx context.Context) {
	if c.executor == nil {
		return
	}
	c.discoveryMu.Lock()
	defer c.discoveryMu.Unlock()
	c.executor.specsMu.RLock()
	specs := map[core.Engine]EngineSpec{}
	for engine, spec := range c.executor.Specs {
		specs[engine] = spec
	}
	c.executor.specsMu.RUnlock()
	seen := map[string]bool{}
	for engine, base := range specs {
		entries, err := os.ReadDir(sharedInstanceRoot(engine))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || !core.ValidAgentShareID(entry.Name()) {
				continue
			}
			spec, err := sharedInstanceSpec(engine, base, entry.Name(), c.executor.serviceManager().Kind())
			if err != nil {
				continue
			}
			key := string(engine) + "/" + entry.Name()
			seen[key] = true
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = c.prepareSharedLogSource(checkCtx, engine, spec)
			cancel()
			if err != nil {
				c.instancesMu.Lock()
				if run := c.instances[key]; run != nil {
					run.cancel()
					delete(c.instances, key)
					c.notifySharedJournals()
				}
				if c.instanceFailures == nil {
					c.instanceFailures = map[string]core.SharedInstanceStatus{}
				}
				c.instanceFailures[key] = core.SharedInstanceStatus{Engine: engine, ShareID: entry.Name(), CoreLogStatus: "failed", CoreLogError: coreLogErrorCode(err)}
				c.instancesMu.Unlock()
			} else {
				c.instancesMu.Lock()
				delete(c.instanceFailures, key)
				c.instancesMu.Unlock()
			}
		}
	}
	removed := false
	c.instancesMu.Lock()
	for key, run := range c.instances {
		if !seen[key] {
			removed = true
			run.cancel()
			delete(c.instances, key)
		}
	}
	for key := range c.instanceFailures {
		if !seen[key] {
			delete(c.instanceFailures, key)
		}
	}
	c.rebalanceInstanceBudgetsLocked()
	c.instancesMu.Unlock()
	if removed {
		c.notifySharedJournals()
	}
}

func (c *CoreLogCollector) rebalanceInstanceBudgetsLocked() {
	c.mu.Lock()
	c.fileBudgetInstances = len(c.instances) + 1
	maxBytes, count := c.coreLogMaxBytes, c.coreLogRotateCount
	c.mu.Unlock()
	for _, run := range c.instances {
		run.collector.mu.Lock()
		run.collector.coreLogMaxBytes = maxBytes
		run.collector.coreLogRotateCount = count
		run.collector.fileBudgetInstances = len(c.instances) + 1
		run.collector.mu.Unlock()
	}
}

func (c *CoreLogCollector) InstanceStatuses() map[string]core.SharedInstanceStatus {
	c.instancesMu.Lock()
	defer c.instancesMu.Unlock()
	result := map[string]core.SharedInstanceStatus{}
	for key, run := range c.instances {
		state := run.collector.Status()[run.engine]
		if state.Status == "" {
			state.Status = "pending"
		}
		result[key] = core.SharedInstanceStatus{Engine: run.engine, ShareID: run.shareID, CoreLogStatus: state.Status, CoreLogError: state.Error}
	}
	for key, state := range c.instanceFailures {
		result[key] = state
	}
	return result
}

// Only two shared journal followers exist per node. Reconfigure their unit
// sets with the last cursor intact when instances appear/disappear.
func (c *CoreLogCollector) notifySharedJournals() {
	for _, updates := range c.sharedJournalUpdates {
		if updates != nil {
			select {
			case updates <- struct{}{}:
			default:
			}
		}
	}
}
func (c *CoreLogCollector) runSharedJournal(ctx context.Context, namespace bool, updates <-chan struct{}) {
	resume := ""
	recoverySince := time.Now().UTC()
	for ctx.Err() == nil {
		c.instancesMu.Lock()
		units := map[string]core.Engine{}
		children := []*coreLogInstanceRun{}
		for _, child := range c.instances {
			units[child.spec.Service] = child.engine
			children = append(children, child)
		}
		c.instancesMu.Unlock()
		if len(units) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-updates:
				continue
			}
		}
		source := newCoreLogJournalSource(units, namespace)
		source.recoverySince = &recoverySince
		source.setStatus = func(status, code string) {
			// The managed namespace is authoritative. A generic fallback can still
			// prove itself by delivering a line; its absence cannot mask namespace health.
			if !namespace {
				return
			}
			for _, child := range children {
				child.collector.setSourceStatus(child.engine, "journal", status, code)
			}
		}
		if err := validatePrivilegedExecutable(journalctlPath); err != nil {
			source.setStatus("failed", "collector-unavailable")
			select {
			case <-ctx.Done():
				return
			case <-updates:
			case <-time.After(2 * time.Second):
			}
			continue
		}
		readCtx, cancel := context.WithCancel(ctx)
		type result struct {
			resume string
			err    error
		}
		finished := make(chan result, 1)
		go func() { next, err := c.follow(readCtx, source, resume); finished <- result{next, err} }()
		changed := false
		var outcome result
		select {
		case <-updates:
			changed = true
			cancel()
			outcome = <-finished
		case <-ctx.Done():
			cancel()
			<-finished
			return
		case outcome = <-finished:
		}
		cancel()
		if outcome.resume != "" {
			resume = outcome.resume
		}
		if changed {
			continue
		}
		source.setStatus("failed", coreLogErrorCode(outcome.err))
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-updates:
			timer.Stop()
		case <-timer.C:
		}
	}
}
