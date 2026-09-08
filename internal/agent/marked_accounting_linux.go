package agent

import (
	"errors"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"slices"
)

func prepareMarkedAccounting(record *trafficRecord, snapshot nativeAccountingSnapshot) error {
	for _, port := range snapshot.Plan.Ports {
		if port.Port != record.Policy.Port {
			continue
		}
		if port.Mark != uint32(0x51430000)|uint32(port.Port) {
			return errors.New("invalid per-port outbound mark")
		}
		if record.Accounting != nil && record.Accounting.Source == "nft-dual" && record.Accounting.Mark == port.Mark && record.Accounting.Inbound == port.Inbound && slices.Equal(record.Accounting.Outbounds, port.Outbounds) {
			record.Accounting.ProcessEpoch = snapshot.ProcessEpoch
			return nil
		}
		epoch, err := randomSuffix(16)
		if err != nil {
			return err
		}
		record.QuotaBaselineBytes = saturatedTrafficAdd(record.QuotaBaselineBytes, trafficUsed(record))
		record.CounterEpoch = epoch
		record.KernelCounters = map[string]uint64{}
		record.ReceivedBytes, record.SentBytes, record.LifetimeReceivedBytes, record.LifetimeSentBytes = 0, 0, 0, 0
		record.Accounting = &core.TrafficAccounting{Source: "nft-dual", Mark: port.Mark, Inbound: port.Inbound, Outbounds: append([]string(nil), port.Outbounds...), ProcessEpoch: snapshot.ProcessEpoch}
		return nil
	}
	return errors.New("listener has no independent marked outbound")
}

func collectMarkedAccounting(counters map[string]uint64, record *trafficRecord, received, sent uint64, first bool) (uint64, uint64) {
	if first {
		return 0, 0
	}
	accounting := record.Accounting
	accounting.ClientReceived = saturatedTrafficAdd(accounting.ClientReceived, received)
	accounting.ClientSent = saturatedTrafficAdd(accounting.ClientSent, sent)
	for _, protocol := range []string{"tcp", "udp"} {
		for _, direction := range []string{"targetin", "targetout"} {
			key := trafficCounterName(record, direction, protocol)
			value, exists := counters[key]
			if !exists {
				delete(record.KernelCounters, key)
				delete(record.KernelCounters, "handle:"+key)
				continue
			}
			previous := record.KernelCounters[key]
			handle := counters["handle:"+key]
			if record.KernelCounters["handle:"+key] != handle {
				previous = 0
			}
			delta := counterDelta(value, previous)
			record.KernelCounters[key], record.KernelCounters["handle:"+key] = value, handle
			if direction == "targetin" {
				accounting.TargetReceived = saturatedTrafficAdd(accounting.TargetReceived, delta)
				received = saturatedTrafficAdd(received, delta)
			} else {
				accounting.TargetSent = saturatedTrafficAdd(accounting.TargetSent, delta)
				sent = saturatedTrafficAdd(sent, delta)
			}
		}
	}
	accounting.Counters = make(map[string]uint64, len(record.KernelCounters))
	for key, value := range record.KernelCounters {
		accounting.Counters[key] = value
	}
	return received, sent
}
