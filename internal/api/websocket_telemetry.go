package api

import (
	"maps"
	"slices"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	wsIdleControlReconcileInterval = 10 * time.Second
	wsInboundGlobalRatePerSec      = 40.0
	wsInboundGlobalBurst           = 120.0
	wsInboundTelemetryRatePerSec   = 2.5
	wsInboundTelemetryBurst        = 10.0
	wsMaxDroppedTelemetryFrames    = 25
	wsMaxUnchangedMetricsKeepalive = 5 * time.Second
)

type wsRateBucket struct {
	rate       float64
	burst      float64
	tokens     float64
	lastRefill time.Time
}

func newWSRateBucket(ratePerSecond, burst float64, now time.Time) wsRateBucket {
	return wsRateBucket{
		rate:       ratePerSecond,
		burst:      burst,
		tokens:     burst,
		lastRefill: now,
	}
}

func (b *wsRateBucket) allow(now time.Time) bool {
	if now.After(b.lastRefill) {
		b.tokens += now.Sub(b.lastRefill).Seconds() * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.lastRefill = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func buildAgentSessionPolicy(baseProbe core.PublicIPProbeConfig, settings core.PanelSettings) (core.AgentPolicy, core.PublicIPProbeConfig) {
	policy := settings.AgentPolicy()
	probe := baseProbe
	probe.IntervalSeconds = uint32(settings.PublicIPProbeIntervalSeconds)
	return policy, probe
}

func sameHTTPChallenges(a, b []core.HTTPChallenge) bool {
	if len(a) != len(b) {
		return false
	}
	return slices.EqualFunc(a, b, func(left, right core.HTTPChallenge) bool {
		return left.OrderID == right.OrderID &&
			left.Domain == right.Domain &&
			left.Token == right.Token &&
			left.Value == right.Value &&
			left.ExpiresAt.Equal(right.ExpiresAt)
	})
}

func sameHostMetricsSnapshot(prev, cur core.HostMetrics) bool {
	if prev.CollectedAt.IsZero() != cur.CollectedAt.IsZero() ||
		prev.CPUAvailable != cur.CPUAvailable ||
		prev.CPUPercent != cur.CPUPercent ||
		prev.MemoryAvailable != cur.MemoryAvailable ||
		prev.MemoryUsedBytes != cur.MemoryUsedBytes ||
		prev.MemoryTotalBytes != cur.MemoryTotalBytes ||
		prev.DiskAvailable != cur.DiskAvailable ||
		prev.DiskUsedBytes != cur.DiskUsedBytes ||
		prev.DiskTotalBytes != cur.DiskTotalBytes ||
		!sameBoolPtr(prev.NetworkRateAvailable, cur.NetworkRateAvailable) ||
		prev.NetworkAvailable != cur.NetworkAvailable ||
		prev.NetworkRXBytes != cur.NetworkRXBytes ||
		prev.NetworkTXBytes != cur.NetworkTXBytes ||
		prev.NetworkRXBPS != cur.NetworkRXBPS ||
		prev.NetworkTXBPS != cur.NetworkTXBPS ||
		prev.ObservedPublicIP != cur.ObservedPublicIP ||
		prev.PublicIPv4 != cur.PublicIPv4 ||
		prev.PublicIPv6 != cur.PublicIPv6 ||
		prev.PublicIPv4Source != cur.PublicIPv4Source ||
		prev.PublicIPv6Source != cur.PublicIPv6Source {
		return false
	}
	if cur.BBR != nil && !sameSystemBBRStatus(prev.BBR, cur.BBR) {
		return false
	}
	if cur.ClockSync != nil && !sameClockSyncStatus(prev.ClockSync, cur.ClockSync) {
		return false
	}
	if cur.NetworkInterfaces != nil && !sameHostNetworkInterfaces(prev.NetworkInterfaces, cur.NetworkInterfaces) {
		return false
	}
	return true
}

func mergeCompactHostMetrics(prev *core.HostMetrics, cur core.HostMetrics) *core.HostMetrics {
	merged := cur
	if prev != nil {
		if merged.BBR == nil && prev.BBR != nil {
			merged.BBR = prev.BBR
		}
		if merged.ClockSync == nil && prev.ClockSync != nil {
			merged.ClockSync = prev.ClockSync
		}
		if merged.NetworkInterfaces == nil && prev.NetworkInterfaces != nil {
			merged.NetworkInterfaces = prev.NetworkInterfaces
		}
	}
	if merged.NetworkInterfaces != nil {
		copied := make([]core.HostNetworkInterface, len(merged.NetworkInterfaces))
		for i, iface := range merged.NetworkInterfaces {
			copied[i] = core.HostNetworkInterface{
				Name:      iface.Name,
				Addresses: slices.Clone(iface.Addresses),
			}
		}
		merged.NetworkInterfaces = copied
	}
	return &merged
}

func sameBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func sameSystemBBRStatus(a, b *core.SystemBBRStatus) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.CollectedAt.Equal(b.CollectedAt) &&
		a.Available == b.Available &&
		a.KernelRelease == b.KernelRelease &&
		a.CongestionControl == b.CongestionControl &&
		slices.Equal(a.AvailableAlgorithms, b.AvailableAlgorithms) &&
		a.DefaultQdisc == b.DefaultQdisc &&
		sameStringMap(a.Parameters, b.Parameters) &&
		slices.Equal(a.Qdiscs, b.Qdiscs) &&
		a.QdiscError == b.QdiscError &&
		a.Persistence == b.Persistence &&
		a.ConfiguredAlgorithm == b.ConfiguredAlgorithm &&
		a.ConfiguredQdisc == b.ConfiguredQdisc &&
		sameStringMap(a.ConfiguredParameters, b.ConfiguredParameters) &&
		a.Error == b.Error
}

func sameClockSyncStatus(a, b *core.ClockSyncStatus) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Status != b.Status || !a.CheckedAt.Equal(b.CheckedAt) {
		return false
	}
	if (a.OffsetMilliseconds == nil) != (b.OffsetMilliseconds == nil) {
		return false
	}
	return a.OffsetMilliseconds == nil || *a.OffsetMilliseconds == *b.OffsetMilliseconds
}

func sameHostNetworkInterfaces(a, b []core.HostNetworkInterface) bool {
	if len(a) != len(b) {
		return false
	}
	return slices.EqualFunc(a, b, func(left, right core.HostNetworkInterface) bool {
		return left.Name == right.Name && slices.Equal(left.Addresses, right.Addresses)
	})
}

func samePortTrafficUsageSlice(a, b []core.PortTrafficUsage) bool {
	if len(a) != len(b) {
		return false
	}
	return slices.EqualFunc(a, b, func(left, right core.PortTrafficUsage) bool {
		if left.ShareID != right.ShareID ||
			left.ShareUsedBytes != right.ShareUsedBytes ||
			!left.CollectedAt.Equal(right.CollectedAt) ||
			left.CounterEpoch != right.CounterEpoch ||
			left.LifetimeReceivedBytes != right.LifetimeReceivedBytes ||
			left.LifetimeSentBytes != right.LifetimeSentBytes ||
			left.PolicyID != right.PolicyID ||
			left.ResetGeneration != right.ResetGeneration ||
			left.ReceivedBytes != right.ReceivedBytes ||
			left.SentBytes != right.SentBytes ||
			left.UsedBytes != right.UsedBytes ||
			left.ReceiveBPS != right.ReceiveBPS ||
			left.SendBPS != right.SendBPS ||
			!left.PeriodStart.Equal(right.PeriodStart) ||
			!left.PeriodEnd.Equal(right.PeriodEnd) ||
			left.Blocked != right.Blocked ||
			left.EnforcementAvailable != right.EnforcementAvailable ||
			left.EnforcementError != right.EnforcementError {
			return false
		}
		return sameTrafficAccounting(left.Accounting, right.Accounting)
	})
}

func sameTrafficAccounting(a, b *core.TrafficAccounting) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Source == b.Source &&
		a.Inbound == b.Inbound &&
		slices.Equal(a.Outbounds, b.Outbounds) &&
		a.Mark == b.Mark &&
		a.ProcessEpoch == b.ProcessEpoch &&
		a.ClientReceived == b.ClientReceived &&
		a.ClientSent == b.ClientSent &&
		a.TargetReceived == b.TargetReceived &&
		a.TargetSent == b.TargetSent &&
		maps.Equal(a.Counters, b.Counters)
}

func clonePortTrafficUsageSlice(items []core.PortTrafficUsage) []core.PortTrafficUsage {
	if len(items) == 0 {
		return nil
	}
	cloned := slices.Clone(items)
	for i := range cloned {
		if cloned[i].Accounting != nil {
			acc := *cloned[i].Accounting
			acc.Outbounds = slices.Clone(acc.Outbounds)
			acc.Counters = maps.Clone(acc.Counters)
			cloned[i].Accounting = &acc
		}
	}
	return cloned
}
