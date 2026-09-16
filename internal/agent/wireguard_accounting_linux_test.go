package agent

import (
	"context"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestWireGuardUDPAccountingIncludesAllLegs(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			manager, backend, now, policy := newAccuracyTrafficManager(t)
			policy.Engine, policy.Protocol = engine, core.TrafficProtocolUDP
			protocol, _ := serverconfig.FindProtocol(engine, serverconfig.ProtocolWireGuard)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Port, input.Tag = policy.Port, "wg-accounting"
			content, err := serverconfig.Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := serverconfig.PrepareIndependentEgress(engine, content)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := compileAccountingConfiguration(engine, plan.Content)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Plan.Ports) != 1 || len(snapshot.Plan.Ports[0].Outbounds) != 1 {
				t.Fatal("WireGuard lost its independent outbound")
			}
			snapshot.ProcessEpoch, snapshot.Counters = "wireguard-test", map[string]uint64{}
			if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
				t.Fatal(err)
			}
			manager.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
				return snapshot, nil
			}
			manager.collect(context.Background(), false)
			if got := manager.Snapshot()[0]; !got.EnforcementAvailable || got.EnforcementError != "" ||
				got.Accounting == nil || got.Accounting.Source != snapshot.Plan.Source {
				t.Fatalf("WireGuard UDP policy cannot establish dual accounting: %+v", got)
			}
			port := snapshot.Plan.Ports[0]
			record := manager.records[policy.ID]
			for i := uint64(1); i <= 2; i++ {
				if engine == core.EngineXray {
					snapshot.Counters["inbound>>>"+port.Inbound+">>>traffic>>>uplink"] = 10 * i
					snapshot.Counters["inbound>>>"+port.Inbound+">>>traffic>>>downlink"] = 20 * i
					snapshot.Counters["outbound>>>"+port.Outbounds[0]+">>>traffic>>>downlink"] = 70 * i
					snapshot.Counters["outbound>>>"+port.Outbounds[0]+">>>traffic>>>uplink"] = 110 * i
				} else {
					backend.counters[trafficCounterName(record, "in", "udp")] = 10 * i
					backend.counters[trafficCounterName(record, "out", "udp")] = 20 * i
					// The UDP tunnel carries both TCP and UDP target traffic.
					backend.counters[trafficCounterName(record, "targetin", "tcp")] = 30 * i
					backend.counters[trafficCounterName(record, "targetin", "udp")] = 40 * i
					backend.counters[trafficCounterName(record, "targetout", "tcp")] = 50 * i
					backend.counters[trafficCounterName(record, "targetout", "udp")] = 60 * i
				}
				*now = now.Add(time.Second)
				manager.collect(context.Background(), false)
				usage := manager.Snapshot()[0]
				a := usage.Accounting
				if !usage.EnforcementAvailable || usage.EnforcementError != "" || a == nil ||
					a.ClientReceived != 10*i || a.ClientSent != 20*i || a.TargetReceived != 70*i || a.TargetSent != 110*i ||
					usage.ReceivedBytes != 80*i || usage.SentBytes != 130*i || usage.UsedBytes != 210*i {
					t.Fatalf("UDP-only policy lost a traffic leg: %+v / %+v", usage, a)
				}
				manager.collect(context.Background(), false)
				if manager.Snapshot()[0].UsedBytes != usage.UsedBytes {
					t.Fatal("repeated WireGuard sample was billed twice")
				}
			}
			policy.Protocol = core.TrafficProtocolTCP
			if nativeAccountingScopeAllowed(policy, snapshot) {
				t.Fatal("TCP-only policy must not bill UDP WireGuard traffic")
			}
		})
	}
}

func TestWireGuardListenerProtocolsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		engine  core.Engine
		content string
		want    core.TrafficProtocol
	}{
		{"xray-wireguard", core.EngineXray, `{"inbounds":[{"protocol":"wireguard","port":443}]}`, core.TrafficProtocolUDP},
		{"xray-duplicate", core.EngineXray, `{"inbounds":[{"protocol":"wireguard","port":443},{"protocol":"wireguard","port":443}]}`, core.TrafficProtocolBoth},
		{"xray-unknown-first", core.EngineXray, `{"inbounds":[{"protocol":"future","port":443},{"protocol":"wireguard","port":443}]}`, core.TrafficProtocolBoth},
		{"xray-unknown-last", core.EngineXray, `{"inbounds":[{"protocol":"wireguard","port":443},{"protocol":"future","port":443}]}`, core.TrafficProtocolBoth},
		{"xray-unknown-hint", core.EngineXray, `{"inbounds":[{"protocol":"future","port":443,"settings":{"network":"udp"}}]}`, core.TrafficProtocolBoth},
		{"singbox-wireguard", core.EngineSingBox, `{"endpoints":[{"tag":"wg","type":"wireguard","listen_port":443}]}`, core.TrafficProtocolUDP},
		{"singbox-duplicate", core.EngineSingBox, `{"endpoints":[{"tag":"wg","type":"wireguard","listen_port":443},{"tag":"wg","type":"wireguard","listen_port":443}]}`, core.TrafficProtocolBoth},
		{"singbox-cross-list", core.EngineSingBox, `{"inbounds":[{"tag":"web","type":"vless","listen_port":443}],"endpoints":[{"tag":"wg","type":"wireguard","listen_port":443}]}`, core.TrafficProtocolBoth},
		{"singbox-unknown-first", core.EngineSingBox, `{"endpoints":[{"tag":"future","type":"future","listen_port":443},{"tag":"wg","type":"wireguard","listen_port":443}]}`, core.TrafficProtocolBoth},
		{"singbox-unknown-last", core.EngineSingBox, `{"endpoints":[{"tag":"wg","type":"wireguard","listen_port":443},{"tag":"future","type":"future","listen_port":443}]}`, core.TrafficProtocolBoth},
		{"singbox-unknown-inbound-hint", core.EngineSingBox, `{"inbounds":[{"tag":"future","type":"future","listen_port":443,"network":"udp"}]}`, core.TrafficProtocolBoth},
		{"singbox-unknown-endpoint-hint", core.EngineSingBox, `{"endpoints":[{"tag":"future","type":"future","listen_port":443,"network":"udp"}]}`, core.TrafficProtocolBoth},
		{"singbox-inbound-type-in-endpoints", core.EngineSingBox, `{"endpoints":[{"tag":"future","type":"vless","listen_port":443}]}`, core.TrafficProtocolBoth},
		{"singbox-unknown-transport", core.EngineSingBox, `{"inbounds":[{"tag":"web","type":"vless","listen_port":443,"transport":{"type":"future"}}]}`, core.TrafficProtocolBoth},
		{"singbox-shadowsocks-network", core.EngineSingBox, `{"inbounds":[{"tag":"ss","type":"shadowsocks","listen_port":443,"network":["udp"]}]}`, core.TrafficProtocolUDP},
		{"singbox-direct-network", core.EngineSingBox, `{"inbounds":[{"tag":"direct","type":"direct","listen_port":443,"network":"tcp"}]}`, core.TrafficProtocolTCP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := accountingListenerProtocols(tc.engine, tc.content)[443]
			if got != tc.want {
				t.Fatalf("verified listener protocol = %s, want %s", got, tc.want)
			}
		})
	}
}
