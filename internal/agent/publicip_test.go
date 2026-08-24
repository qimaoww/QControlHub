package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestProbePublicIPEndpointValidatesResponse(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		status   int
		wantIPv4 bool
		want     string
	}{
		{name: "ipv4 echo", body: "93.184.216.34\n", status: http.StatusOK, wantIPv4: true, want: "93.184.216.34"},
		{name: "ipv4 echo without newline", body: "198.35.26.96", status: http.StatusOK, wantIPv4: true, want: "198.35.26.96"},
		{name: "ipv6 echo", body: "2001:4860:4860::8888\n", status: http.StatusOK, wantIPv4: false, want: "2001:4860:4860::8888"},
		{name: "wrong family rejected", body: "2001:4860:4860::8888\n", status: http.StatusOK, wantIPv4: true},
		{name: "v4-mapped v6 rejected for v6", body: "::ffff:93.184.216.34\n", status: http.StatusOK, wantIPv4: false},
		{name: "private address rejected", body: "10.0.0.8\n", status: http.StatusOK, wantIPv4: true},
		{name: "documentation range rejected", body: "2001:db8::1\n", status: http.StatusOK, wantIPv4: false},
		{name: "not an address", body: "your ip is 93.184.216.34\n", status: http.StatusOK, wantIPv4: true},
		{name: "html rejected", body: "<html>1.2.3.4</html>", status: http.StatusOK, wantIPv4: true},
		{name: "ipv4 with trailing junk", body: "93.184.216.34\njunk", status: http.StatusOK, wantIPv4: true},
		{name: "ipv4 after junk line", body: "junk\n93.184.216.34\n", status: http.StatusOK, wantIPv4: true},
		{name: "ipv4 with a second ip", body: "93.184.216.34\n1.1.1.1", status: http.StatusOK, wantIPv4: true},
		{name: "http error", body: "93.184.216.34", status: http.StatusTeapot, wantIPv4: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()
			got, err := probePublicIPEndpoint(context.Background(), server.Client(), server.URL, testCase.wantIPv4)
			if testCase.want == "" {
				if err == nil {
					t.Fatalf("expected rejection, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("got %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestProbePublicIPEndpointRejectsOversizedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, publicIPProbeMaxBodyBytes+2))
	}))
	defer server.Close()
	if got, err := probePublicIPEndpoint(context.Background(), server.Client(), server.URL, true); err == nil {
		t.Fatalf("expected oversized response to be rejected, got %q", got)
	}
}

func TestPublicIPProbeClientIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("198.35.26.96"))
	}))
	defer server.Close()
	if got, err := probePublicIPEndpoint(context.Background(), publicIPProbeClient("tcp4"), server.URL, true); err != nil || got != "198.35.26.96" {
		t.Fatalf("direct probe used environment proxy: address=%q error=%v", got, err)
	}
}

func TestPublicIPProberClearsFailedFamilyWithoutPollutingOtherFamily(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests > 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("198.35.26.10\n"))
	}))
	defer server.Close()
	prober := &PublicIPProber{
		httpV4: server.Client(), httpV6: server.Client(), interval: time.Minute,
		config: publicIPProbeRuntimeConfig{endpoints: [2]string{server.URL, ""}, source: core.PublicIPProbeSourceAgent},
		wake:   make(chan struct{}, 1),
	}
	prober.probeAll(context.Background())
	ipv4, ipv6 := prober.Snapshot()
	if ipv4 != "198.35.26.10" {
		t.Fatalf("expected direct endpoint result, got ipv4=%q ipv6=%q", ipv4, ipv6)
	}
	if ipv6 != "" {
		t.Fatalf("expected no IPv6 probe result, got %q", ipv6)
	}
	prober.probeAll(context.Background())
	if got, _ := prober.Snapshot(); got != "" {
		t.Fatalf("failed probe must clear the stale address, got %q", got)
	}
}

func TestPublicIPProberNilIsSafe(t *testing.T) {
	var prober *PublicIPProber
	if ipv4, ipv6 := prober.Snapshot(); ipv4 != "" || ipv6 != "" {
		t.Fatal("nil prober snapshot must be empty")
	}
	prober.Run(context.Background())
}

func TestSingleProbeEndpointDropsBlankAndRejectsFallbackLists(t *testing.T) {
	if got, err := singleProbeEndpoint(nil); err != nil || got != "" {
		t.Fatalf("nil endpoints must yield empty, got %q, %v", got, err)
	}
	if got, err := singleProbeEndpoint([]string{" ", "https://echo.example.com"}); err != nil || got != "https://echo.example.com" {
		t.Fatalf("blank endpoints must be dropped, got %q, %v", got, err)
	}
	if _, err := singleProbeEndpoint([]string{"https://one.example.com", "https://two.example.com"}); err == nil {
		t.Fatal("multiple endpoints must not create a silent fallback chain")
	}
}

func TestPublicIPProberEmptyEndpointsDisables(t *testing.T) {
	prober, err := NewPublicIPProber(0, nil, nil)
	if err != nil || prober.Enabled() {
		t.Fatalf("empty endpoint lists must disable probing, got %+v, %v", prober, err)
	}
	prober, err = NewPublicIPProber(0, []string{"https://echo.example.com"}, nil)
	if err != nil || !prober.Enabled() {
		t.Fatalf("a non-empty IPv4 endpoint must enable probing: %+v, %v", prober, err)
	}
}

func TestNormalizePublicIPProbeEvery(t *testing.T) {
	cases := map[time.Duration]time.Duration{
		0:                      defaultPublicIPProbeEvery,
		10 * time.Millisecond:  minPublicIPProbeEvery,
		time.Duration(1 << 62): maxPublicIPProbeEvery,
		30 * time.Minute:       30 * time.Minute,
	}
	for input, want := range cases {
		if got := normalizePublicIPProbeEvery(input); got != want {
			t.Fatalf("normalizePublicIPProbeEvery(%v) = %v, want %v", input, got, want)
		}
	}
}

func TestManagedPublicIPProbeConfigClearsCacheAndLocalConfigWins(t *testing.T) {
	prober, err := NewPublicIPProber(0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prober.addresses = [2]string{"198.35.26.96", "2001:4860:4860::8888"}
	prober.sources = [2]string{core.PublicIPProbeSourceControlPlane, core.PublicIPProbeSourceControlPlane}
	if err := prober.ApplyManagedConfig(core.PublicIPProbeConfig{IPv4Endpoint: "https://probe.example.test/v4", IntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if ipv4, ipv6, source4, source6 := prober.SnapshotWithSources(); ipv4 != "" || ipv6 != "" || source4 != "" || source6 != "" {
		t.Fatalf("changed config retained stale cache: %q %q %q %q", ipv4, ipv6, source4, source6)
	}

	local, err := NewPublicIPProber(0, []string{"https://local.example.test/v4"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.ApplyManagedConfig(core.PublicIPProbeConfig{IPv4Endpoint: "https://managed.example.test/v4"}); err != nil {
		t.Fatal(err)
	}
	local.mu.Lock()
	endpoint, source := local.config.endpoints[0], local.config.source
	local.mu.Unlock()
	if endpoint != "https://local.example.test/v4" || source != core.PublicIPProbeSourceAgent {
		t.Fatalf("managed config overrode local trust choice: endpoint=%q source=%q", endpoint, source)
	}
}

func TestManagedPublicIPProbeRunAppliesConfigImmediately(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("198.35.26.96"))
	}))
	defer server.Close()
	prober, err := NewPublicIPProber(0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prober.httpV4 = server.Client()
	prober.httpV6 = server.Client()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go prober.Run(ctx)
	if err := prober.ApplyManagedConfig(core.PublicIPProbeConfig{IPv4Endpoint: server.URL, IntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ipv4, _, source, _ := prober.SnapshotWithSources()
		if ipv4 == "198.35.26.96" && source == core.PublicIPProbeSourceControlPlane {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("managed config did not trigger an immediate verified probe")
}

func TestPublicIPProberCoalescesConcurrentRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			close(started)
		}
		<-release
		_, _ = w.Write([]byte("198.35.26.96"))
	}))
	defer server.Close()
	prober := &PublicIPProber{
		httpV4: server.Client(), httpV6: server.Client(), interval: time.Minute,
		config: publicIPProbeRuntimeConfig{endpoints: [2]string{server.URL, ""}, source: core.PublicIPProbeSourceAgent},
		wake:   make(chan struct{}, 1),
	}
	done := make(chan struct{}, 2)
	go func() { prober.probeAll(context.Background()); done <- struct{}{} }()
	<-started
	go func() { prober.probeAll(context.Background()); done <- struct{}{} }()
	close(release)
	<-done
	<-done
	if requests != 1 {
		t.Fatalf("concurrent refresh made %d requests, want 1", requests)
	}
}
