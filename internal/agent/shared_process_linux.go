package agent

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// A dedicated ancestor survives service restarts, so sockets can be classified
// from their first packet, including sockets opened by a child of the core.
func processSliceName(engine core.Engine, share string) string {
	return "qch" + strings.ReplaceAll(managedCoreAssetName(engine), "-", "") + share + ".slice"
}

func sharedOpenRCGroupLine(engine core.Engine, spec EngineSpec) string {
	group := processSliceName(engine, filepath.Base(filepath.Dir(spec.ConfigPath)))
	return "\techo \"$$\" > /sys/fs/cgroup/" + group + "/cgroup.procs || return 1\n"
}

func (e *Executor) sharedProcessAccounting(ctx context.Context, engine core.Engine, share string) (nativeAccountingSnapshot, error) {
	var result nativeAccountingSnapshot
	manager := e.serviceManager()
	base := DefaultSpecsForServiceManager(manager.Kind())[engine]
	spec, err := sharedInstanceSpec(engine, base, share, manager.Kind())
	if err != nil {
		return result, err
	}
	e.specsMu.RLock()
	valid := e.Specs[engine] == base && e.ManagedSafetyIssues[engine] == ""
	e.specsMu.RUnlock()
	if !valid {
		return result, errors.New("shared process accounting requires a managed core")
	}
	if _, err = os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		return result, fmt.Errorf("shared process accounting requires cgroup v2: %w", err)
	}
	group := processSliceName(engine, share)
	path := filepath.Join("/sys/fs/cgroup", group)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		if manager.Kind() == ServiceManagerOpenRC {
			err = os.Mkdir(path, 0755)
			if errors.Is(err, os.ErrExist) {
				err = nil
			}
		} else {
			_, err = run(ctx, manager.executable, "start", group)
		}
		if err != nil {
			return result, fmt.Errorf("prepare shared process cgroup: %w", err)
		}
		info, err = os.Stat(path)
	}
	if err != nil {
		return result, err
	}
	result.GroupInode = info.Sys().(*syscall.Stat_t).Ino
	result.Plan.Source = "process"
	// Deployments may spend minutes on certificates. The slice and its counters
	// remain authoritative throughout, including while the service is replaced.
	lock := e.sharedAccountingLock(engine, share)
	if !lock.TryRLock() {
		return result, nil
	}
	defer lock.RUnlock()
	// A first allocation has no unit or configuration yet. Its process meter
	// must be installed before the deploy task can start the service.
	if _, err := os.Stat(spec.ConfigPath); errors.Is(err, os.ErrNotExist) {
		if _, identityErr := os.Stat(sharedStatePath(spec)); errors.Is(identityErr, os.ErrNotExist) {
			return result, nil
		}
	}
	epoch, state, err := e.accountingProcessEpochWithState(ctx, spec)
	if err != nil {
		if state == "" {
			state, err = serviceStatusWithManager(ctx, manager, spec.Service)
			if err != nil {
				return result, err
			}
		}
		if state == "inactive" || state == "failed" {
			return result, nil
		}
		return result, errors.New("cannot verify shared process membership")
	}
	parts := strings.Split(epoch, ":")
	if len(parts) != 3 {
		return result, errors.New("invalid shared process identity")
	}
	membership, err := os.ReadFile("/proc/" + parts[1] + "/cgroup")
	if err != nil {
		return result, err
	}
	for _, line := range strings.Split(string(membership), "\n") {
		path, ok := strings.CutPrefix(line, "0::/")
		if ok && (path == group || strings.HasPrefix(path, group+"/")) {
			return result, nil
		}
	}
	return result, errors.New("running shared core must restart in its accounting cgroup")
}

// All listeners of one instance carry the same cumulative target sample.
// Keeping the epoch with the nft object permits checkpoint loss recovery.
type processCheckpoint struct {
	core.SharedProcessUsage
	Raw        uint64 `json:"raw"`
	Handle     uint64 `json:"handle"`
	GroupInode uint64 `json:"-"`
}

func processCounterPrefix(r *trafficRecord) string {
	return "qch_proc_" + strings.ReplaceAll(string(r.Policy.Engine), "-", "") + "_" + r.Policy.SharedQuota.ID[4:] + "_"
}
func validProcessCounter(name string) bool {
	if !strings.HasPrefix(name, "qch_proc_") {
		return false
	}
	parts := strings.Split(name, "_")
	if len(parts) != 5 || parts[0] != "qch" || parts[1] != "proc" || !core.ValidAgentShareID("shr_"+parts[3]) || !core.ValidTrafficCounterEpoch(parts[4]) {
		return false
	}
	for _, engine := range core.AllEngines() {
		if strings.ReplaceAll(string(engine), "-", "") == parts[2] {
			return true
		}
	}
	return false
}

func sampleProcessCounter(r *trafficRecord, previous processCheckpoint, counters map[string]uint64, inode uint64) (processCheckpoint, error) {
	prefix := processCounterPrefix(r)
	if _, exists := counters[prefix+previous.Epoch]; !exists {
		// The kernel can hold a newer epoch than a restored disk checkpoint.
		for name := range counters {
			if strings.HasPrefix(name, prefix) && validProcessCounter(name) {
				previous = processCheckpoint{SharedProcessUsage: core.SharedProcessUsage{Epoch: strings.TrimPrefix(name, prefix)}}
				break
			}
		}
	}
	if previous.Epoch == "" {
		if ack := r.Policy.SharedQuota.ProcessAck; ack != nil {
			previous.SharedProcessUsage = *ack
		} else {
			epoch, err := randomSuffix(16)
			if err != nil {
				return previous, err
			}
			previous.Epoch = epoch
		}
	}
	previous.GroupInode = inode
	name := prefix + previous.Epoch
	value, exists := counters[name]
	if !exists {
		// Seed the replacement object at the durable high water. Keeping the
		// full lifetime inside nft also makes a later lost checkpoint recoverable.
		if ack := r.Policy.SharedQuota.ProcessAck; ack != nil && ack.Epoch == previous.Epoch {
			previous.Bytes = max(previous.Bytes, ack.Bytes)
		}
		previous.Raw, previous.Handle = previous.Bytes, 0
		return previous, nil
	}
	handle := counters["handle:"+name]
	baseline := previous.Raw
	if previous.Handle != 0 && previous.Handle != handle {
		baseline = 0
	}
	previous.Bytes = saturatedTrafficAdd(previous.Bytes, counterDelta(value, baseline))
	previous.Raw, previous.Handle, previous.GroupInode = value, handle, inode
	return previous, nil
}

func processConnectionMark(r *trafficRecord) uint32 {
	sum := sha256.Sum256([]byte(processCounterPrefix(r) + r.Process.Epoch))
	return binary.BigEndian.Uint32(sum[:4])&0x3fffffff | 0x40000000
}

// Install these rules first. A packet on a process-owned target connection
// must never reach a legacy socket-mark rule, even for a retained marked config.
func renderProcessRules(script *strings.Builder, records map[string]*trafficRecord, counters map[string]uint64, active map[string]bool) {
	for _, id := range sortedTrafficRecordIDs(records) {
		r := records[id]
		if r.Process.GroupInode == 0 {
			continue
		}
		name := processCounterPrefix(r) + r.Process.Epoch
		if active[name] {
			continue
		}
		active[name] = true
		if _, ok := counters[name]; !ok {
			fmt.Fprintf(script, "add counter inet %s %s { packets 0 bytes %d; }\n", trafficTableName, name, r.Process.Bytes)
		}
		group := strconv.Quote(processSliceName(r.Policy.Engine, r.Policy.SharedQuota.ID))
		mark := processConnectionMark(r)
		for _, chain := range []string{"input", "output"} {
			match := fmt.Sprintf("ct direction reply ct mark %d", mark)
			if chain == "output" {
				match = fmt.Sprintf("ct direction original socket cgroupv2 level 1 %s ct mark set %d", group, mark)
			}
			if r.Blocked {
				fmt.Fprintf(script, "add rule inet %s %s %s drop comment %q\n", trafficTableName, chain, match, "qch:block:"+name)
			}
			fmt.Fprintf(script, "add rule inet %s %s %s counter name %s return comment %q\n", trafficTableName, chain, match, name, "qch:process:"+name)
		}
	}
}

func (e *Executor) sharedProcessPorts(ctx context.Context, engine core.Engine, share string) (nativeAccountingSnapshot, error) {
	lock := e.sharedAccountingLock(engine, share)
	if !lock.TryRLock() {
		return nativeAccountingSnapshot{}, errSharedAccountingTransition
	}
	defer lock.RUnlock()
	manager := e.serviceManager()
	spec, err := sharedInstanceSpec(engine, DefaultSpecsForServiceManager(manager.Kind())[engine], share, manager.Kind())
	if err != nil {
		return nativeAccountingSnapshot{}, err
	}
	content, err := readConfigurationFile(spec.ConfigPath)
	if err != nil {
		return nativeAccountingSnapshot{}, err
	}
	state, err := readSharedDeploymentState(spec)
	if err != nil {
		return nativeAccountingSnapshot{}, err
	}
	if state.Applying || state.Digest != configurationDigest(content) {
		return nativeAccountingSnapshot{}, errors.New("cannot verify deployed listener set")
	}
	plan, err := serverconfig.SharedProcessPlan(engine, content)
	return nativeAccountingSnapshot{Plan: plan}, err
}

// Rules have been replaced and revoked services stopped before reclaiming an
// instance's persistent slice. Other engines of the same share retain theirs.
func (m *TrafficManager) releaseUnusedProcessGroups(ctx context.Context, previous map[string]*trafficRecord) error {
	if m.releaseProcess == nil {
		return nil
	}
	retained := map[string]bool{}
	for _, r := range m.records {
		if q := r.Policy.SharedQuota; q != nil && q.ProcessMetering {
			retained[processSliceName(r.Policy.Engine, q.ID)] = true
		}
	}
	var errs []error
	for _, r := range previous {
		q := r.Policy.SharedQuota
		if q == nil || !q.ProcessMetering {
			continue
		}
		key := processSliceName(r.Policy.Engine, q.ID)
		if !retained[key] {
			retained[key] = true
			errs = append(errs, m.releaseProcess(ctx, r.Policy.Engine, q.ID))
		}
	}
	return errors.Join(errs...)
}

func (e *Executor) releaseProcessGroup(ctx context.Context, engine core.Engine, share string) error {
	if !engine.Valid() || !core.ValidAgentShareID(share) {
		return errors.New("invalid process group identity")
	}
	group := processSliceName(engine, share)
	path := filepath.Join("/sys/fs/cgroup", group)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if manager := e.serviceManager(); manager.Kind() == ServiceManagerSystemd {
		_, err := run(ctx, manager.executable, "stop", group)
		return err
	}
	return os.Remove(path)
}
