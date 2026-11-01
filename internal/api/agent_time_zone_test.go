package api

import (
	"net/netip"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAgentTimeZoneAddressTrustAndFamilyOrder(t *testing.T) {
	tests := []struct {
		name  string
		agent core.Agent
		want  string
	}{
		{
			name: "IPv4 before IPv6",
			agent: core.Agent{Metrics: core.HostMetrics{
				PublicIPv4: "8.8.8.8", PublicIPv4Source: core.PublicIPProbeSourceAgent,
				PublicIPv6: "2001:4860:4860::8888", PublicIPv6Source: core.PublicIPProbeSourceAgent,
			}},
			want: "8.8.8.8",
		},
		{
			name: "invalid IPv4 falls back to IPv6",
			agent: core.Agent{Metrics: core.HostMetrics{
				PublicIPv4: "192.168.1.2", PublicIPv4Source: core.PublicIPProbeSourceAgent,
				PublicIPv6: "2001:4860:4860::8888", PublicIPv6Source: core.PublicIPProbeSourceAgent,
			}},
			want: "2001:4860:4860::8888",
		},
		{
			name: "untrusted probes ignored",
			agent: core.Agent{Metrics: core.HostMetrics{
				PublicIPv4: "8.8.8.8", PublicIPv4Source: "untrusted-source",
				PublicIPv6: "2001:4860:4860::8888", PublicIPv6Source: "untrusted-source",
				ObservedPublicIP: "1.1.1.1",
			}},
			want: "1.1.1.1",
		},
		{
			name: "default route interface fallback",
			agent: core.Agent{Metrics: core.HostMetrics{
				ObservedPublicIP:  "8.8.8.8",
				NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.168.0.2", "9.9.9.9"}}},
			}},
			want: "9.9.9.9",
		},
		{
			name:  "verified WSS address is last fallback",
			agent: core.Agent{Metrics: core.HostMetrics{ObservedPublicIP: "8.8.4.4"}},
			want:  "8.8.4.4",
		},
		{
			name: "private and missing addresses rejected",
			agent: core.Agent{Metrics: core.HostMetrics{
				PublicIPv4: "10.0.0.1", PublicIPv4Source: core.PublicIPProbeSourceAgent,
				ObservedPublicIP: "127.0.0.1",
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := agentTimeZoneAddress(test.agent)
			if test.want == "" {
				if got.IsValid() {
					t.Fatalf("selected address %s", got)
				}
				return
			}
			if got != netip.MustParseAddr(test.want) {
				t.Fatalf("selected address = %s, want %s", got, test.want)
			}
		})
	}
}

func TestAgentTimeZoneCommandNeededPreservesLookupFailure(t *testing.T) {
	state := core.AgentTimeZoneState{Mode: "auto", TimeZone: "Asia/Shanghai", Revision: 4, Status: "failed", Error: core.AgentTimeZoneLookupFailure}
	if agentTimeZoneCommandNeeded(state, 3, true) {
		t.Fatal("automatic lookup failure sent the previous time zone as a success candidate")
	}
	state.Status, state.Error = "pending", ""
	if !agentTimeZoneCommandNeeded(state, 3, false) {
		t.Fatal("new pending revision was not sent")
	}
	if agentTimeZoneCommandNeeded(state, 4, false) {
		t.Fatal("acknowledged revision was sent again")
	}
}
