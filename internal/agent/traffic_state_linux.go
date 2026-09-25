package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
	if manager.haltShared != nil {
		seen := map[core.Engine]bool{}
		for _, engine := range manager.recoveryEngines {
			seen[engine] = true
			haltContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			haltErr := manager.haltShared(haltContext, engine)
			if manager.haltLegacyShared != nil {
				haltErr = errors.Join(haltErr, manager.haltLegacyShared(haltContext, engine, manager.sharedPortsForEngine(engine)))
			}
			cancel()
			if haltErr != nil {
				err = errors.Join(err, haltErr)
			}
		}
		for _, record := range manager.records {
			if record.Policy.SharedQuota == nil || seen[record.Policy.Engine] {
				continue
			}
			seen[record.Policy.Engine] = true
			haltContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			haltErr := manager.haltShared(haltContext, record.Policy.Engine)
			if manager.haltLegacyShared != nil {
				haltErr = errors.Join(haltErr, manager.haltLegacyShared(haltContext, record.Policy.Engine, manager.sharedPortsForEngine(record.Policy.Engine)))
			}
			cancel()
			if haltErr != nil {
				err = errors.Join(err, fmt.Errorf("stop unmetered shared core: %w", haltErr))
			}
		}
	}
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

func (manager *TrafficManager) sharedGuardPath() string {
	return filepath.Join(filepath.Dir(manager.statePath), "shared-traffic-guard.json")
}

func hasSharedTraffic(records map[string]*trafficRecord) bool {
	for _, record := range records {
		if record.Policy.SharedQuota != nil {
			return true
		}
	}
	return false
}

func (manager *TrafficManager) recoverSharedEngines(records map[string]*trafficRecord) {
	for _, record := range records {
		if record.Policy.SharedQuota != nil && !slices.Contains(manager.recoveryEngines, record.Policy.Engine) {
			manager.recoveryEngines = append(manager.recoveryEngines, record.Policy.Engine)
		}
		if record.Policy.SharedQuota != nil && record.Policy.Port > 0 {
			if manager.recoveryPorts == nil {
				manager.recoveryPorts = make(map[core.Engine][]int)
			}
			ports := manager.recoveryPorts[record.Policy.Engine]
			if !slices.Contains(ports, record.Policy.Port) {
				manager.recoveryPorts[record.Policy.Engine] = append(ports, record.Policy.Port)
			}
		}
	}
}

func (manager *TrafficManager) sharedPortsForEngine(engine core.Engine) []int {
	ports := append([]int(nil), manager.recoveryPorts[engine]...)
	for _, record := range manager.records {
		if record.Policy.SharedQuota != nil && record.Policy.Engine == engine &&
			!slices.Contains(ports, record.Policy.Port) {
			ports = append(ports, record.Policy.Port)
		}
	}
	return ports
}

func sharedTrafficGuardMatches(state, guard trafficState) bool {
	count := 0
	for id, record := range state.Records {
		if record.Policy.SharedQuota == nil {
			continue
		}
		count++
		activated := guard.Records[id]
		if activated == nil || record.Policy.AgentID != activated.Policy.AgentID ||
			!sameTrafficCounter(record.Policy, activated.Policy) || !sameSharedQuota(record.Policy.SharedQuota, activated.Policy.SharedQuota) {
			return false
		}
	}
	return count == len(guard.Records)
}
