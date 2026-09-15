package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestWireGuardPlanGenerateClientAndMetadata(t *testing.T) {
	p, ok := FindProtocol(core.EngineXray, ProtocolWireGuard)
	if !ok {
		t.Fatal("missing protocol")
	}
	in, err := NewPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	server, err := Generate(core.EngineXray, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.ValidateConfig(core.EngineXray, server); err != nil {
		t.Fatalf("xray rejected WireGuard config: %v", err)
	}
	if strings.Contains(server, in.WireGuardClientPrivateKey) {
		t.Fatal("client private key leaked into server config")
	}
	if !strings.Contains(server, `"protocol": "wireguard"`) || !strings.Contains(server, in.WireGuardClientPublicKey) {
		t.Fatalf("bad server config: %s", server)
	}
	profile, err := BuildClientProfile(in, "example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile.URI, "[Interface]") || !strings.Contains(profile.URI, in.WireGuardClientPrivateKey) {
		t.Fatalf("bad client config: %s", profile.URI)
	}
	meta, err := MarshalClientMetadata(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(meta, in.WireGuardServerPrivateKey) {
		t.Fatal("server private key leaked to metadata")
	}
	parsed := in
	parsed.WireGuardClientPrivateKey = ""
	if err := ApplyClientMetadata(&parsed, meta); err != nil {
		t.Fatal(err)
	}
	if parsed.WireGuardClientPrivateKey != in.WireGuardClientPrivateKey {
		t.Fatal("metadata did not restore private key")
	}
	parsedServer, ok := Parse(core.EngineXray, server)
	if !ok || parsedServer.WireGuardClientPublicKey != in.WireGuardClientPublicKey || parsedServer.WireGuardServerPrivateKey != in.WireGuardServerPrivateKey {
		t.Fatalf("xray WireGuard roundtrip failed: %+v %v", parsedServer, ok)
	}
}

func TestWireGuardRejectsInvalidClientCIDR(t *testing.T) {
	p, _ := FindProtocol(core.EngineXray, ProtocolWireGuard)
	in, _ := NewPlan(p)
	in.WireGuardClientAddress = "10.66.66.0/24"
	if _, err := Generate(core.EngineXray, in); err == nil {
		t.Fatal("expected CIDR rejection")
	}
}

func TestSingBoxWireGuardEndpointKeepsClientPrivateKeyOutOfServer(t *testing.T) {
	p, ok := FindProtocol(core.EngineSingBox, ProtocolWireGuard)
	if !ok {
		t.Fatal("missing sing-box WireGuard endpoint protocol")
	}
	in, err := NewPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	server, err := Generate(core.EngineSingBox, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.ValidateConfig(core.EngineSingBox, server); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(server, in.WireGuardClientPrivateKey) || !strings.Contains(server, `"endpoints"`) {
		t.Fatalf("invalid endpoint output: %s", server)
	}
	all := ParseAll(core.EngineSingBox, server)
	if len(all) != 1 || all[0].Protocol != ProtocolWireGuard || all[0].WireGuardServerAddress != in.WireGuardServerAddress {
		t.Fatalf("parsed endpoint = %#v", all)
	}
}

func TestSingBoxEndpointClientProfiles(t *testing.T) {
	p, _ := FindProtocol(core.EngineSingBox, ProtocolOpenVPNServer)
	in, err := NewPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := BuildClientProfile(in, "vpn.example.com", "")
	if err != nil || !strings.Contains(profile.URI, "proto udp") || strings.Contains(profile.URI, in.OpenVPNServerKeyPath) {
		t.Fatalf("OpenVPN profile: %v %#v", err, profile)
	}
	ts, _ := NewPlan(mustProtocol(core.EngineSingBox, ProtocolTailscale))
	if _, err := BuildClientProfile(ts, "ts.example.com", ""); err == nil {
		t.Fatal("Tailscale unexpectedly exported a generic client profile")
	}
}

func mustProtocol(engine core.Engine, key string) Protocol {
	p, _ := FindProtocol(engine, key)
	return p
}
