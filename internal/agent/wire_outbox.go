package agent

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const wireOutboxLogQueueLimit = 4

// wireOutbox serializes outbound session frames with strict control priority
// and latest-value coalescing for periodic heartbeats and host metrics.
type wireOutbox struct {
	highCh chan core.WireMessage
	notify chan struct{}

	mu                  sync.Mutex
	pendingHeartbeat    *core.WireMessage
	logs                []core.WireMessage
	pendingMetric       *core.WireMessage
	coalescedHeartbeats uint64
	coalescedMetrics    uint64
}

func newWireOutbox(highBuffer int) *wireOutbox {
	if highBuffer <= 0 {
		highBuffer = 8
	}
	return &wireOutbox{
		highCh: make(chan core.WireMessage, highBuffer),
		notify: make(chan struct{}, 1),
	}
}

func (o *wireOutbox) High() chan<- core.WireMessage {
	return o.highCh
}

func (o *wireOutbox) EnqueueHigh(ctx context.Context, msg core.WireMessage) error {
	select {
	case o.highCh <- msg:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (o *wireOutbox) signal() {
	select {
	case o.notify <- struct{}{}:
	default:
	}
}

func (o *wireOutbox) EnqueueHeartbeat(msg core.WireMessage) {
	o.mu.Lock()
	if o.pendingHeartbeat != nil {
		o.coalescedHeartbeats++
	}
	copyMsg := msg
	o.pendingHeartbeat = &copyMsg
	o.mu.Unlock()
	o.signal()
}

func (o *wireOutbox) TryEnqueueLogs(msg core.WireMessage) bool {
	o.mu.Lock()
	if len(o.logs) >= wireOutboxLogQueueLimit {
		o.mu.Unlock()
		return false
	}
	o.logs = append(o.logs, msg)
	o.mu.Unlock()
	o.signal()
	return true
}

func (o *wireOutbox) EnqueueMetric(msg core.WireMessage) {
	o.mu.Lock()
	if o.pendingMetric != nil {
		o.coalescedMetrics++
	}
	copyMsg := msg
	o.pendingMetric = &copyMsg
	o.mu.Unlock()
	o.signal()
}

func (o *wireOutbox) popLocked() (core.WireMessage, bool) {
	if o.pendingHeartbeat != nil {
		msg := *o.pendingHeartbeat
		o.pendingHeartbeat = nil
		return msg, true
	}
	if len(o.logs) > 0 {
		msg := o.logs[0]
		copy(o.logs, o.logs[1:])
		o.logs[len(o.logs)-1] = core.WireMessage{}
		o.logs = o.logs[:len(o.logs)-1]
		return msg, true
	}
	if o.pendingMetric != nil {
		msg := *o.pendingMetric
		o.pendingMetric = nil
		return msg, true
	}
	return core.WireMessage{}, false
}

func (o *wireOutbox) Pop(ctx context.Context) (core.WireMessage, bool) {
	for {
		select {
		case <-ctx.Done():
			return core.WireMessage{}, false
		case msg := <-o.highCh:
			return msg, true
		default:
		}

		o.mu.Lock()
		msg, ok := o.popLocked()
		o.mu.Unlock()
		if ok {
			return msg, true
		}

		select {
		case <-ctx.Done():
			return core.WireMessage{}, false
		case msg := <-o.highCh:
			return msg, true
		case <-o.notify:
		}
	}
}

func (o *wireOutbox) stats() (heartbeatsCoalesced, metricsCoalesced uint64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.coalescedHeartbeats, o.coalescedMetrics
}

// compactTelemetryState tracks the latest slow-changing snapshots and traffic
// timestamps sent within a session so unchanged sections can be omitted on
// high-frequency WireMetrics pushes without losing control-plane state.
type compactTelemetryState struct {
	mu              sync.Mutex
	hasBaseline     bool
	resyncPending   bool
	interfacesKnown bool
	interfaces      []core.HostNetworkInterface
	bbrKnown        bool
	bbr             *core.SystemBBRStatus
	clockKnown      bool
	clockSync       *core.ClockSyncStatus
	trafficKnown    bool
	traffic         map[string]core.PortTrafficUsage
}

func newCompactTelemetryState() *compactTelemetryState {
	return &compactTelemetryState{}
}

func cloneNetworkInterfaces(src []core.HostNetworkInterface) []core.HostNetworkInterface {
	if src == nil {
		return nil
	}
	out := make([]core.HostNetworkInterface, len(src))
	for i := range src {
		out[i] = core.HostNetworkInterface{
			Name:      src[i].Name,
			Addresses: slices.Clone(src[i].Addresses),
		}
	}
	return out
}

func equalNetworkInterfaces(a, b []core.HostNetworkInterface) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || !slices.Equal(a[i].Addresses, b[i].Addresses) {
			return false
		}
	}
	return true
}

func cloneSystemBBRStatus(src *core.SystemBBRStatus) *core.SystemBBRStatus {
	if src == nil {
		return nil
	}
	out := *src
	out.AvailableAlgorithms = slices.Clone(src.AvailableAlgorithms)
	out.Parameters = maps.Clone(src.Parameters)
	out.Qdiscs = slices.Clone(src.Qdiscs)
	out.ConfiguredParameters = maps.Clone(src.ConfiguredParameters)
	return &out
}

func (s *compactTelemetryState) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasBaseline = false
	s.resyncPending = true
	s.interfacesKnown = false
	s.interfaces = nil
	s.bbrKnown = false
	s.bbr = nil
	s.clockKnown = false
	s.clockSync = nil
	s.trafficKnown = false
	clear(s.traffic)
}

func (s *compactTelemetryState) RecordHeartbeat(msg core.WireMessage) {
	if msg.Heartbeat == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasBaseline = true
	s.resyncPending = false
	if m := msg.Heartbeat.Metrics; m != nil {
		if m.NetworkInterfaces != nil {
			s.interfaces = cloneNetworkInterfaces(m.NetworkInterfaces)
			s.interfacesKnown = true
		}
		if m.BBR != nil {
			s.bbr = cloneSystemBBRStatus(m.BBR)
			s.bbrKnown = true
		}
		if m.ClockSync != nil {
			s.clockSync = cloneClockSync(m.ClockSync)
			s.clockKnown = true
		}
	}
	s.recordTrafficLocked(msg.Heartbeat.TrafficUsage)
}

func (s *compactTelemetryState) CompactMetrics(msg core.WireMessage) core.WireMessage {
	if msg.Metrics == nil {
		return msg
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasBaseline {
		s.hasBaseline = true
		if msg.Metrics.NetworkInterfaces != nil {
			s.interfaces = cloneNetworkInterfaces(msg.Metrics.NetworkInterfaces)
			s.interfacesKnown = true
		}
		if msg.Metrics.BBR != nil {
			s.bbr = cloneSystemBBRStatus(msg.Metrics.BBR)
			s.bbrKnown = true
		}
		if msg.Metrics.ClockSync != nil {
			s.clockSync = cloneClockSync(msg.Metrics.ClockSync)
			s.clockKnown = true
		}
		s.recordTrafficLocked(msg.TrafficUsage)
		if s.resyncPending {
			msg.TelemetryFullSync = true
			s.resyncPending = false
		}
		return msg
	}
	mCopy := *msg.Metrics
	if s.interfacesKnown && equalNetworkInterfaces(mCopy.NetworkInterfaces, s.interfaces) {
		mCopy.NetworkInterfaces = nil
	} else if mCopy.NetworkInterfaces != nil {
		s.interfaces = cloneNetworkInterfaces(mCopy.NetworkInterfaces)
		s.interfacesKnown = true
	}

	if s.bbrKnown && mCopy.BBR != nil && equalBBRStatus(mCopy.BBR, s.bbr) {
		mCopy.BBR = nil
	} else if mCopy.BBR != nil {
		s.bbr = cloneSystemBBRStatus(mCopy.BBR)
		s.bbrKnown = true
	}

	if s.clockKnown && mCopy.ClockSync != nil && equalClockSync(mCopy.ClockSync, s.clockSync) {
		mCopy.ClockSync = nil
	} else if mCopy.ClockSync != nil {
		s.clockSync = cloneClockSync(mCopy.ClockSync)
		s.clockKnown = true
	}
	msg.Metrics = &mCopy

	if len(msg.TrafficUsage) > 0 && s.trafficKnown {
		var diff []core.PortTrafficUsage
		for _, usage := range msg.TrafficUsage {
			prev, exists := s.traffic[usage.PolicyID]
			if exists && sameTrafficUsageSnapshot(prev, usage) {
				continue
			}
			diff = append(diff, usage)
		}
		s.recordTrafficLocked(msg.TrafficUsage)
		msg.TrafficUsage = diff
	} else {
		s.recordTrafficLocked(msg.TrafficUsage)
	}
	return msg
}

func (s *compactTelemetryState) recordTrafficLocked(usages []core.PortTrafficUsage) {
	s.trafficKnown = true
	if len(usages) == 0 {
		clear(s.traffic)
		return
	}
	if s.traffic == nil {
		s.traffic = make(map[string]core.PortTrafficUsage, len(usages))
	} else {
		clear(s.traffic)
	}
	for _, usage := range usages {
		if usage.Accounting != nil {
			acc := *usage.Accounting
			acc.Outbounds = slices.Clone(usage.Accounting.Outbounds)
			acc.Counters = maps.Clone(usage.Accounting.Counters)
			usage.Accounting = &acc
		}
		s.traffic[usage.PolicyID] = usage
	}
}

func equalBBRStatus(a, b *core.SystemBBRStatus) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.CollectedAt.Equal(b.CollectedAt) &&
		a.Available == b.Available &&
		a.KernelRelease == b.KernelRelease &&
		a.CongestionControl == b.CongestionControl &&
		a.DefaultQdisc == b.DefaultQdisc &&
		a.QdiscError == b.QdiscError &&
		a.Persistence == b.Persistence &&
		a.ConfiguredAlgorithm == b.ConfiguredAlgorithm &&
		a.ConfiguredQdisc == b.ConfiguredQdisc &&
		a.Error == b.Error &&
		slices.Equal(a.AvailableAlgorithms, b.AvailableAlgorithms) &&
		maps.Equal(a.Parameters, b.Parameters) &&
		slices.Equal(a.Qdiscs, b.Qdiscs) &&
		maps.Equal(a.ConfiguredParameters, b.ConfiguredParameters)
}

func equalClockSync(a, b *core.ClockSyncStatus) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Status != b.Status || !a.CheckedAt.Equal(b.CheckedAt) {
		return false
	}
	if (a.OffsetMilliseconds == nil) != (b.OffsetMilliseconds == nil) {
		return false
	}
	if a.OffsetMilliseconds != nil && *a.OffsetMilliseconds != *b.OffsetMilliseconds {
		return false
	}
	return true
}

func equalAccounting(a, b *core.TrafficAccounting) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Source == b.Source &&
		a.Inbound == b.Inbound &&
		a.Mark == b.Mark &&
		a.ProcessEpoch == b.ProcessEpoch &&
		a.ClientReceived == b.ClientReceived &&
		a.ClientSent == b.ClientSent &&
		a.TargetReceived == b.TargetReceived &&
		a.TargetSent == b.TargetSent &&
		slices.Equal(a.Outbounds, b.Outbounds) &&
		maps.Equal(a.Counters, b.Counters)
}

func sameTrafficUsageSnapshot(a, b core.PortTrafficUsage) bool {
	return a.PolicyID == b.PolicyID &&
		a.ShareID == b.ShareID &&
		a.ShareUsedBytes == b.ShareUsedBytes &&
		a.ResetGeneration == b.ResetGeneration &&
		a.ReceivedBytes == b.ReceivedBytes &&
		a.SentBytes == b.SentBytes &&
		a.UsedBytes == b.UsedBytes &&
		a.LifetimeReceivedBytes == b.LifetimeReceivedBytes &&
		a.LifetimeSentBytes == b.LifetimeSentBytes &&
		a.ReceiveBPS == b.ReceiveBPS &&
		a.SendBPS == b.SendBPS &&
		a.Blocked == b.Blocked &&
		a.EnforcementAvailable == b.EnforcementAvailable &&
		a.EnforcementError == b.EnforcementError &&
		a.CounterEpoch == b.CounterEpoch &&
		a.CollectedAt.Equal(b.CollectedAt) &&
		a.PeriodStart.Equal(b.PeriodStart) &&
		a.PeriodEnd.Equal(b.PeriodEnd) &&
		equalAccounting(a.Accounting, b.Accounting)
}
