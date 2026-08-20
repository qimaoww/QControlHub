package api

import (
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientAddressCandidatesPreferManagedAddress(t *testing.T) {
	agent := core.Agent{
		Labels: map[string]string{
			"client_address": "Node.Example.COM.",
			"public_ip":      "203.0.113.8",
		},
		Metrics: core.HostMetrics{NetworkInterfaces: []core.HostNetworkInterface{{
			Name: "eth0", Addresses: []string{"192.0.2.20"},
		}}},
	}

	candidates := clientAddressCandidates(agent)
	if len(candidates) != 3 {
		t.Fatalf("client address candidates = %+v", candidates)
	}
	if candidates[0].address != "Node.Example.COM." || candidates[0].source != "手动设置" {
		t.Fatalf("managed client address = %+v", candidates[0])
	}
	if candidates[1].source != "节点公网 IP" || candidates[2].source != "节点网络接口 eth0" {
		t.Fatalf("client address sources = %+v", candidates)
	}
}
