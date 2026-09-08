package agent

import (
	"context"
	"errors"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
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
		if !nativeAccountingScopeAllowed(r.Policy, snapshot) {
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
