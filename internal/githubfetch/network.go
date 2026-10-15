package githubfetch

import (
	"context"
	"crypto/tls"
	"errors"
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
	v4Done, v6Done                                int
	githubDone                                    bool
}

func classify(e evidence) Mode {
	if e.v4 && e.v6 {
		return DualStack
	}
	if e.v4 {
		return IPv4
	}
	if e.v6 && e.route6 {
		if e.translatedGitHub {
			if e.dns64 {
				return NAT64
			}
			return IPv6Direct
		}
		if e.dns6 && e.v4Done == 2 && e.githubDone {
			return IPv6Only
		}
	}
	return Unknown
}

// Detector is scoped to this process/network namespace. Invalidation also
// prevents an in-flight probe from repopulating a stale cache.
type Detector struct {
	mu         sync.Mutex
	mode       Mode
	expires    time.Time
	running    chan struct{}
	generation uint64
	probe      func(context.Context) Mode
}

var localDetector = &Detector{probe: probeNetwork}

func (d *Detector) Invalidate() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.expires = time.Time{}
	d.generation++
}
func (d *Detector) Detect(ctx context.Context) Mode {
	for {
		if ctx.Err() != nil {
			return Unknown
		}
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
		generation := d.generation
		d.mu.Unlock()
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		mode := d.probe(probeCtx)
		cancel()
		d.mu.Lock()
		if ctx.Err() == nil && generation == d.generation {
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

type networkProbe struct {
	name, kind string
	budget     time.Duration
	run        func(context.Context) error
}
type probeResult struct{ name, kind, state string }

var errProbeBudget = errors.New("independent network probe budget exhausted")

func probeNetwork(ctx context.Context) Mode { return probeNetworkWith(ctx, systemProbes()) }
func systemProbes() []networkProbe {
	var probes []networkProbe
	for _, p := range []struct{ kind, host, address string }{
		{"v4", "one.one.one.one", "1.1.1.1:443"}, {"v4", "dns.google", "8.8.8.8:443"},
		{"v6", "one.one.one.one", "[2606:4700:4700::1111]:443"}, {"v6", "dns.google", "[2001:4860:4860::8888]:443"},
	} {
		probes = append(probes, networkProbe{p.kind + "/" + p.host, p.kind, 2500 * time.Millisecond, func(ctx context.Context) error { return tlsProbe(ctx, "tcp", p.host, p.address) }})
	}
	for _, p := range []struct {
		kind, host string
		budget     time.Duration
	}{
		{"dns6", "v6.gh-proxy.org", 1500 * time.Millisecond}, {"dns64", "ipv4only.arpa", 750 * time.Millisecond},
	} {
		probes = append(probes, networkProbe{p.kind, p.kind, p.budget, func(ctx context.Context) error {
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip6", p.host)
			if err != nil {
				return err
			}
			if len(ips) == 0 {
				return errors.New("no AAAA records")
			}
			return nil
		}})
	}
	probes = append(probes, networkProbe{"github6", "translated", 2500 * time.Millisecond, func(ctx context.Context) error { return tlsProbe(ctx, "tcp6", "api.github.com", "api.github.com:443") }},
		networkProbe{"route6", "route6", 500 * time.Millisecond, func(ctx context.Context) error {
			conn, err := (&net.Dialer{}).DialContext(ctx, "udp6", "[2606:4700:4700::1111]:443")
			if err == nil {
				conn.Close()
			}
			return err
		}})
	return probes
}

// Every probe owns a budget. Pending auxiliary evidence never erases completed
// observations, and DNS64 only labels a proven direct IPv6 GitHub path.
func probeNetworkWith(ctx context.Context, probes []networkProbe) Mode {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan probeResult, len(probes))
	states := make(map[string]string, len(probes))
	for _, p := range probes {
		states[p.name] = "pending"
		go func() {
			probeCtx, stop := context.WithTimeoutCause(ctx, p.budget, errProbeBudget)
			defer stop()
			err := p.run(probeCtx)
			state := "success"
			if err != nil {
				state = "failed"
				if probeCtx.Err() != nil {
					state = "unfinished"
					if errors.Is(context.Cause(probeCtx), errProbeBudget) {
						state = "timeout"
					}
				}
			}
			results <- probeResult{p.name, p.kind, state}
		}()
	}
	e := evidence{}
	apply := func(r probeResult) {
		states[r.name] = r.state
		ok := r.state == "success"
		// An independent timeout is a bounded negative observation. Parent
		// cancellation only interrupts a probe and cannot rule out that route.
		complete := r.state != "unfinished"
		switch r.kind {
		case "v4":
			e.v4 = e.v4 || ok
			if complete {
				e.v4Done++
			}
		case "v6":
			e.v6 = e.v6 || ok
			if complete {
				e.v6Done++
			}
		case "route6":
			e.route6 = ok
		case "dns6":
			e.dns6 = ok
		case "dns64":
			e.dns64 = ok
		case "translated":
			e.translatedGitHub = ok
			e.githubDone = complete
		}
	}
	finish := func() Mode {
		mode := classify(e)
		slog.Info("GitHub network evidence", "network", mode, "probes", states, "ipv4_tls", e.v4, "ipv6_tls", e.v6, "ipv6_route", e.route6, "proxy_aaaa", e.dns6, "dns64", e.dns64, "github_ipv6_tls", e.translatedGitHub)
		return mode
	}
	for completed := 0; completed < len(probes); completed++ {
		select {
		case r := <-results:
			apply(r)
			if (e.v4 && (e.v6 || e.v6Done == 2)) || (e.v6 && e.route6 && e.v4Done == 2 && e.githubDone && (e.dns6 || e.translatedGitHub)) {
				return finish()
			}
		case <-ctx.Done():
			// A ready result and the deadline can race. Drain evidence before deciding.
			for {
				select {
				case r := <-results:
					apply(r)
				default:
					return finish()
				}
			}
		}
	}
	return finish()
}
func tlsProbe(ctx context.Context, network, host, address string) error {
	dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
	conn, err := dialer.DialContext(ctx, network, address)
	if err == nil {
		conn.Close()
	}
	return err
}
