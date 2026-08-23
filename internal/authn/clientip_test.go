package authn

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPOnlyTrustsForwardingChainFromConfiguredProxy(t *testing.T) {
	outerProxy := "10.80.0.1"
	webProxy := "10.80.0.2"
	proxies, err := ParseTrustedProxies([]string{outerProxy + "/32", webProxy + "/32"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies() error = %v", err)
	}
	forwardedChain := "192.0.2.99, 93.184.216.34, " + outerProxy

	partialTrust, err := ParseTrustedProxies([]string{outerProxy + "/32"})
	if err != nil {
		t.Fatal(err)
	}
	untrusted := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	untrusted.RemoteAddr = webProxy + ":443"
	untrusted.Header.Set("X-Forwarded-For", forwardedChain)
	if got := ClientIP(untrusted, partialTrust); got != webProxy {
		t.Fatalf("untrusted direct web proxy changed ClientIP() to %q", got)
	}

	trusted := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	trusted.RemoteAddr = webProxy + ":443"
	trusted.Header.Set("X-Forwarded-For", forwardedChain)
	if got := ClientIP(trusted, proxies); got != "93.184.216.34" {
		t.Fatalf("complete two-hop chain ClientIP() = %q", got)
	}
	if got := PublicClientIP(trusted, proxies); got != "93.184.216.34" {
		t.Fatalf("forged leftmost value crossed the real Agent source: %q", got)
	}
}

func TestPublicClientIPNormalizesOnlyTrustedPublicSources(t *testing.T) {
	proxies, err := ParseTrustedProxies([]string{"10.0.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}

	trusted := httptest.NewRequest("GET", "https://control.example", nil)
	trusted.RemoteAddr = "10.0.0.7:443"
	trusted.Header.Set("X-Forwarded-For", "::ffff:93.184.216.34")
	if got := PublicClientIP(trusted, proxies); got != "93.184.216.34" {
		t.Fatalf("trusted public source = %q", got)
	}

	untrusted := httptest.NewRequest("GET", "https://control.example", nil)
	untrusted.RemoteAddr = "93.184.216.34:443"
	untrusted.Header.Set("X-Forwarded-For", "1.1.1.1")
	if got := PublicClientIP(untrusted, proxies); got != "93.184.216.34" {
		t.Fatalf("untrusted forwarding header changed public source to %q", got)
	}

	for _, forwarded := range []string{"10.0.0.8", "not-an-address"} {
		request := httptest.NewRequest("GET", "https://control.example", nil)
		request.RemoteAddr = "10.0.0.7:443"
		request.Header.Set("X-Forwarded-For", forwarded)
		if got := PublicClientIP(request, proxies); got != "" {
			t.Errorf("trusted proxy accepted non-public forwarded source %q as %q", forwarded, got)
		}
	}

	for _, source := range []string{
		"10.0.0.8", "100.64.0.1", "127.0.0.1", "169.254.10.20",
		"192.0.2.1", "192.31.196.1", "192.52.193.1", "192.175.48.1",
		"198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1",
		"::1", "64:ff9b::1", "100:0:0:1::1", "2001:db8::1", "2620:4f:8000::1",
		"3fff::1", "5f00::1", "fe80::1", "2606:4700:4700::1111%eth0", "::", "not-an-address",
	} {
		request := httptest.NewRequest("GET", "https://control.example", nil)
		request.RemoteAddr = source
		if got := PublicClientIP(request, nil); got != "" {
			t.Errorf("non-public source %q normalized to %q", source, got)
		}
	}

	for input, want := range map[string]string{
		"93.184.216.34":            "93.184.216.34",
		"[2606:4700:4700::1111]":   "2606:4700:4700::1111",
		"::ffff:93.184.216.34":     "93.184.216.34",
		" 2606:4700:4700:0::1111 ": "2606:4700:4700::1111",
	} {
		if got := NormalizePublicIP(input); got != want {
			t.Errorf("NormalizePublicIP(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestVerifiedAgentPublicIPResolvesOnlyUnambiguousPublicClients(t *testing.T) {
	outerProxy := "10.80.0.1"
	webProxy := "10.80.0.2"
	proxies, err := ParseTrustedProxies([]string{outerProxy + "/32", webProxy + "/32"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies() error = %v", err)
	}

	// A trusted two-hop chain with a single untrusted client resolves to it.
	clean := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	clean.RemoteAddr = webProxy + ":443"
	clean.Header.Set("X-Forwarded-For", "93.184.216.34, "+outerProxy)
	if got := VerifiedAgentPublicIP(clean, proxies); got != "93.184.216.34" {
		t.Fatalf("clean two-hop VerifiedAgentPublicIP() = %q", got)
	}

	// A public relay mixed with a real client to its left is proxy ambiguity,
	// so the relay must never be stored as the Agent address.
	relay := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	relay.RemoteAddr = webProxy + ":443"
	relay.Header.Set("X-Forwarded-For", "93.184.216.34, 2400:cb00::1")
	if got := VerifiedAgentPublicIP(relay, proxies); got != "" {
		t.Fatalf("untrusted relay resolved as Agent source %q", got)
	}

	// Once the CDN public prefix is explicitly trusted the full chain resolves
	// the real client rather than the edge hop.
	cdnConfigured, err := ParseTrustedProxies([]string{outerProxy + "/32", webProxy + "/32", "2400:cb00::/32"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies() CDN error = %v", err)
	}
	resolved := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	resolved.RemoteAddr = webProxy + ":443"
	resolved.Header.Set("X-Forwarded-For", "93.184.216.34, 2400:cb00::1")
	if got := VerifiedAgentPublicIP(resolved, cdnConfigured); got != "93.184.216.34" {
		t.Fatalf("CDN-configured VerifiedAgentPublicIP() = %q", got)
	}

	// An attacker-forged left value still means the rightmost untrusted hop
	// forwarded the request: fail closed rather than trusting a relay.
	forged := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	forged.RemoteAddr = webProxy + ":443"
	forged.Header.Set("X-Forwarded-For", "1.1.1.1, 93.184.216.34, "+outerProxy)
	if got := VerifiedAgentPublicIP(forged, proxies); got != "" {
		t.Fatalf("forged left edge resolved as Agent source %q", got)
	}

	// A direct untrusted public peer is accepted, but its attacker-controlled
	// forwarding header is never honored.
	direct := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	direct.RemoteAddr = "93.184.216.34:443"
	direct.Header.Set("X-Forwarded-For", "1.1.1.1")
	if got := VerifiedAgentPublicIP(direct, proxies); got != "93.184.216.34" {
		t.Fatalf("direct public VerifiedAgentPublicIP() = %q", got)
	}

	// Untrusted direct peers that are not globally routable are empty.
	private := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	private.RemoteAddr = "10.0.0.8:443"
	if got := VerifiedAgentPublicIP(private, nil); got != "" {
		t.Fatalf("private direct peer normalized as %q", got)
	}

	// A fully trusted chain carries no provable client and is ambiguous.
	allTrusted := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	allTrusted.RemoteAddr = webProxy + ":443"
	allTrusted.Header.Set("X-Forwarded-For", outerProxy)
	if got := VerifiedAgentPublicIP(allTrusted, proxies); got != "" {
		t.Fatalf("all-trusted chain resolved as %q", got)
	}

	// A malformed X-Forwarded-For entry must not be silently skipped.
	malformed := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	malformed.RemoteAddr = webProxy + ":443"
	malformed.Header.Set("X-Forwarded-For", "not-an-address")
	if got := VerifiedAgentPublicIP(malformed, proxies); got != "" {
		t.Fatalf("malformed chain resolved as %q", got)
	}
}
