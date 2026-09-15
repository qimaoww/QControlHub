package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSingBoxCapabilitiesChecksVersionAndTags(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "sing-box")
	write := func(version string) {
		t.Helper()
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' "+version+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	content := `{"endpoints":[{"type":"wireguard"}]}`
	write("'sing-box version 1.10.9'; printf '%s\\n' 'Tags: with_wireguard'")
	if err := validateSingBoxCapabilities(context.Background(), binary, content); err == nil {
		t.Fatal("accepted WireGuard endpoint on old sing-box")
	}
	write("'sing-box version 1.11.0'; printf '%s\\n' 'Tags: with_wireguard'")
	if err := validateSingBoxCapabilities(context.Background(), binary, content); err != nil {
		t.Fatalf("rejected supported WireGuard build: %v", err)
	}
	write("'sing-box version 1.11.0'; printf '%s\\n' 'Tags: with_gvisor'")
	if err := validateSingBoxCapabilities(context.Background(), binary, content); err == nil {
		t.Fatal("accepted WireGuard endpoint without with_wireguard")
	}
}

func TestValidateSingBoxCapabilitiesOpenVPNAndGVisor(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "sing-box")
	content := `{"endpoints":[{"type":"openvpn-server","system":false}]}`
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' 'sing-box version 1.14.0'; printf '%s\\n' 'Tags: with_openvpn'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateSingBoxCapabilities(context.Background(), binary, content); err == nil {
		t.Fatal("accepted system:false OpenVPN without with_gvisor")
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' 'sing-box version 1.14.0'; printf '%s\\n' 'Tags: with_openvpn,with_gvisor'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateSingBoxCapabilities(context.Background(), binary, content); err != nil {
		t.Fatalf("rejected supported OpenVPN build: %v", err)
	}
}

func TestValidateSingBoxCapabilitiesTailscaleRequiresGVisor(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "sing-box")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' 'sing-box version 1.14.0'; printf '%s\\n' 'Tags: with_tailscale'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateSingBoxCapabilities(context.Background(), binary, `{"endpoints":[{"type":"tailscale","system":true}]}`); err == nil {
		t.Fatal("accepted Tailscale endpoint without with_gvisor")
	}
}
