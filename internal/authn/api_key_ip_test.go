package authn

import (
	"net/http/httptest"
	"testing"
)

func TestAuthenticationClientIPFailsClosedAndAcceptsPrivateSources(t *testing.T) {
	proxies, _ := ParseTrustedProxies([]string{"10.0.0.1/32", "10.0.0.2/32"})
	for _, tc := range []struct{ remote, xff, want string }{
		{"192.168.0.2:42", "127.0.0.1", "192.168.0.2"},
		{"10.0.0.2:42", "192.168.0.8, 10.0.0.1", "192.168.0.8"},
		{"10.0.0.2:42", "127.0.0.1, 192.168.0.8, 10.0.0.1", "192.168.0.8"},
		{"10.0.0.2:42", "::ffff:192.168.0.8, 10.0.0.1", "192.168.0.8"},
		{"10.0.0.2:42", "fd00::42, 10.0.0.1", "fd00::42"},
		{"[::1]:42", "192.0.2.5", "::1"},
		{"10.0.0.2:42", "invalid, 10.0.0.1", ""},
		{"not-an-ip", "127.0.0.1", ""},
		{"10.0.0.2:42", "", "10.0.0.2"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		r.Header.Set("X-Real-IP", "127.0.0.1")
		r.Header.Set("Forwarded", "for=127.0.0.1")
		if got := ClientIP(r, proxies); got != tc.want {
			t.Errorf("%s via %q got %q want %q", tc.remote, tc.xff, got, tc.want)
		}
	}
}
