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
		"3fff::1", "5f00::1", "fe80::1", "::", "not-an-address",
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
