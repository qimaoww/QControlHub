package githubfetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

type Transport struct {
	Base       http.RoundTripper
	Detect     func(context.Context) Mode
	Invalidate func()
	// HeaderBudget bounds each route's DNS/connect/TLS/header phase, not the body.
	HeaderBudget     time.Duration
	mu               sync.Mutex
	acceleratedUntil time.Time
}

// Failure is terminal for the caller's retry loop: each eligible route has
// already been attempted within the same request budget. A new operation may
// reprobe after invalidation; quota/status/content errors remain with callers.
type Failure struct {
	Direct, Channel      error
	Host, Stage, Skipped string
	Category             string
}

func (e *Failure) Error() string {
	direct := "not attempted"
	if e.Direct != nil {
		direct = describeNetworkError(e.Host, e.Direct)
	}
	channel := "not attempted"
	if e.Channel != nil {
		channel = describeNetworkError("v6.gh-proxy.org", e.Channel)
	}
	if e.Skipped != "" {
		channel = "not attempted (" + e.Skipped + ")"
	}
	if e.Category == "validation" {
		return fmt.Sprintf("GitHub download validation failed (stage=%s): direct: %s; acceleration: %s", e.Stage, direct, channel)
	}
	return fmt.Sprintf("GitHub download network failure (stage=%s): direct: %s; acceleration: %s; check the Agent network namespace's DNS servers and HTTPS egress; on IPv6-only hosts configure a reachable IPv6 DNS resolver for both api.github.com and v6.gh-proxy.org", e.Stage, direct, channel)
}
func (e *Failure) Unwrap() []error {
	var errs []error
	if e.Direct != nil {
		errs = append(errs, e.Direct)
	}
	if e.Channel != nil {
		errs = append(errs, e.Channel)
	}
	return errs
}
func Terminal(err error) bool { var failure *Failure; return errors.As(err, &failure) }
func describeNetworkError(host string, err error) string {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		label := "domain resolution failed"
		if dns.IsTimeout {
			label = "domain resolution timed out"
		}
		if host == "api.github.com" {
			return fmt.Sprintf("GitHub API %s (%s): %v", label, host, err)
		}
		return fmt.Sprintf("%s %s: %v", host, label, err)
	}
	return fmt.Sprintf("%s connection failed: %v", host, err)
}
func networkFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var certificate *tls.CertificateVerificationError
	var invalid x509.CertificateInvalidError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	if errors.As(err, &certificate) || errors.As(err, &invalid) || errors.As(err, &unknown) || errors.As(err, &hostname) {
		return false
	}
	var dns *net.DNSError
	var op *net.OpError
	return errors.As(err, &dns) || errors.As(err, &op) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// Wrap retains the original redirect policy, dialer, TLS validation and caller
// deadline. Only public GitHub GET/HEAD requests are eligible.
func Wrap(client *http.Client) *http.Client {
	copy := *client
	copy.Transport = &Transport{Base: client.Transport, Detect: localDetector.Detect, Invalidate: localDetector.Invalidate}
	return &copy
}
func (t *Transport) invalidate() {
	invalidate := t.Invalidate
	if invalidate == nil && t.Detect == nil {
		invalidate = localDetector.Invalidate
	}
	if invalidate != nil {
		invalidate()
	}
	t.mu.Lock()
	t.acceleratedUntil = time.Time{}
	t.mu.Unlock()
}
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if !IsGitHub(req.URL) || (req.Method != http.MethodGet && req.Method != http.MethodHead) || (req.Body != nil && req.Body != http.NoBody) {
		return base.RoundTrip(req)
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if transport, ok := base.(*http.Transport); ok && transport.Proxy != nil {
		proxy, err := transport.Proxy(req)
		if err != nil {
			return nil, err
		}
		if proxy != nil {
			slog.Info("GitHub download channel", "channel", "configured-proxy", "proxy_host", proxy.Hostname(), "host", req.URL.Hostname())
			response, err := t.request(base, req)
			if err != nil && req.Context().Err() == nil {
				category := "network"
				if !networkFailure(err) {
					category = "validation"
				}
				return nil, &Failure{Direct: err, Host: req.URL.Hostname(), Stage: "configured-proxy", Skipped: "explicit egress proxy preserved", Category: category}
			}
			return response, err
		}
	}
	t.mu.Lock()
	preferChannel := time.Now().Before(t.acceleratedUntil)
	t.mu.Unlock()
	mode := Unknown
	if !preferChannel {
		detect := t.Detect
		if detect == nil {
			detect = localDetector.Detect
		}
		mode = detect(req.Context())
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	var directErr error
	if mode != IPv6Only && !preferChannel {
		response, err := t.request(base, req)
		if err == nil {
			return response, nil
		}
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		if !networkFailure(err) {
			return nil, err
		}
		directErr = err
		t.invalidate()
		slog.Warn("GitHub download channel switch", "network", mode, "from", "direct", "to", "acceleration", "reason", describeNetworkError(req.URL.Hostname(), err), "stage", "dns-connect-headers", "attempt", 1)
	}
	channelAttempt := 1
	if directErr != nil {
		channelAttempt = 2
	}
	response, err := t.accelerated(base, req)
	if err != nil {
		t.invalidate()
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		if directErr == nil && networkFailure(err) {
			detect := t.Detect
			if detect == nil {
				detect = localDetector.Detect
			}
			refreshed := detect(req.Context())
			if refreshed == IPv4 || refreshed == DualStack || refreshed == NAT64 || refreshed == IPv6Direct {
				slog.Info("GitHub download channel switch", "from", "acceleration", "to", "direct", "reason", "network changed", "network", refreshed, "attempt", 2)
				recovered, recoveryErr := t.request(base, req)
				if recoveryErr == nil {
					return recovered, nil
				}
				directErr = recoveryErr
				t.invalidate()
			}
			if req.Context().Err() != nil {
				return nil, req.Context().Err()
			}
		}
		slog.Warn("GitHub accelerated request failed", "network", mode, "host", req.URL.Hostname(), "stage", "dns-connect-headers", "attempt", channelAttempt, "error", err)
		category := "network"
		if !networkFailure(err) {
			category = "validation"
		}
		return nil, &Failure{Direct: directErr, Channel: err, Host: req.URL.Hostname(), Stage: "acceleration", Category: category}
	}
	// The real HTTPS resource request validates reachability. Keep Unknown's
	// network meaning and remember only the working channel for a short interval.
	if response != nil {
		t.mu.Lock()
		t.acceleratedUntil = time.Now().Add(30 * time.Second)
		t.mu.Unlock()
	}
	slog.Info("GitHub download channel", "network", mode, "channel", "acceleration", "host", req.URL.Hostname(), "status", response.StatusCode, "attempt", channelAttempt)
	return response, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.cancel()
	}
	return n, err
}
func (b *cancelBody) Close() error { defer b.cancel(); return b.ReadCloser.Close() }
func (t *Transport) request(base http.RoundTripper, req *http.Request) (*http.Response, error) {
	budget := t.HeaderBudget
	if budget <= 0 {
		budget = 5 * time.Second
	}
	var dnsPending atomic.Bool
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { dnsPending.Store(true) },
		DNSDone:  func(httptrace.DNSDoneInfo) { dnsPending.Store(false) },
	}
	ctx, cancel := context.WithCancel(httptrace.WithClientTrace(req.Context(), trace))
	timer := time.AfterFunc(budget, cancel)
	response, err := base.RoundTrip(req.Clone(ctx))
	stopped := timer.Stop()
	if err != nil {
		cancel()
		if !stopped && req.Context().Err() == nil {
			if dnsPending.Load() {
				return nil, &net.DNSError{Name: req.URL.Hostname(), Err: "i/o timeout (route DNS budget exceeded)", IsTimeout: true}
			}
			return nil, fmt.Errorf("DNS/connect/header budget exceeded: %w", context.DeadlineExceeded)
		}
		return nil, err
	}
	response.Request = req
	response.Body = &cancelBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}
func (t *Transport) accelerated(base http.RoundTripper, req *http.Request) (*http.Response, error) {
	accelerated, err := Accelerated(req.URL.String())
	if err != nil {
		return nil, err
	}
	wire := req.Clone(req.Context())
	wire.URL, _ = url.Parse(accelerated)
	wire.Host = ""
	wire.Header = make(http.Header)
	for _, name := range []string{"Accept", "User-Agent", "Range", "If-None-Match", "If-Modified-Since"} {
		for _, value := range req.Header.Values(name) {
			wire.Header.Add(name, value)
		}
	}
	// A proxy excluded for GitHub but configured for the accelerator must not
	// silently change the meaning of the explicit egress configuration.
	if transport, ok := base.(*http.Transport); ok && transport.Proxy != nil {
		bypass := transport.Clone()
		bypass.Proxy = nil
		base = bypass
		defer bypass.CloseIdleConnections()
	}
	response, err := t.request(base, wire)
	if err != nil {
		return nil, err
	}
	response.Request = req
	if location := response.Header.Get("Location"); location != "" && response.StatusCode >= 300 && response.StatusCode < 400 {
		next, parseErr := wire.URL.Parse(location)
		if parseErr == nil {
			var original string
			original, parseErr = Original(next.String())
			if parseErr == nil {
				response.Header = response.Header.Clone()
				response.Header.Set("Location", original)
			}
		}
		if parseErr != nil {
			response.Body.Close()
			return nil, fmt.Errorf("GitHub channel rejected redirect outside public GitHub sources")
		}
	}
	return response, nil
}
