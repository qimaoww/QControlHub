package store

import (
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMultiHopChainSchedulingAndAggregation(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	req := core.DiagnosticRequest{
		RequestID: "req_chain_1",
		Nodes: []core.DiagnosticEndpoint{
			{AgentID: "agt_a", Name: "A", Address: "198.51.100.1"},
			{AgentID: "agt_b", Name: "B", Address: "198.51.100.2"},
			{AgentID: "agt_c", Name: "C", Address: "198.51.100.3"},
			{AgentID: "agt_d", Name: "D", Address: "198.51.100.4"},
		},
		Bidirectional: true,
		Parameters: core.DiagnosticParameters{
			Tool: "ping", Protocol: "icmp", Provider: "disable-geoip",
			Count: 5, IntervalMS: 200, TimeoutMS: 1000, MaxHops: 30, Size: 56, Port: 5201, Duration: 5, Streams: 1,
		},
	}
	sess := core.DiagnosticSession{
		ID:        "ndg_chain01",
		Request:   req,
		State:     "running",
		Results:   map[string]core.DiagnosticResult{},
		CreatedAt: now,
		Deadline:  now.Add(2 * time.Minute),
	}

	// Round 0 must emit at most 1 active command per source agent (f0 on A, r0 on B, r1 on C, r2 on D).
	round0 := diagnosticCommands(sess)
	if len(round0) != 4 {
		t.Fatalf("expected 4 initial non-overlapping commands, got %+v", round0)
	}
	seenAgent := map[string]bool{}
	for _, cmd := range round0 {
		if seenAgent[cmd.Source.AgentID] {
			t.Fatalf("agent %s received concurrent commands in round 0: %+v", cmd.Source.AgentID, round0)
		}
		seenAgent[cmd.Source.AgentID] = true
		sess.Results[cmd.ID] = core.DiagnosticResult{ID: cmd.ID, State: "succeeded", Cleaned: true, At: now}
	}
	diagnosticComplete(&sess)
	if sess.State != "running" {
		t.Fatalf("session finished before round 1 commands executed: %s", sess.State)
	}

	// Round 1 emits remaining B->C (f1) and C->D (f2).
	round1 := diagnosticCommands(sess)
	if len(round1) != 2 {
		t.Fatalf("expected 2 remaining commands in round 1, got %+v", round1)
	}
	for _, cmd := range round1 {
		sess.Results[cmd.ID] = core.DiagnosticResult{ID: cmd.ID, State: "succeeded", Cleaned: true, At: now}
	}
	diagnosticComplete(&sess)
	if sess.State != "succeeded" {
		t.Fatalf("expected session succeeded after all hops cleaned, got %s", sess.State)
	}

	// Verify stopping a multi-hop chain finishes cleanly once dispatched commands clean up.
	stopSess := core.DiagnosticSession{
		ID:        "ndg_chain02",
		Request:   req,
		State:     "stopping",
		Results:   map[string]core.DiagnosticResult{"ndg_chain02-f0": {ID: "ndg_chain02-f0", State: "canceled", Cleaned: true}},
		CreatedAt: now,
		Deadline:  now.Add(2 * time.Minute),
	}
	diagnosticComplete(&stopSess)
	if stopSess.State != "canceled" {
		t.Fatalf("expected stopping session with cleaned active work to cancel cleanly, got %s", stopSess.State)
	}

	// Verify bidirectional end-to-end RTT sum and compound survival packet loss across hops.
	avg := func(v float64) *float64 { return &v }
	fwd := []*core.DashboardProbeDirectionMetrics{
		{State: "succeeded", Sent: 5, Received: 5, LossPercent: 0, MinMS: avg(8), AvgMS: avg(10), MaxMS: avg(12), StdevMS: avg(3), Reached: true},
		{State: "succeeded", Sent: 10, Received: 9, LossPercent: 10, MinMS: avg(18), AvgMS: avg(20), MaxMS: avg(24), StdevMS: avg(4), Reached: true},
		{State: "succeeded", Sent: 10, Received: 9, LossPercent: 10, MinMS: avg(28), AvgMS: avg(30), MaxMS: avg(35), StdevMS: avg(0), Reached: true},
	}
	aggFwd := aggregateChainDirectionMetrics(fwd, req.Nodes, false, "succeeded")
	if aggFwd == nil || !aggFwd.Reached || aggFwd.AvgMS == nil || *aggFwd.AvgMS != 60 || aggFwd.LossPercent != 19 {
		t.Fatalf("unexpected aggregated forward chain metrics: %+v", aggFwd)
	}
	rev := []*core.DashboardProbeDirectionMetrics{
		{State: "succeeded", Sent: 5, Received: 5, LossPercent: 0, MinMS: avg(11), AvgMS: avg(12), MaxMS: avg(14), StdevMS: avg(1), Reached: true},
		{State: "succeeded", Sent: 5, Received: 5, LossPercent: 0, MinMS: avg(21), AvgMS: avg(22), MaxMS: avg(24), StdevMS: avg(1), Reached: true},
		{State: "succeeded", Sent: 5, Received: 5, LossPercent: 0, MinMS: avg(31), AvgMS: avg(32), MaxMS: avg(34), StdevMS: avg(1), Reached: true},
	}
	aggRev := aggregateChainDirectionMetrics(rev, req.Nodes, true, "succeeded")
	if aggRev == nil || !aggRev.Reached || aggRev.AvgMS == nil || *aggRev.AvgMS != 66 || aggRev.LossPercent != 0 {
		t.Fatalf("unexpected aggregated reverse chain metrics: %+v", aggRev)
	}
}
