package githubfetch

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func fixtureResponse(r *http.Request, status int) *http.Response {
	return &http.Response{Request: r, StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("resource"))}
}
func dnsTimeout(host string) error {
	return &net.DNSError{Name: host, Err: "i/o timeout", IsTimeout: true}
}
func TestProbeTransportLinkedRegression(t *testing.T) {
	for _, dnsWorks := range []bool{true, false} {
		t.Run(map[bool]string{true: "auxiliary timeout", false: "Unknown direct DNS timeout"}[dnsWorks], func(t *testing.T) {
			d := &Detector{probe: func(ctx context.Context) Mode { return probeNetworkWith(ctx, fixtureProbes(dnsWorks)) }}
			var direct, channel int
			tr := &Transport{Detect: d.Detect, Invalidate: d.Invalidate, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Hostname() == "v6.gh-proxy.org" {
					channel++
					if r.Header.Get("Authorization") != "" {
						t.Fatal("credentials leaked")
					}
					return fixtureResponse(r, 200), nil
				}
				direct++
				return nil, dnsTimeout(r.URL.Hostname())
			})}
			client := &http.Client{Transport: tr}
			for _, source := range []string{"https://api.github.com/repos/MetaCubeX/mihomo/releases/latest", "https://github.com/o/r/releases/download/v1/core.gz", "https://release-assets.githubusercontent.com/file"} {
				req, _ := http.NewRequest("GET", source, nil)
				req.Header.Set("Authorization", "secret")
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
			}
			wantDirect := 0
			if !dnsWorks {
				wantDirect = 1
			}
			if direct != wantDirect || channel != 3 {
				t.Fatalf("direct=%d channel=%d", direct, channel)
			}
		})
	}
}
func TestBothRoutesFailWithoutRepeatedDirectAttempts(t *testing.T) {
	d := &Detector{probe: func(ctx context.Context) Mode { return probeNetworkWith(ctx, fixtureProbes(false)) }}
	var hosts []string
	tr := &Transport{Detect: d.Detect, Invalidate: d.Invalidate, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Hostname())
		return nil, dnsTimeout(r.URL.Hostname())
	})}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/o/r/releases/latest", nil)
	_, err := tr.RoundTrip(req)
	if !Terminal(err) || len(hosts) != 2 || !d.expires.IsZero() {
		t.Fatalf("err=%v hosts=%v cache=%v", err, hosts, d.expires)
	}
	for _, part := range []string{"GitHub API domain resolution timed out", "acceleration:", "v6.gh-proxy.org", "IPv6 DNS resolver"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("missing %q: %v", part, err)
		}
	}
}
func TestFallbackDoesNotHandleHTTPOrTLSFailures(t *testing.T) {
	for _, status := range []int{403, 404, 429, 500} {
		count := 0
		tr := &Transport{Detect: func(context.Context) Mode { return Unknown }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) { count++; return fixtureResponse(r, status), nil })}
		req, _ := http.NewRequest("GET", "https://api.github.com/repos/o/r/releases/latest", nil)
		resp, err := tr.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if count != 1 || resp.StatusCode != status {
			t.Fatalf("status %d retried", status)
		}
	}
	count := 0
	tr := &Transport{Detect: func(context.Context) Mode { return Unknown }, Base: roundTripFunc(func(*http.Request) (*http.Response, error) { count++; return nil, x509.UnknownAuthorityError{} })}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/o/r/releases/latest", nil)
	if _, err := tr.RoundTrip(req); err == nil || count != 1 {
		t.Fatalf("TLS validation retried: %v", err)
	}
}
func TestCancellationAndHeaderBudget(t *testing.T) {
	for _, cancelCaller := range []bool{false, true} {
		calls := 0
		ctx, cancel := context.WithCancel(context.Background())
		tr := &Transport{HeaderBudget: 10 * time.Millisecond, Detect: func(context.Context) Mode { return Unknown }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				if cancelCaller {
					cancel()
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			return fixtureResponse(r, 200), nil
		})}
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/o/r/releases/latest", nil)
		resp, err := tr.RoundTrip(req)
		cancel()
		if cancelCaller {
			if !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("canceled request retried: %v calls=%d", err, calls)
			}
		} else {
			if err != nil || calls != 2 {
				t.Fatalf("route budget did not recover: %v", err)
			}
			resp.Body.Close()
		}
	}
	// The header budget must stop once a response arrives; archive bodies keep
	// the caller's total budget.
	tr := &Transport{HeaderBudget: time.Millisecond, Detect: func(context.Context) Mode { return IPv4 }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &contextBody{ctx: r.Context()}}, nil
	})}
	req, _ := http.NewRequest("GET", "https://github.com/o/r/file", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("header budget killed body: %v", err)
	}
	resp.Body.Close()
}

type contextBody struct{ ctx context.Context }

func (b *contextBody) Read([]byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return 0, io.EOF
}
func (b *contextBody) Close() error { return nil }
func TestWorkingChannelExpiresAndRecoversChangedNetwork(t *testing.T) {
	mode := Unknown
	calls := 0
	channelBroken := false
	d := &Detector{probe: func(context.Context) Mode { return mode }}
	tr := &Transport{Detect: d.Detect, Invalidate: d.Invalidate, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Hostname() == "v6.gh-proxy.org" {
			if channelBroken {
				return nil, dnsTimeout(r.URL.Hostname())
			}
			return fixtureResponse(r, 200), nil
		}
		if mode == Unknown {
			return nil, dnsTimeout(r.URL.Hostname())
		}
		return fixtureResponse(r, 200), nil
	})}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/o/r/releases/latest", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	mode = IPv4
	channelBroken = true
	resp, err = tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if calls != 4 {
		t.Fatalf("network change recovery calls=%d", calls)
	}
	if !tr.acceleratedUntil.IsZero() {
		t.Fatal("stale channel retained")
	}
}

func TestBrokenChannelBodyReprobesChangedNetwork(t *testing.T) {
	mode := Unknown
	d := &Detector{probe: func(context.Context) Mode { return mode }}
	direct, channel := 0, 0
	tr := &Transport{Detect: d.Detect, Invalidate: d.Invalidate, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "v6.gh-proxy.org" {
			channel++
			response := fixtureResponse(r, 200)
			response.Body = io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))
			return response, nil
		}
		direct++
		if mode == Unknown {
			return nil, dnsTimeout(r.URL.Hostname())
		}
		return fixtureResponse(r, 200), nil
	})}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/o/r/releases/latest", nil)
	response, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	// The channel worked through headers, then the network changed during
	// transfer. The caller's bounded retry must be able to discover direct IPv4.
	mode = IPv4
	_, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if !errors.Is(readErr, io.ErrUnexpectedEOF) {
		t.Fatalf("body error=%v", readErr)
	}
	response, err = tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if direct != 2 || channel != 1 {
		t.Fatalf("broken body retained channel: direct=%d channel=%d", direct, channel)
	}
}

func TestHeaderBudgetReportsOutstandingDNSAndPreservesTrace(t *testing.T) {
	traceCalls := 0
	tr := &Transport{HeaderBudget: time.Millisecond, Detect: func(context.Context) Mode { return Unknown }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "v6.gh-proxy.org" {
			return nil, dnsTimeout(r.URL.Hostname())
		}
		trace := httptrace.ContextClientTrace(r.Context())
		trace.DNSStart(httptrace.DNSStartInfo{Host: r.URL.Hostname()})
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/o/r/releases/latest", nil)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{DNSStart: func(httptrace.DNSStartInfo) { traceCalls++ }}))
	_, err := tr.RoundTrip(req)
	if !Terminal(err) || !strings.Contains(err.Error(), "GitHub API domain resolution timed out") || traceCalls != 1 {
		t.Fatalf("diagnosis=%v trace=%d", err, traceCalls)
	}
}

func TestLateHeadersDoNotEscapeRouteDeadline(t *testing.T) {
	calls := 0
	tr := &Transport{HeaderBudget: time.Millisecond, Detect: func(context.Context) Mode { return Unknown }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			<-r.Context().Done()
		}
		return fixtureResponse(r, 200), nil
	})}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/o/r/releases/latest", nil)
	response, err := tr.RoundTrip(req)
	if err != nil || calls != 2 {
		t.Fatalf("late headers bypassed fallback: calls=%d err=%v", calls, err)
	}
	response.Body.Close()
}
