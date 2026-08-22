package api

import (
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientAddressCandidatesFollowManagedObservedAndInterfacePriority(t *testing.T) {
	agent := core.Agent{
		Labels: map[string]string{
			"client_address": "Node.Example.COM.",
			"public_host":    "Public.Example.COM.",
			"public_ip":      "1.1.1.1",
		},
		Metrics: core.HostMetrics{
			ObservedPublicIP: "93.184.216.34",
			NetworkInterfaces: []core.HostNetworkInterface{{
				Name: "eth0", Addresses: []string{"10.0.0.8"},
			}},
		},
	}

	candidates := clientAddressCandidates(agent)
	if len(candidates) != 5 {
		t.Fatalf("client address candidates = %+v", candidates)
	}
	if candidates[0].address != "Node.Example.COM." || candidates[0].source != "手动设置" {
		t.Fatalf("managed client address = %+v", candidates[0])
	}
	if candidates[1].source != "节点公网域名" || candidates[2].source != "节点公网 IP" || candidates[3].source != "控制面实时观测公网地址" || candidates[4].source != "Agent 默认路由接口 eth0" {
		t.Fatalf("client address sources = %+v", candidates)
	}

	delete(agent.Labels, "client_address")
	delete(agent.Labels, "public_host")
	delete(agent.Labels, "public_ip")
	candidates = clientAddressCandidates(agent)
	if len(candidates) != 2 || candidates[0].address != "93.184.216.34" {
		t.Fatalf("restored automatic candidates = %+v", candidates)
	}

	agent.Metrics.ObservedPublicIP = "2606:4700:4700::1111"
	if changed := clientAddressCandidates(agent); len(changed) != 2 || changed[0].address != "2606:4700:4700::1111" {
		t.Fatalf("changed WSS source candidates = %+v", changed)
	}

	agent.Metrics.ObservedPublicIP = "198.51.100.9"
	candidates = clientAddressCandidates(agent)
	if len(candidates) != 1 || candidates[0].address != "10.0.0.8" || candidates[0].source != "Agent 默认路由接口 eth0" {
		t.Fatalf("private observation did not fall back to interface = %+v", candidates)
	}
}
