package core

import (
	"strings"
	"testing"
)

func TestDashboardProbePairRequestNormalizeAndValidate(t *testing.T) {
	t.Parallel()
	valid := DashboardProbePairRequest{
		Name:          "  HK -> SG Latency  ",
		GroupName:     "  默认分组  ",
		Node1:         DiagnosticEndpoint{AgentID: "agt_0123456789abcdef", Address: "198.51.100.10"},
		Node2:         DiagnosticEndpoint{AgentID: "agt_fedcba9876543210", Address: "203.0.113.20"},
		Bidirectional: true,
	}
	req := valid
	if err := req.NormalizeAndValidate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "HK -> SG Latency" || req.Parameters.Tool != "ping" || req.Parameters.Protocol != "icmp" || req.Parameters.Count != DashboardProbeDefaultCount || req.AutoIntervalSeconds != 60 {
		t.Fatalf("unexpected normalized defaults: %+v", req)
	}

	custom := DashboardProbePairRequest{
		GroupName:  "公网拨测",
		TargetMode: "ip",
		Node1:      DiagnosticEndpoint{AgentID: "agt_0123456789abcdef", Address: "198.51.100.10"},
		Node2:      DiagnosticEndpoint{Address: "1.1.1.1"},
		Parameters: DiagnosticParameters{Count: 4, IntervalMS: 250, TimeoutMS: 1200, Size: 64},
	}
	if err := custom.NormalizeAndValidate(); err != nil {
		t.Fatalf("custom IP probe rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*DashboardProbePairRequest)
	}{
		{"empty group", func(r *DashboardProbePairRequest) { r.GroupName = "" }},
		{"blank group", func(r *DashboardProbePairRequest) { r.GroupName = "   " }},
		{"control group", func(r *DashboardProbePairRequest) { r.GroupName = "a\nb" }},
		{"long group", func(r *DashboardProbePairRequest) { r.GroupName = strings.Repeat("组", 41) }},
		{"long name", func(r *DashboardProbePairRequest) { r.Name = strings.Repeat("a", 81) }},
		{"mixed family", func(r *DashboardProbePairRequest) { r.Node2.Address = "2001:db8::2" }},
		{"same agent", func(r *DashboardProbePairRequest) { r.Node2.AgentID = r.Node1.AgentID }},
		{"loopback target", func(r *DashboardProbePairRequest) {
			r.TargetMode = "ip"
			r.Bidirectional = false
			r.Node2 = DiagnosticEndpoint{Address: "127.0.0.1"}
		}},
		{"custom bidirectional", func(r *DashboardProbePairRequest) {
			r.TargetMode = "ip"
			r.Bidirectional = true
			r.Node2 = DiagnosticEndpoint{Address: "1.1.1.1"}
		}},
		{"custom bound node", func(r *DashboardProbePairRequest) {
			r.TargetMode = "ip"
			r.Bidirectional = false
			r.Node2.AgentID = "agt_fedcba9876543210"
		}},
		{"custom same ip", func(r *DashboardProbePairRequest) {
			r.TargetMode = "ip"
			r.Bidirectional = false
			r.Node2 = DiagnosticEndpoint{Address: r.Node1.Address}
		}},
		{"wrong tool", func(r *DashboardProbePairRequest) { r.Parameters.Tool = "mtr" }},
		{"too small interval", func(r *DashboardProbePairRequest) { r.AutoIntervalSeconds = 10 }},
		{"too large interval", func(r *DashboardProbePairRequest) { r.AutoIntervalSeconds = 3601 }},
		{"invalid count", func(r *DashboardProbePairRequest) { r.Parameters.Count = 101 }},
		{"negative size", func(r *DashboardProbePairRequest) { r.Parameters.Size = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := valid
			tc.mutate(&bad)
			if err := bad.NormalizeAndValidate(); err == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.name)
			}
		})
	}

	chain := DashboardProbePairRequest{
		Name:      "  A-B-C-D Full Chain  ",
		GroupName: "  亚太骨干组  ",
		Nodes: []DiagnosticEndpoint{
			{AgentID: "agt_a", Address: "198.51.100.1"},
			{AgentID: "agt_b", Address: "198.51.100.2"},
			{AgentID: "agt_c", Address: "198.51.100.3"},
			{AgentID: "agt_d", Address: "198.51.100.4"},
		},
		Bidirectional: true,
	}
	if err := chain.NormalizeAndValidate(); err != nil {
		t.Fatalf("valid A-B-C-D chain rejected: %v", err)
	}
	if chain.GroupName != "亚太骨干组" || chain.Node1.AgentID != "agt_a" || chain.Node2.AgentID != "agt_d" || len(chain.Nodes) != 4 {
		t.Fatalf("unexpected normalized chain: %+v", chain)
	}

	dupChain := chain
	dupChain.Nodes = []DiagnosticEndpoint{
		{AgentID: "agt_a", Address: "198.51.100.1"},
		{AgentID: "agt_b", Address: "198.51.100.2"},
		{AgentID: "agt_a", Address: "198.51.100.1"},
	}
	if err := dupChain.NormalizeAndValidate(); err == nil {
		t.Fatal("duplicate agent in chain must be rejected")
	}

	mixedChain := chain
	mixedChain.Nodes = []DiagnosticEndpoint{
		{AgentID: "agt_a", Address: "198.51.100.1"},
		{AgentID: "agt_b", Address: "2001:db8::2"},
		{AgentID: "agt_c", Address: "198.51.100.3"},
	}
	if err := mixedChain.NormalizeAndValidate(); err == nil {
		t.Fatal("mixed IPv4/IPv6 hop chain must be rejected")
	}

	customChain := DashboardProbePairRequest{
		GroupName:  "多跳IP非法组",
		TargetMode: "ip",
		Nodes: []DiagnosticEndpoint{
			{AgentID: "agt_a", Address: "198.51.100.1"},
			{AgentID: "agt_b", Address: "198.51.100.2"},
			{Address: "1.1.1.1"},
		},
	}
	if err := customChain.NormalizeAndValidate(); err == nil {
		t.Fatal("custom IP mode with >2 hops must be rejected")
	}
}
