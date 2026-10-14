package githubfetch

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNetworkEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    evidence
		want Mode
	}{
		{"IPv4", evidence{v4: true}, IPv4},
		{"dual", evidence{v4: true, v6: true, route6: true, dns6: true}, DualStack},
		{"IPv6", evidence{v6: true, route6: true, dns6: true, v4Done: 2, githubDone: true}, IPv6Only},
		{"offline", evidence{route6: true, dns6: true}, Unknown},
		{"DNS failure", evidence{v6: true, route6: true}, Unknown},
		{"address without route", evidence{v6: true, dns6: true}, Unknown},
		{"working NAT64", evidence{v6: true, route6: true, dns6: true, dns64: true, translatedGitHub: true}, NAT64},
		{"native GitHub IPv6", evidence{v6: true, route6: true, dns6: true, translatedGitHub: true}, IPv6Direct},
		{"DNS64 without working translator", evidence{v6: true, route6: true, dns6: true, dns64: true, v4Done: 2, githubDone: true}, IPv6Only},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.e); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestDetectorSharesCacheExpiresAndCancels(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	d := &Detector{probe: func(ctx context.Context) Mode {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return IPv6Only
		case <-ctx.Done():
			return Unknown
		}
	}}
	var wg sync.WaitGroup
	wg.Go(func() {
		if got := d.Detect(context.Background()); got != IPv6Only {
			t.Errorf("got %s", got)
		}
	})
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := d.Detect(ctx); got != Unknown {
		t.Fatalf("canceled waiter=%s", got)
	}
	for range 12 {
		wg.Go(func() {
			if d.Detect(context.Background()) != IPv6Only {
				t.Error("shared detection failed")
			}
		})
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("probes=%d", calls.Load())
	}
	d.mu.Lock()
	d.expires = time.Time{}
	d.probe = func(context.Context) Mode { calls.Add(1); return Unknown }
	d.mu.Unlock()
	if d.Detect(context.Background()) != Unknown || calls.Load() != 2 {
		t.Fatal("cache did not expire")
	}
	if ttl := time.Until(d.expires); ttl > 15*time.Second || ttl < 10*time.Second {
		t.Fatalf("unknown TTL=%v", ttl)
	}
}

func TestCanceledProbeDoesNotCacheNetworkDecision(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Detector{probe: func(ctx context.Context) Mode { cancel(); <-ctx.Done(); return Unknown }}
	if d.Detect(ctx) != Unknown || !d.expires.IsZero() {
		t.Fatal("canceled probe cached a decision")
	}
	d.probe = func(context.Context) Mode { return IPv6Only }
	if d.Detect(context.Background()) != IPv6Only {
		t.Fatal("canceled detection blocked recovery")
	}
}

// These fixtures exercise the collector used by system probes, rather than
// stubbing Detect to return IPv6Only.
func fixtureProbes(dnsWorks bool) []networkProbe {
	ok := func(context.Context) error { return nil }
	fail := func(context.Context) error { return &net.DNSError{Name: "fixture", Err: "no records"} }
	slow := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	dns := fail
	if dnsWorks {
		dns = ok
	}
	return []networkProbe{
		{"v4a", "v4", 10 * time.Millisecond, slow}, {"v4b", "v4", time.Second, fail},
		{"v6a", "v6", time.Second, ok}, {"v6b", "v6", time.Second, fail},
		{"route6", "route6", time.Second, ok}, {"dns6", "dns6", time.Second, dns},
		{"github6", "translated", time.Second, fail}, {"dns64", "dns64", time.Second, slow},
	}
}
func TestAuxiliaryTimeoutRetainsPartialEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if got := probeNetworkWith(ctx, fixtureProbes(true)); got != IPv6Only {
		t.Fatalf("partial evidence lost: %s", got)
	}
	if ctx.Err() != nil {
		t.Fatal("waited for auxiliary deadline")
	}
	probes := fixtureProbes(true)
	// Interrupted IPv4 probes are not proof of an IPv6-only environment.
	for i := 0; i < 2; i++ {
		probes[i].budget = time.Second
		probes[i].run = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	}
	ctx2, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if got := probeNetworkWith(ctx2, probes); got != Unknown {
		t.Fatalf("unfinished probes misclassified: %s", got)
	}
}
func TestInvalidationDuringProbeDoesNotCacheStaleResult(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	d := &Detector{probe: func(context.Context) Mode { close(started); <-release; return IPv6Only }}
	done := make(chan Mode, 1)
	go func() { done <- d.Detect(context.Background()) }()
	<-started
	d.Invalidate()
	close(release)
	<-done
	if !d.expires.IsZero() {
		t.Fatal("stale probe repopulated cache")
	}
	d.probe = func(context.Context) Mode { return IPv4 }
	if d.Detect(context.Background()) != IPv4 {
		t.Fatal("network change not detected")
	}
}
