package githubfetch

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"sync"
	"time"
)

type Mode string

const (
	Unknown    Mode = "unknown"
	IPv4       Mode = "ipv4"
	DualStack  Mode = "dual-stack"
	IPv6Only   Mode = "ipv6-only"
	NAT64      Mode = "nat64-dns64"
	IPv6Direct Mode = "ipv6-github-direct"
)

type evidence struct {
	v4, v6, route6, dns6, dns64, translatedGitHub bool
}

func classify(e evidence) Mode {
	if e.v4 && e.v6 {
		return DualStack
	}
	if e.v4 {
		return IPv4
	}
	if e.v6 && e.route6 && e.dns6 {
		if e.dns64 && e.translatedGitHub {
			return NAT64
		}
		if e.translatedGitHub {
			return IPv6Direct
		}
		return IPv6Only
	}
	return Unknown
}

// Detector caches only in this process/network namespace. Concurrent callers
// share one bounded probe; canceled callers never wait for another probe.
type Detector struct {
	mu      sync.Mutex
	mode    Mode
	expires time.Time
	running chan struct{}
	probe   func(context.Context) Mode
}

var localDetector = &Detector{probe: probeNetwork}

func (d *Detector) Detect(ctx context.Context) Mode {
	for {
		d.mu.Lock()
		if time.Now().Before(d.expires) {
			mode := d.mode
			d.mu.Unlock()
			return mode
		}
		if waiting := d.running; waiting != nil {
			d.mu.Unlock()
			select {
			case <-waiting:
				continue
			case <-ctx.Done():
				return Unknown
			}
		}
		d.running = make(chan struct{})
		d.mu.Unlock()
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		mode := d.probe(probeCtx)
		cancel()
		d.mu.Lock()
		if ctx.Err() == nil {
			d.mode = mode
			ttl := 5 * time.Minute
			if mode == Unknown {
				ttl = 15 * time.Second
			}
			d.expires = time.Now().Add(ttl)
		}
		close(d.running)
		d.running = nil
		d.mu.Unlock()
		slog.Info("GitHub download network detected", "network", mode, "accelerated", mode == IPv6Only)
		return mode
	}
}

// TLS probes use two independent providers per family and no environment HTTP
// proxy. A local address or a failed GitHub request alone is never evidence.
func probeNetwork(ctx context.Context) Mode {
	type result struct {
		kind string
		ok   bool
	}
	results := make(chan result, 7)
	for _, p := range []struct{ kind, host, address string }{
		{"v4", "one.one.one.one", "1.1.1.1:443"},
		{"v4", "dns.google", "8.8.8.8:443"},
		{"v6", "one.one.one.one", "[2606:4700:4700::1111]:443"},
		{"v6", "dns.google", "[2001:4860:4860::8888]:443"},
	} {
		go func() { results <- result{p.kind, tlsReachable(ctx, "tcp", p.host, p.address)} }()
	}
	go func() {
		ips, _ := net.DefaultResolver.LookupIP(ctx, "ip6", "v6.gh-proxy.org")
		results <- result{"dns6", len(ips) > 0}
	}()
	go func() {
		ips, _ := net.DefaultResolver.LookupIP(ctx, "ip6", "ipv4only.arpa")
		results <- result{"dns64", len(ips) > 0}
	}()
	go func() {
		results <- result{"translated", tlsReachable(ctx, "tcp6", "api.github.com", "api.github.com:443")}
	}()
	e := evidence{}
	// UDP connect selects a route without sending a packet. Connectivity is
	// established separately by the TLS probes above.
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp6", "[2606:4700:4700::1111]:443")
	if err == nil {
		e.route6 = true
		conn.Close()
	}
	for range 7 {
		select {
		case r := <-results:
			switch r.kind {
			case "v4":
				e.v4 = e.v4 || r.ok
			case "v6":
				e.v6 = e.v6 || r.ok
			case "dns6":
				e.dns6 = r.ok
			case "dns64":
				e.dns64 = r.ok
			case "translated":
				e.translatedGitHub = r.ok
			}
		case <-ctx.Done():
			return Unknown
		}
	}
	slog.Debug("GitHub network evidence", "ipv4_tls", e.v4, "ipv6_tls", e.v6, "ipv6_route", e.route6, "proxy_aaaa", e.dns6, "dns64", e.dns64, "github_ipv6_tls", e.translatedGitHub)
	return classify(e)
}

func tlsReachable(ctx context.Context, network, host, address string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
	conn, err := dialer.DialContext(probeCtx, network, address)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
