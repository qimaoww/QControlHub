package webui

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestClientAccessCacheKeyStableAndDistinct(t *testing.T) {
	t.Parallel()
	first := clientAccessCacheKey("agt_0123456789abcdef", core.EngineXray, "content-a")
	again := clientAccessCacheKey("agt_0123456789abcdef", core.EngineXray, "content-a")
	if first != again {
		t.Fatalf("cache key is not stable: %q vs %q", first, again)
	}
	if !strings.Contains(first, "agt_0123456789abcdef|xray|") {
		t.Fatalf("cache key lost identity prefix: %q", first)
	}
	otherContent := clientAccessCacheKey("agt_0123456789abcdef", core.EngineXray, "content-b")
	otherEngine := clientAccessCacheKey("agt_0123456789abcdef", core.EngineMihomo, "content-a")
	if first == otherContent || first == otherEngine {
		t.Fatal("cache keys collide across inputs")
	}
}

func TestClientAccessForCachesByContentDigest(t *testing.T) {
	t.Parallel()
	server := &Server{clientAccessCache: make(map[string]clientAccessData, 8)}
	input := serverconfig.Input{
		Protocol: serverconfig.ProtocolTrojan, Tag: "cached-trojan", Listen: "0.0.0.0", Port: 24443,
		Username: "relay-user", Credential: "correct-horse-battery-staple", Transport: "raw", TLSEnabled: true,
		CertificatePath: "/server-only/certificate.pem", PrivateKeyPath: "/server-only/private-key.pem",
	}
	content, err := serverconfig.Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	agent := core.Agent{
		ID: "agt_0123456789abcdef", Name: "test-node", Status: "online",
		Metrics: core.HostMetrics{NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.168.31.205"}}}},
	}
	first := server.clientAccessFor(agent, core.EngineXray, content)
	if len(first.Profiles) != 1 {
		t.Fatalf("first parse produced %d profiles", len(first.Profiles))
	}
	if len(server.clientAccessCache) != 1 {
		t.Fatalf("cache size = %d after first parse, want 1", len(server.clientAccessCache))
	}
	second := server.clientAccessFor(agent, core.EngineXray, content)
	if len(server.clientAccessCache) != 1 {
		t.Fatalf("cache size = %d after repeated parse, want 1 (hit)", len(server.clientAccessCache))
	}
	if second.Address != first.Address || second.Profiles[0].Profile.URI != first.Profiles[0].Profile.URI {
		t.Fatal("cached result differs from the first parse")
	}

	// A different content digest must not reuse the cached entry.
	changedInput := input
	changedInput.Port = 24444
	otherContent, err := serverconfig.Generate(core.EngineXray, changedInput)
	if err != nil {
		t.Fatal(err)
	}
	other := server.clientAccessFor(agent, core.EngineXray, otherContent)
	if len(other.Profiles) != 1 || other.Profiles[0].Profile.URI == first.Profiles[0].Profile.URI {
		t.Fatalf("changed content unexpectedly matched the cache: %+v", other)
	}
	if len(server.clientAccessCache) != 2 {
		t.Fatalf("cache size = %d after distinct content, want 2", len(server.clientAccessCache))
	}
}
