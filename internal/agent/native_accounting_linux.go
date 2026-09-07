package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"gopkg.in/yaml.v3"
)

type nativeAccountingSnapshot struct {
	Plan         serverconfig.AccountingPlan
	Counters     map[string]uint64
	ProcessEpoch string
}

func (e *Executor) prepareNativeAccountingContent(ctx context.Context, engine core.Engine, spec EngineSpec, content string) (string, string) {
	if spec != DefaultSpecsForServiceManager(e.serviceManager().Kind())[engine] {
		return content, ""
	}
	_, compilationErr := serverconfig.PrepareAccounting(engine, content)
	if compilationErr != nil && engine == core.EngineSingBox {
		_, compilationErr = serverconfig.PrepareMarkedSingBoxAccounting(content)
	}
	if compilationErr != nil && strings.Contains(content, "qch-trf-") {
		previous, err := readConfigurationFile(spec.ConfigPath)
		if err != nil {
			return content, "accounting update rejected: " + err.Error()
		}
		source, err := serverconfig.AccountingUpdateSource(engine, content, previous)
		if err != nil {
			return content, "accounting update rejected: " + err.Error()
		}
		content = source
	}
	if engine == core.EngineSingBox {
		if err := validatePrivilegedExecutable(spec.Binary); err != nil {
			return content, "native accounting unavailable: " + err.Error()
		}
		version, err := run(ctx, spec.Binary, "version")
		if err != nil || !strings.Contains(version, "with_v2ray_api") {
			plan, planErr := serverconfig.PrepareMarkedSingBoxAccounting(content)
			if planErr != nil {
				return content, "dual accounting unavailable: " + planErr.Error()
			}
			if err := checkOutboundMarkRouting(ctx); err != nil {
				return content, "dual accounting unavailable: " + err.Error()
			}
			return plan.Content, ""
		}
	}
	plan, err := serverconfig.PrepareAccounting(engine, content)
	if err != nil {
		return content, "native accounting unavailable: " + err.Error()
	}
	if plan.Source == "nft-dual" {
		if err := checkOutboundMarkRouting(ctx); err != nil {
			return content, "dual accounting unavailable: " + err.Error()
		}
	}
	return plan.Content, ""
}

// A new socket mark must not silently select an administrator's fwmark-based
// routing table. Complex policy routing is intentionally an explicit review.
func checkOutboundMarkRouting(ctx context.Context) error {
	var binary string
	for _, candidate := range []string{"/usr/sbin/ip", "/usr/bin/ip", "/sbin/ip", "/bin/ip"} {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil && validatePrivilegedExecutable(resolved) == nil {
			binary = resolved
			break
		}
	}
	if binary == "" {
		return errors.New("iproute2 is required to verify outbound mark routing")
	}
	for _, family := range []string{"-4", "-6"} {
		output, err := run(ctx, binary, family, "rule", "show")
		if err != nil {
			return errors.New("cannot verify policy routing before applying outbound marks")
		}
		if strings.Contains(output, "fwmark") {
			return errors.New("existing fwmark policy routing requires manual review before enabling outbound accounting")
		}
	}
	return nil
}

func (e *Executor) migrateNativeAccounting(ctx context.Context) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		e.specsMu.RLock()
		spec, ok := e.Specs[engine]
		issue := e.ExistingDiscoveryIssues[engine]
		_, pendingImport := e.ExistingSpecs[engine]
		e.specsMu.RUnlock()
		if !ok || pendingImport || issue != "" || spec != DefaultSpecsForServiceManager(e.serviceManager().Kind())[engine] {
			continue
		}
		migration, cancel := context.WithTimeout(ctx, 45*time.Second)
		func() {
			defer cancel()
			if e.serviceManager().Kind() == ServiceManagerOpenRC {
				current, err := os.ReadFile(filepath.Join(openRCInitRoot, spec.Service))
				if err != nil {
					return
				}
				wanted, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/openrc/"+spec.Service)
				if err != nil {
					return
				}
				legacy := bytes.ReplaceAll(wanted, []byte("^cap_net_bind_service,^cap_net_admin"), []byte("^cap_net_bind_service"))
				if !bytes.Equal(current, wanted) && !bytes.Equal(current, legacy) {
					slog.Warn("managed traffic migration refused customized OpenRC script", "engine", engine)
					return
				}
			}
			if _, err := validateManagedServiceForExistingDiscovery(migration, engine, spec, e.serviceManager()); err != nil {
				slog.Warn("managed traffic migration refused unrecognized service", "engine", engine, "error", err)
				return
			}
			status, err := serviceStatusWithManager(migration, e.serviceManager(), spec.Service)
			if err != nil || strings.TrimSpace(status) != "active" {
				return
			}
			content, err := readConfigurationFile(spec.ConfigPath)
			if err != nil {
				return
			}
			prepared, warning := e.prepareNativeAccountingContent(migration, engine, spec, content)
			if warning != "" {
				slog.Warn("managed traffic migration deferred", "engine", engine, "reason", warning)
				return
			}
			if prepared == content {
				return
			}
			// Execute validates before activation, backs up the fixed config and
			// restores/restarts the previous revision if the service fails.
			if _, err := e.Execute(migration, core.Task{Engine: engine, Action: core.ActionDeploy, ConfigContent: prepared}); err != nil {
				slog.Warn("managed traffic migration failed", "engine", engine, "error", err)
				return
			}
			slog.Info("managed traffic migration completed", "engine", engine)
		}()
	}
}

func (e *Executor) nativeAccounting(ctx context.Context, engine core.Engine) (nativeAccountingSnapshot, error) {
	var result nativeAccountingSnapshot
	e.specsMu.RLock()
	spec, ok := e.Specs[engine]
	issue := e.ExistingDiscoveryIssues[engine]
	e.specsMu.RUnlock()
	defaultSpec := DefaultSpecsForServiceManager(e.serviceManager().Kind())[engine]
	if !ok || spec != defaultSpec || issue != "" {
		return result, errors.New("dual accounting requires a verified managed core")
	}
	content, err := readConfigurationFile(spec.ConfigPath)
	if err != nil {
		return result, err
	}
	plan, err := serverconfig.PrepareAccounting(engine, content)
	if engine == core.EngineSingBox && (err != nil || !strings.Contains(content, `"v2ray_api"`)) {
		plan, err = serverconfig.PrepareMarkedSingBoxAccounting(content)
	}
	if err != nil {
		return result, err
	}
	var actual, compiled any
	decode := func(content string, dest *any) error {
		if engine == core.EngineMihomo {
			return yaml.Unmarshal([]byte(content), dest)
		}
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		return decoder.Decode(dest)
	}
	if decode(content, &actual) != nil || decode(plan.Content, &compiled) != nil || !reflect.DeepEqual(actual, compiled) {
		return result, errors.New("managed configuration has not migrated to independent outbound accounting")
	}
	epoch, err := e.accountingProcessEpoch(ctx, spec)
	if err != nil {
		return result, err
	}
	if plan.Source == "nft-dual" {
		return nativeAccountingSnapshot{Plan: plan, ProcessEpoch: epoch}, nil
	}
	counters, err := queryNativeTraffic(ctx, engine)
	if err != nil {
		return result, err
	}
	// A process swap during the request must not attribute the old response to
	// the new generation. Retry on the next sample without advancing baselines.
	after, err := e.accountingProcessEpoch(ctx, spec)
	if err != nil || after != epoch {
		return result, errors.New("core restarted during statistics collection")
	}
	return nativeAccountingSnapshot{Plan: plan, Counters: counters, ProcessEpoch: epoch}, nil
}

func (e *Executor) accountingProcessEpoch(ctx context.Context, spec EngineSpec) (string, error) {
	if e.serviceManager().Kind() == ServiceManagerOpenRC {
		return managedCoreProcessEpoch(spec)
	}
	output, err := run(ctx, e.serviceManager().executable, "show", spec.Service, "--property=MainPID", "--property=ExecMainStartTimestampMonotonic", "--property=ActiveState", "--no-pager")
	if err != nil {
		return "", err
	}
	properties := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			properties[key] = strings.TrimSpace(value)
		}
	}
	pid, pidErr := strconv.ParseUint(properties["MainPID"], 10, 32)
	started, startErr := strconv.ParseUint(properties["ExecMainStartTimestampMonotonic"], 10, 64)
	if pidErr != nil || startErr != nil || pid == 0 || started == 0 || properties["ActiveState"] != "active" {
		return "", errors.New("managed accounting service is not active")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d:%d", strings.TrimSpace(string(boot)), pid, started), nil
}

func managedCoreProcessEpoch(spec EngineSpec) (string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "", err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	var found string
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		base := filepath.Join("/proc", entry.Name())
		cmd, err := os.ReadFile(filepath.Join(base, "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(string(cmd), "\x00")
		if len(args) == 0 || args[0] != spec.Binary {
			continue
		}
		// A root Agent with a restricted capability set cannot necessarily
		// dereference a non-root process's exe link. cmdline/stat remain
		// readable on OpenRC; require the exact fixed executable and config.
		if binary, err := os.Readlink(filepath.Join(base, "exe")); err == nil && binary != spec.Binary {
			continue
		}
		matches := false
		for _, arg := range args {
			if arg == spec.ConfigPath {
				matches = true
			}
		}
		if !matches {
			continue
		}
		stat, err := os.ReadFile(filepath.Join(base, "stat"))
		if err != nil {
			continue
		}
		end := strings.LastIndexByte(string(stat), ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(stat[end+1:]))
		if len(fields) < 20 {
			continue
		}
		if found != "" {
			return "", errors.New("multiple managed processes share one accounting configuration")
		}
		found = strings.TrimSpace(string(boot)) + ":" + entry.Name() + ":" + fields[19]
	}
	if found == "" {
		return "", errors.New("managed accounting process is not running")
	}
	return found, nil
}

func collectNativeAccounting(record *trafficRecord, snapshot nativeAccountingSnapshot) (uint64, uint64, error) {
	var port *serverconfig.AccountingPort
	for i := range snapshot.Plan.Ports {
		if snapshot.Plan.Ports[i].Port == record.Policy.Port {
			port = &snapshot.Plan.Ports[i]
			break
		}
	}
	if port == nil {
		return 0, 0, errors.New("listener has no independent outbound mapping")
	}
	keys := []string{"inbound>>>" + port.Inbound + ">>>traffic>>>uplink", "inbound>>>" + port.Inbound + ">>>traffic>>>downlink"}
	for _, tag := range port.Outbounds {
		keys = append(keys, "outbound>>>"+tag+">>>traffic>>>downlink", "outbound>>>"+tag+">>>traffic>>>uplink")
	}
	if len(keys) > 260 {
		return 0, 0, errors.New("too many accounting outbounds")
	}
	first := record.Accounting == nil || record.Accounting.Source != "core-api" || record.Accounting.Inbound != port.Inbound || !reflect.DeepEqual(record.Accounting.Outbounds, port.Outbounds)
	if first {
		epoch, err := randomSuffix(16)
		if err != nil {
			return 0, 0, err
		}
		record.QuotaBaselineBytes = saturatedTrafficAdd(record.QuotaBaselineBytes, trafficUsed(record))
		record.CounterEpoch = epoch
		record.KernelCounters = map[string]uint64{}
		record.ReceivedBytes, record.SentBytes, record.LifetimeReceivedBytes, record.LifetimeSentBytes = 0, 0, 0, 0
		record.Accounting = &core.TrafficAccounting{Source: "core-api", Inbound: port.Inbound, Outbounds: append([]string(nil), port.Outbounds...), Counters: map[string]uint64{}}
	}
	accounting := record.Accounting
	if accounting.Counters == nil {
		accounting.Counters = map[string]uint64{}
	}
	var received, sent uint64
	for i, key := range keys {
		value := snapshot.Counters[key]
		previous := accounting.Counters[key]
		if accounting.ProcessEpoch != snapshot.ProcessEpoch {
			previous = 0
		}
		delta := counterDelta(value, previous)
		if first {
			delta = 0
		} // Establish a new scope, never rebill a process's prior lifetime.
		accounting.Counters[key] = value
		switch {
		case i == 0:
			accounting.ClientReceived = saturatedTrafficAdd(accounting.ClientReceived, delta)
			received = saturatedTrafficAdd(received, delta)
		case i == 1:
			accounting.ClientSent = saturatedTrafficAdd(accounting.ClientSent, delta)
			sent = saturatedTrafficAdd(sent, delta)
		case i%2 == 0:
			accounting.TargetReceived = saturatedTrafficAdd(accounting.TargetReceived, delta)
			received = saturatedTrafficAdd(received, delta)
		default:
			accounting.TargetSent = saturatedTrafficAdd(accounting.TargetSent, delta)
			sent = saturatedTrafficAdd(sent, delta)
		}
	}
	accounting.ProcessEpoch = snapshot.ProcessEpoch
	if !accounting.Valid() {
		return 0, 0, fmt.Errorf("invalid native counter metadata")
	}
	return received, sent, nil
}
