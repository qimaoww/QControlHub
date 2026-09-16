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
	write("'sing-box version 1.11.0'; printf '%s\\n' 'Tags: with_wireguard,with_gvisor'")
	if err := validateSingBoxCapabilities(context.Background(), binary, content); err != nil {
		t.Fatalf("rejected supported WireGuard build: %v", err)
	}
	write("'sing-box version 1.11.0'; printf '%s\\n' 'Tags: with_gvisor'")
	if err := validateSingBoxCapabilities(context.Background(), binary, content); err == nil {
		t.Fatal("accepted WireGuard endpoint without with_wireguard")
	}
}

func TestValidateSingBoxCapabilitiesDefaultsAndExactTags(t *testing.T) {
	for _, test := range []struct {
		name, content, output string
		allowed               bool
	}{
		{"WireGuard default stack", `{"endpoints":[{"type":"wireguard"}]}`, "sing-box version 1.11.0\nTags: with_wireguard", false},
		{"OpenVPN default stack", `{"endpoints":[{"type":"openvpn-server"}]}`, "sing-box version 1.14.0\nTags: with_openvpn", false},
		{"explicit system stack", `{"endpoints":[{"type":"wireguard","system":true}]}`, "sing-box version 1.11.0\nTags: with_wireguard", true},
		{"tag suffix", `{"endpoints":[{"type":"wireguard"}]}`, "sing-box version 1.11.0\nTags: with_wireguard_disabled,with_gvisor", false},
		{"gvisor suffix", `{"endpoints":[{"type":"wireguard"}]}`, "sing-box version 1.11.0\nTags: with_wireguard,with_gvisor_disabled", false},
		{"tag outside Tags line", `{"endpoints":[{"type":"wireguard"}]}`, "sing-box version 1.11.0\nTags: with_gvisor\nRevision: with_wireguard", false},
		{"invalid system flag", `{"endpoints":[{"type":"wireguard","system":"false"}]}`, "sing-box version 1.14.0\nTags: with_wireguard,with_gvisor", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "sing-box")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' '"+test.output+"'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			err := validateSingBoxCapabilities(context.Background(), binary, test.content)
			if (err == nil) != test.allowed {
				t.Fatalf("allowed=%v, error=%v", test.allowed, err)
			}
		})
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
