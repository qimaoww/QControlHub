package agent

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
