package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

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
	configuration, err := e.accountingConfiguration(engine, content)
	if err != nil {
		return result, err
	}
	epoch, err := e.accountingProcessEpoch(ctx, spec)
	if err != nil {
		return result, err
	}
	configuration.ProcessEpoch = epoch
	if configuration.Plan.Source == "nft-dual" {
		return configuration, nil
	}
	counters, err := queryNativeTrafficAt(ctx, engine, configuration.Plan.API)
	if err != nil {
		return result, err
	}
	// A process swap during the request must not attribute the old response to
	// the new generation. Retry on the next sample without advancing baselines.
	after, err := e.accountingProcessEpoch(ctx, spec)
	if err != nil || after != epoch {
		return result, errors.New("core restarted during statistics collection")
	}
	configuration.Counters = counters
	return configuration, nil
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
	// Lazy counters may be absent before their first use. Once a nonzero
	// baseline exists, absence in the same process is not evidence of reset.
	// Validate all legs before mutating any baseline so recovery cannot rebill
	// an omitted counter's old lifetime or consume the other legs prematurely.
	if !first && record.Accounting.ProcessEpoch == snapshot.ProcessEpoch {
		for _, key := range keys {
			if _, present := snapshot.Counters[key]; !present && record.Accounting.Counters[key] != 0 {
				return 0, 0, errors.New("incomplete native statistics: previously active counter is missing")
			}
		}
	}
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
