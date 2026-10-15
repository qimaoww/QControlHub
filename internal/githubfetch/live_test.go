package githubfetch

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Run inside an IPv6-only namespace with usable system DNS. Failure injection
// affects only auxiliary observations; HTTPS/downloads use the real transport.
func TestLiveIPv6ProbeFailureRecovery(t *testing.T) {
	if os.Getenv("QCH_TEST_LIVE_GITHUB") != "1" {
		t.Skip("opt-in real public IPv6 resources")
	}
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "auxiliary DNS64 timeout", true: "Unknown then direct failure"}[unknown], func(t *testing.T) {
			probes := systemProbes()
			for i, p := range probes {
				if p.kind == "dns64" {
					probes[i].run = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
					probes[i].budget = 30 * time.Second
				}
				if unknown && p.kind == "dns6" {
					probes[i].run = func(context.Context) error {
						return &net.DNSError{Name: "auxiliary probe", Err: "injected timeout", IsTimeout: true}
					}
				}
			}
			detector := &Detector{probe: func(ctx context.Context) Mode { return probeNetworkWith(ctx, probes) }}
			direct, channel := 0, 0
			base := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
			defer base.CloseIdleConnections()
			tr := &Transport{Detect: detector.Detect, Invalidate: detector.Invalidate, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasPrefix(r.URL.String(), Prefix) {
					channel++
				} else {
					direct++
				}
				return base.RoundTrip(r)
			})}
			client := &http.Client{Transport: tr, Timeout: 20 * time.Second}
			for _, source := range []string{"https://api.github.com/repos/MetaCubeX/mihomo/releases/latest", "https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/go.mod"} {
				response, err := client.Get(source)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
				response.Body.Close()
				if err != nil || response.StatusCode != 200 {
					t.Fatalf("status=%d err=%v body=%s", response.StatusCode, err, body[:min(len(body), 200)])
				}
			}
			wantDirect := 0
			if unknown {
				wantDirect = 1
			}
			if direct != wantDirect || channel != 2 {
				t.Fatalf("direct=%d channel=%d", direct, channel)
			}
			t.Logf("actual HTTPS resources: direct=%d acceleration=%d", direct, channel)
		})
	}
}
