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
	Policy             core.PortTrafficPolicy `json:"policy"`
	ReceivedBytes      uint64                 `json:"received_bytes"`
	SentBytes          uint64                 `json:"sent_bytes"`
	LastKernelReceived uint64                 `json:"last_kernel_received"`
	LastKernelSent     uint64                 `json:"last_kernel_sent"`
	PeriodStart        time.Time              `json:"period_start"`
	PeriodEnd          time.Time              `json:"period_end"`
	Blocked            bool                   `json:"blocked"`
	ReceiveBPS         uint64                 `json:"-"`
	SendBPS            uint64                 `json:"-"`
	LastCollectedAt    time.Time              `json:"-"`
}

type trafficState struct {
	Records map[string]*trafficRecord `json:"records"`
}

type TrafficManager struct {
	mu          sync.Mutex
	statePath   string
	backend     trafficCounterBackend
	records     map[string]*trafficRecord
	snapshot    []core.PortTrafficUsage
	lastSavedAt time.Time
	now         func() time.Time
}

func NewTrafficManager(agentStatePath string) *TrafficManager {
	return NewTrafficManagerForServiceManager(agentStatePath, defaultSystemdServiceManager())
}

func NewTrafficManagerForServiceManager(agentStatePath string, serviceManager *ServiceManager) *TrafficManager {
	manager := &TrafficManager{
		statePath: filepath.Join(filepath.Dir(agentStatePath), "traffic-state.json"),
		backend:   newNFTBackendForServiceManager(serviceManager), records: make(map[string]*trafficRecord), now: time.Now,
	}
	state, err := loadTrafficState(manager.statePath)
	if err == nil {
		if err := validateLoadedTrafficState(state, manager.now().UTC()); err == nil {
			manager.records = state.Records
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

func (manager *TrafficManager) Start(ctx context.Context) {
	if manager == nil {
		return
	}
	go func() {
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
				return
			case <-ticker.C:
				manager.collect(ctx, false)
			}
		}
	}()
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
		if err != nil || policy.ResetGeneration == 0 {
			return fmt.Errorf("control plane returned an invalid traffic policy: %w", err)
		}
		policy.Name, policy.CycleAnchor = normalized.Name, normalized.CycleAnchor
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	_ = manager.collectLocked(ctx, false)
	next := make(map[string]*trafficRecord, len(policies))
	for _, policy := range policies {
		record := manager.records[policy.ID]
		if record == nil || !sameTrafficCounter(record.Policy, policy) {
			record = &trafficRecord{}
		}
		record.Policy = policy
		next[policy.ID] = record
	}
	manager.records = next
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
	if len(manager.records) == 0 && !forceRules {
		manager.snapshot = nil
		return nil
	}
	if manager.backend == nil {
		return manager.setUnavailableLocked(errors.New("nftables backend is unavailable"))
	}
	counters, tableExists, err := manager.backend.Counters(ctx)
	if err != nil {
		return manager.setUnavailableLocked(err)
	}
	now := manager.now().UTC()
	stateChanged := false
	if tableExists && !trafficRuleSetComplete(counters, manager.records) {
		forceRules = true
	}
	for _, id := range sortedTrafficRecordIDs(manager.records) {
		record := manager.records[id]
		periodStart, periodEnd, periodErr := core.TrafficPeriodAt(record.Policy.CycleAnchor, record.Policy.Cycle, now)
		if periodErr != nil {
			return manager.setUnavailableLocked(periodErr)
		}
		kernelReceived, kernelSent := policyKernelCounters(counters, record.Policy)
		if !record.PeriodStart.Equal(periodStart) || !record.PeriodEnd.Equal(periodEnd) {
			record.ReceivedBytes, record.SentBytes = 0, 0
			record.LastKernelReceived, record.LastKernelSent = kernelReceived, kernelSent
			record.ReceiveBPS, record.SendBPS = 0, 0
			record.PeriodStart, record.PeriodEnd = periodStart, periodEnd
			record.LastCollectedAt = now
			if record.Blocked {
				record.Blocked = false
				forceRules = true
			}
			stateChanged = true
		} else {
			receivedDelta := counterDelta(kernelReceived, record.LastKernelReceived)
			sentDelta := counterDelta(kernelSent, record.LastKernelSent)
			// Once blocked, nftables counters observe rejected attempts. Keep
			// those bytes out of billed usage while retaining a fresh baseline.
			if record.Blocked {
				receivedDelta, sentDelta = 0, 0
			}
			record.ReceivedBytes = saturatedTrafficAdd(record.ReceivedBytes, receivedDelta)
			record.SentBytes = saturatedTrafficAdd(record.SentBytes, sentDelta)
			if !record.LastCollectedAt.IsZero() && now.After(record.LastCollectedAt) {
				seconds := now.Sub(record.LastCollectedAt).Seconds()
				record.ReceiveBPS = trafficRate(receivedDelta, seconds)
				record.SendBPS = trafficRate(sentDelta, seconds)
			}
			record.LastKernelReceived, record.LastKernelSent = kernelReceived, kernelSent
			record.LastCollectedAt = now
			stateChanged = stateChanged || receivedDelta > 0 || sentDelta > 0
		}
		blocked := record.Policy.AutoBlock && trafficUsed(record) >= record.Policy.LimitBytes
		if blocked != record.Blocked {
			record.Blocked = blocked
			forceRules = true
			stateChanged = true
		}
	}

	if forceRules || (len(manager.records) > 0 && !tableExists) || (len(manager.records) == 0 && tableExists) {
		script := renderTrafficRules(manager.records, tableExists)
		if err := manager.backend.Replace(ctx, script); err != nil {
			return manager.setUnavailableLocked(err)
		}
		for _, record := range manager.records {
			record.LastKernelReceived, record.LastKernelSent = 0, 0
		}
		stateChanged = true
		manager.lastSavedAt = time.Time{}
	}
	manager.refreshSnapshotLocked(true, "")
	if stateChanged && (manager.lastSavedAt.IsZero() || now.Sub(manager.lastSavedAt) >= 30*time.Second || forceRules) {
		if err := saveTrafficState(manager.statePath, trafficState{Records: manager.records}); err != nil {
			manager.refreshSnapshotLocked(false, "persist traffic counters: "+err.Error())
			return err
		}
		manager.lastSavedAt = now
	}
	return nil
}

func (manager *TrafficManager) setUnavailableLocked(err error) error {
	message := strings.TrimSpace(err.Error())
	if len(message) > 500 {
		message = message[:500]
	}
	now := manager.now().UTC()
	for _, record := range manager.records {
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
		result = append(result, core.PortTrafficUsage{
			PolicyID: id, ResetGeneration: record.Policy.ResetGeneration,
			ReceivedBytes: record.ReceivedBytes, SentBytes: record.SentBytes,
			UsedBytes: trafficUsed(record), ReceiveBPS: record.ReceiveBPS, SendBPS: record.SendBPS,
			PeriodStart: record.PeriodStart, PeriodEnd: record.PeriodEnd, Blocked: record.Blocked,
			EnforcementAvailable: available, EnforcementError: message,
		})
	}
	manager.snapshot = result
}

func sameTrafficCounter(left, right core.PortTrafficPolicy) bool {
	return left.Port == right.Port && left.Protocol == right.Protocol && left.Cycle == right.Cycle &&
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
		// input+dport is client-to-listener upload; output+sport is
		// listener-to-client download. Keep the API field names listener-centric.
		received = saturatedTrafficAdd(received, counters[trafficRuleComment(policy.ID, "in", protocol)])
		sent = saturatedTrafficAdd(sent, counters[trafficRuleComment(policy.ID, "out", protocol)])
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
	for id, record := range records {
		for _, protocol := range trafficProtocols(record.Policy.Protocol) {
			if _, exists := counters[trafficRuleComment(id, "in", protocol)]; !exists {
				return false
			}
			if _, exists := counters[trafficRuleComment(id, "out", protocol)]; !exists {
				return false
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

func renderTrafficRules(records map[string]*trafficRecord, tableExists bool) string {
	var script strings.Builder
	if tableExists {
		script.WriteString("delete table inet " + trafficTableName + "\n")
	}
	if len(records) == 0 {
		return script.String()
	}
	script.WriteString("add table inet " + trafficTableName + "\n")
	script.WriteString("add chain inet " + trafficTableName + " input { type filter hook input priority -10; policy accept; }\n")
	script.WriteString("add chain inet " + trafficTableName + " output { type filter hook output priority -10; policy accept; }\n")
	for _, id := range sortedTrafficRecordIDs(records) {
		record := records[id]
		for _, protocol := range trafficProtocols(record.Policy.Protocol) {
			verdict := ""
			if record.Blocked {
				verdict = " drop"
			}
			fmt.Fprintf(&script, "add rule inet %s input meta l4proto %s %s dport %d counter%s comment %s\n",
				trafficTableName, protocol, protocol, record.Policy.Port, verdict,
				strconv.Quote(trafficRuleComment(id, "in", protocol)))
			fmt.Fprintf(&script, "add rule inet %s output meta l4proto %s %s sport %d counter%s comment %s\n",
				trafficTableName, protocol, protocol, record.Policy.Port, verdict,
				strconv.Quote(trafficRuleComment(id, "out", protocol)))
		}
	}
	return script.String()
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
			Rule *struct {
				Comment string `json:"comment"`
				Expr    []struct {
					Counter *struct {
						Bytes uint64 `json:"bytes"`
					} `json:"counter"`
				} `json:"expr"`
			} `json:"rule"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		return nil, fmt.Errorf("parse nftables traffic counters: %w", err)
	}
	result := make(map[string]uint64)
	for _, item := range document.NFTables {
		if item.Rule == nil || !strings.HasPrefix(item.Rule.Comment, "qch:trf_") {
			continue
		}
		for _, expression := range item.Rule.Expr {
			if expression.Counter != nil {
				result[item.Rule.Comment] = saturatedTrafficAdd(result[item.Rule.Comment], expression.Counter.Bytes)
			}
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
		if record.ReceivedBytes > math.MaxInt64 || record.SentBytes > math.MaxInt64 ||
			record.LastKernelReceived > math.MaxInt64 || record.LastKernelSent > math.MaxInt64 {
			return errors.New("traffic state contains an out-of-range counter")
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
