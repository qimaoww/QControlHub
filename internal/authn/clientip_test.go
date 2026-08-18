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
