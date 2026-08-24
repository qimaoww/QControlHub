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
	if len(candidates) != 4 {
		t.Fatalf("client address candidates = %+v", candidates)
	}
	if candidates[0].address != "Node.Example.COM." || candidates[0].source != "手动设置" {
		t.Fatalf("managed client address = %+v", candidates[0])
	}
	if candidates[1].source != "节点公网域名" || candidates[2].source != "节点公网 IP" || candidates[3].source != "已验证连接来源 · IPv4" {
		t.Fatalf("client address sources = %+v", candidates)
	}

	delete(agent.Labels, "client_address")
	delete(agent.Labels, "public_host")
	delete(agent.Labels, "public_ip")
	candidates = clientAddressCandidates(agent)
	if len(candidates) != 1 || candidates[0].address != "93.184.216.34" || candidates[0].source != "已验证连接来源 · IPv4" {
		t.Fatalf("restored automatic candidates = %+v", candidates)
	}

	agent.Metrics.ObservedPublicIP = "2001:4860:4860::8888"
	if changed := clientAddressCandidates(agent); len(changed) != 1 || changed[0].address != "2001:4860:4860::8888" || changed[0].source != "已验证连接来源 · IPv6" {
		t.Fatalf("changed WSS source candidates = %+v", changed)
	}

	agent.Metrics.ObservedPublicIP = "172.69.135.152"
	if candidates := clientAddressCandidates(agent); len(candidates) != 0 {
		t.Fatalf("Cloudflare WSS relay surfaced as a client candidate = %+v", candidates)
	}

	// A private interface address must never become a node candidate.
	agent.Metrics.ObservedPublicIP = "198.51.100.9"
	candidates = clientAddressCandidates(agent)
	if len(candidates) != 0 {
		t.Fatalf("private interface or special-purpose observation surfaced = %+v", candidates)
	}

	// A truly public default-route interface address becomes the next fallback.
	agent.Metrics.ObservedPublicIP = ""
	agent.Metrics.NetworkInterfaces = []core.HostNetworkInterface{{
		Name: "eth0", Addresses: []string{"93.184.216.34"},
	}}
	candidates = clientAddressCandidates(agent)
	if len(candidates) != 1 || candidates[0].address != "93.184.216.34" || candidates[0].source != "Agent 默认路由接口 eth0" {
		t.Fatalf("public interface fallback = %+v", candidates)
	}
}

func TestClientAddressCandidatesIncludeProbedDualStackAddresses(t *testing.T) {
	agent := core.Agent{
		Metrics: core.HostMetrics{
			// The control plane only ever observes the family the connection
			// used; the probed pair must still surface both.
			ObservedPublicIP: "93.184.216.34",
			PublicIPv4:       "198.35.26.96",
			PublicIPv6:       "2001:4860:4860::8888",
		},
	}
	candidates := clientAddressCandidates(agent)
	if len(candidates) != 3 {
		t.Fatalf("probed candidates = %+v", candidates)
	}
	if candidates[0].address != "198.35.26.96" || candidates[0].source != "节点公网探测 · IPv4" {
		t.Fatalf("probed IPv4 candidate = %+v", candidates[0])
	}
	if candidates[1].address != "2001:4860:4860::8888" || candidates[1].source != "节点公网探测 · IPv6" {
		t.Fatalf("probed IPv6 candidate = %+v", candidates[1])
	}
	if candidates[2].address != "93.184.216.34" || candidates[2].source != "已验证连接来源 · IPv4" {
		t.Fatalf("observed candidate = %+v", candidates[2])
	}

	// A probed address that equals the observation must not duplicate it.
	agent.Metrics.PublicIPv4 = "93.184.216.34"
	candidates = clientAddressCandidates(agent)
	if len(candidates) != 2 {
		t.Fatalf("deduplicated candidates = %+v", candidates)
	}
}

func TestClientAddressCandidatesRejectCloudflareProbesAndKeepFallbacks(t *testing.T) {
	agent := core.Agent{Metrics: core.HostMetrics{
		PublicIPv4:       "172.69.135.152",
		PublicIPv6:       "::ffff:172.69.135.152",
		ObservedPublicIP: "93.184.216.34",
		NetworkInterfaces: []core.HostNetworkInterface{{
			Name: "eth0", Addresses: []string{"198.35.26.96", "2001:4860:4860::8888"},
		}},
	}}
	candidates := clientAddressCandidates(agent)
	if len(candidates) != 3 {
		t.Fatalf("Cloudflare probes did not fall back cleanly: %+v", candidates)
	}
	if candidates[0].address != "198.35.26.96" || candidates[0].source != "Agent 默认路由接口 eth0" {
		t.Fatalf("IPv4 interface fallback = %+v", candidates[0])
	}
	if candidates[1].address != "2001:4860:4860::8888" || candidates[1].source != "Agent 默认路由接口 eth0" {
		t.Fatalf("IPv6 interface fallback = %+v", candidates[1])
	}
	if candidates[2].address != "93.184.216.34" || candidates[2].source != "已验证连接来源 · IPv4" {
		t.Fatalf("verified WSS fallback = %+v", candidates[2])
	}
}

func TestClientAddressCandidatesKeepManualConnectionAheadOfAutomaticFamilies(t *testing.T) {
	agent := core.Agent{
		Labels: map[string]string{
			"client_address": "node.example.com",
			"public_ip":      "93.184.216.34",
		},
		Metrics: core.HostMetrics{
			PublicIPv4: "198.35.26.96",
			NetworkInterfaces: []core.HostNetworkInterface{{
				Name: "eth0", Addresses: []string{"2606:4700:4700::1111"},
			}},
		},
	}

	candidates := clientAddressCandidates(agent)
	if len(candidates) != 4 {
		t.Fatalf("manual and automatic candidates = %+v", candidates)
	}
	if candidates[0].address != "node.example.com" || candidates[0].source != "手动设置" {
		t.Fatalf("manual client address priority = %+v", candidates[0])
	}
	if candidates[1].address != "93.184.216.34" || candidates[1].source != "节点公网 IP" {
		t.Fatalf("manual public IP priority = %+v", candidates[1])
	}
	if candidates[2].address != "198.35.26.96" || candidates[2].source != "节点公网探测 · IPv4" {
		t.Fatalf("probe fallback priority = %+v", candidates[2])
	}
	if candidates[3].address != "2606:4700:4700::1111" || candidates[3].source != "Agent 默认路由接口 eth0" {
		t.Fatalf("interface fallback priority = %+v", candidates[3])
	}
}

func TestClientAddressCandidatesRejectNonRoutableInterfaceAddresses(t *testing.T) {
	agent := core.Agent{
		Metrics: core.HostMetrics{
			ObservedPublicIP: "93.184.216.34",
			NetworkInterfaces: []core.HostNetworkInterface{
				{Name: "tailscale0", Addresses: []string{"100.64.0.8"}},
				{Name: "docker0", Addresses: []string{"172.17.0.1"}},
				{Name: "lo", Addresses: []string{"127.0.0.1"}},
				{Name: "eth0", Addresses: []string{"192.0.2.9", "198.51.100.9", "203.0.113.9"}},
				{Name: "eth1", Addresses: []string{"2606:4700:4700::1111%eth1", "2001:db8::8", "fe80::1"}},
			},
		},
	}
	candidates := clientAddressCandidates(agent)
	if len(candidates) != 1 || candidates[0].address != "93.184.216.34" || candidates[0].source != "已验证连接来源 · IPv4" {
		t.Fatalf("non-routable interfaces leaked into candidates = %+v", candidates)
	}

	// A genuine public default-route IPv6 surface is kept on its family.
	agent.Metrics.ObservedPublicIP = ""
	agent.Metrics.NetworkInterfaces = []core.HostNetworkInterface{{
		Name: "eth0", Addresses: []string{"2606:4700:4700::1111"},
	}}
	candidates = clientAddressCandidates(agent)
	if len(candidates) != 1 || candidates[0].address != "2606:4700:4700::1111" || candidates[0].source != "Agent 默认路由接口 eth0" {
		t.Fatalf("public interface IPv6 did not surface = %+v", candidates)
	}
}

func TestClientAddressCandidatesPriorityAndDedup(t *testing.T) {
	agent := core.Agent{
		Metrics: core.HostMetrics{
			PublicIPv4:       "198.35.26.96",
			PublicIPv6:       "2001:4860:4860::8888",
			ObservedPublicIP: "93.184.216.34",
			NetworkInterfaces: []core.HostNetworkInterface{{
				Name: "eth0", Addresses: []string{"8.8.8.8"},
			}},
		},
	}
	candidates := clientAddressCandidates(agent)
	// probe IPv4, probe IPv6, then the distinct route-interface address, and
	// finally the verified WSS value.
	if len(candidates) != 4 {
		t.Fatalf("priority candidates = %+v", candidates)
	}
	if candidates[0].address != "198.35.26.96" || candidates[0].source != "节点公网探测 · IPv4" {
		t.Fatalf("probe IPv4 candidate = %+v", candidates[0])
	}
	if candidates[1].address != "2001:4860:4860::8888" || candidates[1].source != "节点公网探测 · IPv6" {
		t.Fatalf("probe IPv6 candidate = %+v", candidates[1])
	}
	if candidates[2].address != "8.8.8.8" || candidates[2].source != "Agent 默认路由接口 eth0" {
		t.Fatalf("route-interface candidate = %+v", candidates[2])
	}
	if candidates[3].address != "93.184.216.34" || candidates[3].source != "已验证连接来源 · IPv4" {
		t.Fatalf("verified WSS fallback source = %+v", candidates[3])
	}
}
