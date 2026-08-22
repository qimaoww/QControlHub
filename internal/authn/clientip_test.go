package authn

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPOnlyTrustsForwardingChainFromConfiguredProxy(t *testing.T) {
	proxies, err := ParseTrustedProxies([]string{"10.0.0.0/24", "192.0.2.20"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies() error = %v", err)
	}
	untrusted := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	untrusted.RemoteAddr = "203.0.113.5:1234"
	untrusted.Header.Set("X-Forwarded-For", "198.51.100.9")
	if got := ClientIP(untrusted, proxies); got != "203.0.113.5" {
		t.Fatalf("untrusted proxy ClientIP() = %q", got)
	}

	trusted := httptest.NewRequest("GET", "https://qcontrolhub.example", nil)
	trusted.RemoteAddr = "10.0.0.7:443"
	trusted.Header.Set("X-Forwarded-For", "198.51.100.9, 192.0.2.20")
	if got := ClientIP(trusted, proxies); got != "198.51.100.9" {
		t.Fatalf("trusted chain ClientIP() = %q", got)
	}
}

func TestPublicClientIPNormalizesOnlyTrustedPublicSources(t *testing.T) {
	proxies, err := ParseTrustedProxies([]string{"10.0.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}

	trusted := httptest.NewRequest("GET", "https://control.example", nil)
	trusted.RemoteAddr = "10.0.0.7:443"
	trusted.Header.Set("X-Forwarded-For", "::ffff:198.51.100.9")
	if got := PublicClientIP(trusted, proxies); got != "198.51.100.9" {
		t.Fatalf("trusted public source = %q", got)
	}

	untrusted := httptest.NewRequest("GET", "https://control.example", nil)
	untrusted.RemoteAddr = "203.0.113.7:443"
	untrusted.Header.Set("X-Forwarded-For", "198.51.100.10")
	if got := PublicClientIP(untrusted, proxies); got != "203.0.113.7" {
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

	for _, source := range []string{"10.0.0.8", "127.0.0.1", "169.254.10.20", "::1", "fe80::1", "::", "not-an-address"} {
		request := httptest.NewRequest("GET", "https://control.example", nil)
		request.RemoteAddr = source
		if got := PublicClientIP(request, nil); got != "" {
			t.Errorf("non-public source %q normalized to %q", source, got)
		}
	}
}
