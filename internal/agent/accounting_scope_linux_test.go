package agent

import (
	"context"
	"errors"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifiedListenerProtocols(t *testing.T) {
	for _, tc := range []struct {
		engine  core.Engine
		content string
		want    core.TrafficProtocol
	}{
		{core.EngineXray, `{"inbounds":[{"port":443,"protocol":"vless"}]}`, core.TrafficProtocolTCP},
		{core.EngineXray, `{"inbounds":[{"port":443,"protocol":"vless","streamSettings":{"network":"xhttp","security":"tls","tlsSettings":{"alpn":["h3"]}}}]}`, core.TrafficProtocolUDP},
		{core.EngineXray, `{"inbounds":[{"port":443,"protocol":"vless","streamSettings":{"network":"splithttp","security":"tls","tlsSettings":{"alpn":["h2","http/1.1"]}}}]}`, core.TrafficProtocolTCP},
		{core.EngineXray, `{"inbounds":[{"port":443,"protocol":"vless","streamSettings":{"network":"xhttp","security":"tls","tlsSettings":{"alpn":["h3","h2"]}}}]}`, core.TrafficProtocolTCP},
		{core.EngineXray, `{"inbounds":[{"port":443,"protocol":"vmess","streamSettings":{"network":"kcp"}}]}`, core.TrafficProtocolUDP},
		{core.EngineXray, `{"inbounds":[{"port":443,"protocol":"dokodemo-door","settings":{"network":"udp"}}]}`, core.TrafficProtocolUDP},
		{core.EngineXray, `{"inbounds":[{"port":443,"protocol":"vless"},{"port":443,"protocol":"vmess"}]}`, core.TrafficProtocolBoth},
		{core.EngineSingBox, `{"inbounds":[{"listen_port":443,"type":"hysteria2"}]}`, core.TrafficProtocolUDP},
		{core.EngineSingBox, `{"inbounds":[{"listen_port":443,"type":"tuic"}]}`, core.TrafficProtocolUDP},
		{core.EngineSingBox, `{"inbounds":[{"listen_port":443,"type":"vless"}]}`, core.TrafficProtocolTCP},
		{core.EngineMihomo, "listeners:\n  - {name: hy, type: hysteria2, port: 443}\n", core.TrafficProtocolUDP},
		{core.EngineShadowsocksRust, `{"server_port":443,"mode":"udp_only"}`, core.TrafficProtocolUDP},
	} {
		got := accountingListenerProtocols(tc.engine, tc.content)[443]
		if got != tc.want {
			t.Errorf("%s %s got %s want %s", tc.engine, tc.content, got, tc.want)
		}
		if tc.engine == core.EngineXray && (strings.Contains(tc.content, "xhttp") || strings.Contains(tc.content, "splithttp")) {
			endpoints := serverconfig.DiscoverTrafficPorts(tc.engine, tc.content)
			if len(endpoints) != 1 || endpoints[0].Protocol != tc.want {
				t.Fatalf("discovery and accounting disagree: %+v", endpoints)
			}
		}
	}
}

func TestExplicitSingleTransportScope(t *testing.T) {
	for _, tc := range []struct {
		engine core.Engine
		config string
		want   core.TrafficProtocol
	}{
		{core.EngineSingBox, `{"outbounds":[{"tag":"out","type":"direct"}],"inbounds":[{"tag":"edge","type":"vmess","listen_port":443,"transport":{"type":"quic"}}]}`, core.TrafficProtocolUDP},
		{core.EngineMihomo, "listeners: [{name: edge, type: shadowsocks, port: 443, udp: false}]", core.TrafficProtocolTCP},
		{core.EngineMihomo, "listeners: [{name: edge, type: shadowsocks, port: 443, udp: true}]", core.TrafficProtocolBoth},
		{core.EngineMihomo, "listeners: [{name: edge, type: shadowsocks, port: 443, network: tcp}]", core.TrafficProtocolBoth},
		{core.EngineMihomo, "listeners: [{name: edge, type: shadowsocks, port: 443, udp: 'false'}]", core.TrafficProtocolBoth},
		{core.EngineXray, `{"outbounds":[{"tag":"out","protocol":"freedom"}],"inbounds":[{"tag":"edge","protocol":"socks","port":443,"settings":{"udp":false}}]}`, core.TrafficProtocolTCP},
		{core.EngineXray, `{"outbounds":[{"tag":"out","protocol":"freedom"}],"inbounds":[{"tag":"edge","protocol":"socks","port":443,"settings":{"udp":true}}]}`, core.TrafficProtocolBoth},
		{core.EngineXray, `{"outbounds":[{"tag":"out","protocol":"freedom"}],"inbounds":[{"tag":"edge","protocol":"socks","port":443,"settings":{"udp":false},"streamSettings":{"network":"kcp"}}]}`, core.TrafficProtocolBoth},
	} {
		endpoints := serverconfig.DiscoverTrafficPorts(tc.engine, tc.config)
		if len(endpoints) != 1 || endpoints[0].Protocol != tc.want {
			t.Fatalf("discovery %s: %+v", tc.config, endpoints)
		}
		plan, err := serverconfig.PrepareIndependentEgress(tc.engine, tc.config)
		if err != nil {
			t.Fatal(err)
		}
		protocols := accountingListenerProtocols(tc.engine, plan.Content)
		if protocols[443] != tc.want {
			t.Fatalf("accounting %s: %v", tc.config, protocols)
		}
		for _, proto := range []core.TrafficProtocol{core.TrafficProtocolTCP, core.TrafficProtocolUDP} {
			policy := core.PortTrafficPolicy{Engine: tc.engine, Port: 443, InboundTag: "edge", Protocol: proto}
			issue := nativeAccountingScopeIssue(policy, nativeAccountingSnapshot{Plan: plan, ListenerProtocols: protocols})
			if (issue == "") != (proto == tc.want) {
				t.Fatalf("%s %s: %q", tc.config, proto, issue)
			}
		}
	}
}

func TestSingleTransportInitialFailurePreservesDiagnostic(t *testing.T) {
	for _, protocol := range []core.TrafficProtocol{core.TrafficProtocolTCP, core.TrafficProtocolUDP} {
		m, _, _, p := newAccuracyTrafficManager(t)
		p.Protocol = protocol
		if err := m.SetPolicies(context.Background(), []core.PortTrafficPolicy{p}, p.AgentID); err != nil {
			t.Fatal(err)
		}
		m.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
			return nativeAccountingSnapshot{}, errors.New("API connection refused")
		}
		m.collect(context.Background(), false)
		if got := m.Snapshot()[0]; !strings.Contains(got.EnforcementError, "API connection refused") || strings.Contains(got.EnforcementError, "exclusive listener") {
			t.Fatalf("diagnostic lost: %+v", got)
		}
		m.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
			return nativeAccountingSnapshot{}, nil
		}
		m.collect(context.Background(), false)
		if got := m.Snapshot()[0]; strings.Contains(got.EnforcementError, "API connection refused") {
			t.Fatal("stale source error retained")
		}
	}
}

func TestSingleTransportNativeAccountingRecovery(t *testing.T) {
	for _, protocol := range []core.TrafficProtocol{core.TrafficProtocolTCP, core.TrafficProtocolUDP} {
		t.Run(string(protocol), func(t *testing.T) {
			m, _, now, p := newAccuracyTrafficManager(t)
			p.Protocol = protocol
			if err := m.SetPolicies(context.Background(), []core.PortTrafficPolicy{p}, p.AgentID); err != nil {
				t.Fatal(err)
			}
			counters := map[string]uint64{"inbound>>>a>>>traffic>>>uplink": 100, "inbound>>>a>>>traffic>>>downlink": 100, "outbound>>>direct>>>traffic>>>uplink": 100, "outbound>>>direct>>>traffic>>>downlink": 100}
			failed := false
			m.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
				if failed {
					return nativeAccountingSnapshot{}, errors.New("temporary API failure")
				}
				return nativeAccountingSnapshot{Plan: serverconfig.AccountingPlan{Source: "core-api", Ports: []serverconfig.AccountingPort{{Port: p.Port, Inbound: "a", Outbounds: []string{"direct"}}}}, Counters: counters, ProcessEpoch: "same", ListenerProtocols: map[int]core.TrafficProtocol{p.Port: protocol}}, nil
			}
			m.collect(context.Background(), false)
			epoch := m.records[p.ID].CounterEpoch
			for key := range counters {
				counters[key] = 200
			}
			*now = now.Add(time.Second)
			m.collect(context.Background(), false)
			if got := m.Snapshot()[0]; got.ReceivedBytes != 200 || got.SentBytes != 200 || got.EnforcementError != "" {
				t.Fatalf("native sample %+v", got)
			}
			failed = true
			*now = now.Add(time.Second)
			m.collect(context.Background(), false)
			if m.records[p.ID].CounterEpoch != epoch {
				t.Fatal("failure changed epoch")
			}
			failed = false
			for key := range counters {
				counters[key] = 250
			}
			*now = now.Add(time.Second)
			m.collect(context.Background(), false)
			if got := m.Snapshot()[0]; got.UsedBytes != 600 {
				t.Fatalf("recovery total %+v", got)
			}
			m.collect(context.Background(), false)
			if got := m.Snapshot()[0]; got.UsedBytes != 600 {
				t.Fatal("duplicate sample billed")
			}
		})
	}
}

func TestSingleTransportMarkedAccountingIncludesBothOutboundTransports(t *testing.T) {
	for _, protocol := range []core.TrafficProtocol{core.TrafficProtocolTCP, core.TrafficProtocolUDP} {
		m, _, _, p := newAccuracyTrafficManager(t)
		r := m.records[p.ID]
		r.Policy.Protocol = protocol
		snapshot := nativeAccountingSnapshot{Plan: serverconfig.AccountingPlan{Source: "nft-dual", Ports: []serverconfig.AccountingPort{{Port: p.Port, Inbound: "a", Mark: uint32(0x51430000) | uint32(p.Port)}}}, ProcessEpoch: "one", ListenerProtocols: map[int]core.TrafficProtocol{p.Port: protocol}}
		if nativeAccountingScopeIssue(r.Policy, snapshot) != "" {
			t.Fatal("exclusive listener rejected")
		}
		if err := prepareMarkedAccounting(r, snapshot); err != nil {
			t.Fatal(err)
		}
		counters := map[string]uint64{}
		for _, transport := range []string{"tcp", "udp"} {
			counters[trafficCounterName(r, "targetin", transport)] = 20
			counters[trafficCounterName(r, "targetout", transport)] = 30
		}
		rx, tx := collectMarkedAccounting(counters, r, 10, 15, false)
		if rx != 50 || tx != 75 {
			t.Fatalf("cross-transport egress lost: %d %d", rx, tx)
		}
		rx, tx = collectMarkedAccounting(counters, r, 0, 0, false)
		if rx != 0 || tx != 0 {
			t.Fatal("marked egress double counted")
		}
	}
}

func TestMieruListenerScope(t *testing.T) {
	protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolMieru)
	input, err := serverconfig.NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.Port = 443
	generated, err := serverconfig.Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	const listener = "listeners:\n  - {name: edge, type: mieru, port: 443, users: {test: test}"
	for _, tc := range []struct {
		name, content string
		want          core.TrafficProtocol
	}{
		{"preset", generated, core.TrafficProtocolTCP},
		{"tcp", listener + ", transport: TCP}\n", core.TrafficProtocolTCP},
		{"udp", listener + ", transport: UDP}\n", core.TrafficProtocolUDP},
		{"missing", listener + ", network: tcp}\n", core.TrafficProtocolBoth},
		{"invalid", listener + ", transport: 'tcp,garbage'}\n", core.TrafficProtocolBoth},
		{"lowercase", listener + ", transport: tcp}\n", core.TrafficProtocolBoth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ports := serverconfig.DiscoverTrafficPorts(core.EngineMihomo, tc.content)
			plan, err := serverconfig.PrepareIndependentEgress(core.EngineMihomo, tc.content)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := compileAccountingConfiguration(core.EngineMihomo, plan.Content)
			if err != nil {
				t.Fatal(err)
			}
			m, _, _, p := newAccuracyTrafficManager(t)
			p.Engine, p.Protocol = core.EngineMihomo, core.TrafficProtocolTCP
			if tc.want == core.TrafficProtocolUDP {
				p.Protocol = tc.want
			}
			m.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) { return snapshot, nil }
			if err := m.SetPolicies(context.Background(), []core.PortTrafficPolicy{p}, p.AgentID); err != nil {
				t.Fatal(err)
			}
			got := m.Snapshot()[0]
			if len(ports) != 1 || ports[0].Protocol != tc.want || snapshot.ListenerProtocols[p.Port] != tc.want || (got.Accounting != nil && got.Accounting.Source == "nft-dual") != (tc.want != core.TrafficProtocolBoth) {
				t.Fatalf("discovery=%v verified=%s diagnostic=%q", ports, snapshot.ListenerProtocols[p.Port], got.EnforcementError)
			}
			if tc.want != core.TrafficProtocolBoth {
				p.Protocol = core.TrafficProtocolUDP
				if tc.want == p.Protocol {
					p.Protocol = core.TrafficProtocolTCP
				}
				if nativeAccountingScopeIssue(p, snapshot) == "" {
					t.Fatal("mismatched policy accepted")
				}
			}
		})
	}
	for _, content := range []string{listener + ", transport: TCP}\n  - {name: other, type: http, port: 443}\n", "mixed-port: 443\n" + listener + ", transport: UDP}\n"} {
		if _, err := serverconfig.PrepareIndependentEgress(core.EngineMihomo, content); err == nil {
			t.Fatal("shared port acquired an outbound")
		}
	}
}

func TestMarkedListenerMigrationKeepsMeasuredBoundary(t *testing.T) {
	m, b, now, p := newAccuracyTrafficManager(t)
	p.Engine, p.Protocol, p.InboundTag = core.EngineMihomo, core.TrafficProtocolTCP, "edge"
	ctx := context.Background()
	if err := m.SetPolicies(ctx, []core.PortTrafficPolicy{p}, p.AgentID); err != nil {
		t.Fatal(err)
	}
	r := m.records[p.ID]
	bump := func(direction string, n uint64) { b.counters[trafficCounterName(r, direction, "tcp")] += n }
	sample := func() core.PortTrafficUsage {
		*now = now.Add(time.Second)
		m.collect(ctx, false)
		return m.Snapshot()[0]
	}
	bump("in", 50)
	bump("out", 100)
	if got := sample(); got.UsedBytes != 150 {
		t.Fatal(got)
	}
	plan := serverconfig.AccountingPlan{Source: "nft-dual", Ports: []serverconfig.AccountingPort{{Port: p.Port, Inbound: p.InboundTag, Mark: 0x51430000 | uint32(p.Port)}}}
	snapshot := nativeAccountingSnapshot{Plan: plan, ListenerProtocols: map[int]core.TrafficProtocol{p.Port: p.Protocol}}
	m.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) { return snapshot, nil }
	oldName := trafficCounterName(r, "in", "tcp")
	b.onReplace = func() { b.counters[oldName] += 5 }
	bump("in", 3)
	bump("out", 7)
	if got := sample(); got.UsedBytes != 10 || got.Accounting.ClientReceived != 3 || got.Accounting.ClientSent != 7 || r.QuotaBaselineBytes != 150 {
		t.Fatal(got)
	}
	b.onReplace = nil
	bump("in", 4)
	bump("targetout", 6)
	if got := sample(); got.UsedBytes != 25 {
		t.Fatal(got)
	}
	snapshot.ScopeError = "Mihomo configuration does not match the active deployment"
	bump("in", 9)
	bump("targetout", 888)
	if got := sample(); got.ReceivedBytes != 9 || got.SentBytes != 0 || got.Accounting != nil || r.QuotaBaselineBytes != 175 || !strings.Contains(got.EnforcementError, snapshot.ScopeError) {
		t.Fatal(got)
	}
	if got := sample(); got.UsedBytes != 9 {
		t.Fatal(got)
	}
}

func TestMihomoAccountingNeedsEffectiveConfiguration(t *testing.T) {
	dir := t.TempDir()
	spec := EngineSpec{ConfigPath: filepath.Join(dir, "config.yaml"), Service: "qagent-mihomo.service"}
	const content = "mode: rule\n"
	if err := os.WriteFile(spec.ConfigPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	e := &Executor{serviceStarts: map[string]cachedServiceStart{spec.Service: {epoch: "legacy", started: time.Now().Add(time.Minute)}}}
	ctx := context.Background()
	check := func(p core.PortTrafficPolicy, want bool) {
		t.Helper()
		snapshot := nativeAccountingSnapshot{ListenerProtocols: map[int]core.TrafficProtocol{443: core.TrafficProtocolTCP}}
		if err := e.verifyAccountingDeployment(ctx, spec, content, "legacy", &snapshot); err != nil {
			t.Fatal(err)
		}
		if issue := nativeAccountingScopeIssue(p, snapshot); (issue == "") != want {
			t.Fatalf("unexpected scope issue %q", issue)
		}
	}
	p := core.PortTrafficPolicy{Engine: core.EngineMihomo, Protocol: core.TrafficProtocolTCP, Port: 443, ConfigID: "fixture", ConfigVersion: 1}
	check(p, true)
	e.serviceStarts[spec.Service] = cachedServiceStart{epoch: "legacy", started: time.Now().Add(-time.Minute)}
	check(p, false)
	state := sharedDeploymentState{ConfigID: p.ConfigID, ConfigVersion: 1, Digest: configurationDigest(content)}
	for _, tc := range []struct {
		applying bool
		digest   string
		version  int
		want     bool
	}{
		{false, configurationDigest(content), 1, true}, {true, configurationDigest(content), 1, false},
		{false, configurationDigest("another config"), 1, false}, {false, configurationDigest(content), 2, false},
	} {
		state.Applying, state.Digest, state.ConfigVersion = tc.applying, tc.digest, tc.version
		if err := writeSharedDeploymentState(spec, state); err != nil {
			t.Fatal(err)
		}
		check(p, tc.want)
	}
}
