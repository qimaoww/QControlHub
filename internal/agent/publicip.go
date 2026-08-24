package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

const (
	defaultPublicIPProbeEvery = 5 * time.Minute
	minPublicIPProbeEvery     = time.Minute
	maxPublicIPProbeEvery     = 24 * time.Hour
	publicIPProbeTimeout      = 5 * time.Second
	publicIPProbeMaxBodyBytes = 64
	publicIPProbeUserAgent    = "QControlHub-Agent"
)

// publicIPProbeResponse matches a body that is exactly one IP address with
// optional surrounding whitespace. Multiline mode is intentionally disabled so
// a response carrying extra content on another line is rejected.
var publicIPProbeResponse = regexp.MustCompile(`^\s*([0-9a-fA-F:.]+)\s*$`)

// PublicIPProber discovers the node's public egress address for each family
// with outbound probes to operator-selected echo services. The control plane's
// connection-derived observation can only ever see the family the WSS
// connection used, so these probes close the dual-stack blind spot for
// client-access address candidates.
type PublicIPProber struct {
	mu         sync.Mutex
	addresses  [2]string
	sources    [2]string
	config     publicIPProbeRuntimeConfig
	local      bool
	generation uint64
	wake       chan struct{}
	inflight   chan struct{}

	httpV4   *http.Client
	httpV6   *http.Client
	interval time.Duration
}

type publicIPProbeRuntimeConfig struct {
	endpoints [2][]string
	source    string
}

// normalizePublicIPProbeEvery clamps the configured probe interval. Zero means
// the default cadence.
func normalizePublicIPProbeEvery(value time.Duration) time.Duration {
	if value == 0 {
		return defaultPublicIPProbeEvery
	}
	if value < minPublicIPProbeEvery {
		return minPublicIPProbeEvery
	}
	if value > maxPublicIPProbeEvery {
		return maxPublicIPProbeEvery
	}
	return value
}

// NewPublicIPProber builds a managed-capable prober from optional local
// operator-supplied endpoints. Local configuration is one endpoint per family;
// only managed zero-configuration supplies the approved same-family fallback.
// Probes use a direct connection so an operator proxy can never turn a proxy
// egress address into a node address.
func NewPublicIPProber(interval time.Duration, ipv4Endpoints, ipv6Endpoints []string) (*PublicIPProber, error) {
	ipv4, err := singleProbeEndpoint(ipv4Endpoints)
	if err != nil {
		return nil, fmt.Errorf("IPv4 public IP probe: %w", err)
	}
	ipv6, err := singleProbeEndpoint(ipv6Endpoints)
	if err != nil {
		return nil, fmt.Errorf("IPv6 public IP probe: %w", err)
	}
	config := core.PublicIPProbeConfig{IPv4Endpoint: ipv4, IPv6Endpoint: ipv6, IntervalSeconds: uint32(normalizePublicIPProbeEvery(interval) / time.Second)}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	prober := &PublicIPProber{
		httpV4:   publicIPProbeClient("tcp4"),
		httpV6:   publicIPProbeClient("tcp6"),
		interval: normalizePublicIPProbeEvery(interval),
		wake:     make(chan struct{}, 1),
	}
	if ipv4 != "" || ipv6 != "" {
		prober.local = true
		prober.config = publicIPProbeRuntimeConfig{endpoints: [2][]string{{ipv4}, {ipv6}}, source: core.PublicIPProbeSourceAgent}
	}
	return prober, nil
}

// singleProbeEndpoint returns the one explicit trust anchor for a family.
// Multiple endpoints are rejected so failure cannot silently switch owners.
func singleProbeEndpoint(configured []string) (string, error) {
	filtered := make([]string, 0, len(configured))
	for _, endpoint := range configured {
		if endpoint = strings.TrimSpace(endpoint); endpoint != "" {
			filtered = append(filtered, endpoint)
		}
	}
	if len(filtered) > 1 {
		return "", errors.New("exactly one endpoint per address family is allowed")
	}
	if len(filtered) == 0 {
		return "", nil
	}
	return filtered[0], nil
}

func publicIPProbeClient(network string) *http.Client {
	dialer := &net.Dialer{Timeout: publicIPProbeTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// Never honor an operator HTTP(S)_PROXY here: the probe must report the
		// node's own egress address, not a proxy's, so it cannot be mistaken
		// for a node address candidate.
		Proxy: nil,
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   publicIPProbeTimeout,
		ResponseHeaderTimeout: publicIPProbeTimeout,
		IdleConnTimeout:       90 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("public IP probe redirects are disabled")
		},
	}
}

// Run probes configured families immediately and then on every tick. A failed
// family is cleared so a stale value cannot outlive its latest verification.
func (prober *PublicIPProber) Run(ctx context.Context) {
	if prober == nil {
		return
	}
	prober.probeAll(ctx)
	timer := time.NewTimer(prober.currentInterval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-prober.wake:
			prober.probeAll(ctx)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(prober.currentInterval())
		case <-timer.C:
			prober.probeAll(ctx)
			timer.Reset(prober.currentInterval())
		}
	}
}

func (prober *PublicIPProber) probeAll(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	prober.mu.Lock()
	if prober.inflight != nil {
		done := prober.inflight
		prober.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
		}
		return
	}
	done := make(chan struct{})
	prober.inflight = done
	config := prober.config
	generation := prober.generation
	clients := [2]*http.Client{prober.httpV4, prober.httpV6}
	prober.mu.Unlock()
	defer func() {
		prober.mu.Lock()
		close(done)
		prober.inflight = nil
		prober.mu.Unlock()
	}()

	results := [2]string{}
	var wait sync.WaitGroup
	for family, endpoints := range config.endpoints {
		if len(endpoints) == 0 || endpoints[0] == "" {
			continue
		}
		wait.Add(1)
		go func(family int, endpoints []string) {
			defer wait.Done()
			results[family] = probePublicIPFamily(ctx, clients[family], endpoints, family == 0)
		}(family, endpoints)
	}
	wait.Wait()

	prober.mu.Lock()
	if generation != prober.generation {
		prober.mu.Unlock()
		return
	}
	previous := prober.addresses
	for family := range results {
		prober.addresses[family] = results[family]
		if results[family] == "" {
			prober.sources[family] = ""
		} else {
			prober.sources[family] = config.source
		}
	}
	prober.mu.Unlock()
	for family, address := range results {
		if previous[family] == address {
			continue
		}
		label := "IPv6"
		if family == 0 {
			label = "IPv4"
		}
		if address == "" {
			slog.Info("cleared unverifiable public egress address", "family", label, "source", config.source)
		} else {
			slog.Info("verified public egress address", "family", label, "source", config.source)
		}
	}
}

func (prober *PublicIPProber) currentInterval() time.Duration {
	prober.mu.Lock()
	defer prober.mu.Unlock()
	return prober.interval
}

// ApplyManagedConfig atomically replaces the control-plane supplied config.
// A local Agent configuration is authoritative and cannot be silently
// overridden. Any accepted change clears cached values before a fresh probe.
func (prober *PublicIPProber) ApplyManagedConfig(config core.PublicIPProbeConfig) error {
	if prober == nil {
		return nil
	}
	config.IPv4Endpoint = strings.TrimSpace(config.IPv4Endpoint)
	config.IPv4FallbackEndpoint = strings.TrimSpace(config.IPv4FallbackEndpoint)
	config.IPv6Endpoint = strings.TrimSpace(config.IPv6Endpoint)
	config.IPv6FallbackEndpoint = strings.TrimSpace(config.IPv6FallbackEndpoint)
	if err := config.Validate(); err != nil {
		return err
	}
	prober.mu.Lock()
	if prober.local {
		prober.mu.Unlock()
		return nil
	}
	interval := defaultPublicIPProbeEvery
	if config.IntervalSeconds != 0 {
		interval = time.Duration(config.IntervalSeconds) * time.Second
	}
	endpoints := [2][]string{
		compactProbeEndpoints(config.IPv4Endpoint, config.IPv4FallbackEndpoint),
		compactProbeEndpoints(config.IPv6Endpoint, config.IPv6FallbackEndpoint),
	}
	next := publicIPProbeRuntimeConfig{endpoints: endpoints, source: core.PublicIPProbeSourceControlPlane}
	if reflect.DeepEqual(prober.config, next) && prober.interval == interval {
		prober.mu.Unlock()
		return nil
	}
	prober.config = next
	prober.interval = interval
	prober.generation++
	prober.addresses = [2]string{}
	prober.sources = [2]string{}
	prober.mu.Unlock()
	select {
	case prober.wake <- struct{}{}:
	default:
	}
	return nil
}

// Snapshot returns the most recent probed public addresses.
func (prober *PublicIPProber) Snapshot() (ipv4, ipv6 string) {
	if prober == nil {
		return "", ""
	}
	prober.mu.Lock()
	defer prober.mu.Unlock()
	return prober.addresses[0], prober.addresses[1]
}

func (prober *PublicIPProber) SnapshotWithSources() (ipv4, ipv6, ipv4Source, ipv6Source string) {
	if prober == nil {
		return "", "", "", ""
	}
	prober.mu.Lock()
	defer prober.mu.Unlock()
	return prober.addresses[0], prober.addresses[1], prober.sources[0], prober.sources[1]
}

func (prober *PublicIPProber) Enabled() bool {
	if prober == nil {
		return false
	}
	prober.mu.Lock()
	defer prober.mu.Unlock()
	return len(prober.config.endpoints[0]) > 0 && prober.config.endpoints[0][0] != "" || len(prober.config.endpoints[1]) > 0 && prober.config.endpoints[1][0] != ""
}

func compactProbeEndpoints(primary, fallback string) []string {
	endpoints := make([]string, 0, 2)
	if primary = strings.TrimSpace(primary); primary != "" {
		endpoints = append(endpoints, primary)
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		endpoints = append(endpoints, fallback)
	}
	return endpoints
}

// probePublicIPFamily tries only the ordered same-family chain. A two-endpoint
// chain shares the five-second family budget, so a failed primary cannot turn
// into an unbounded second request.
func probePublicIPFamily(ctx context.Context, client *http.Client, endpoints []string, wantIPv4 bool) string {
	familyContext, cancel := context.WithTimeout(ctx, publicIPProbeTimeout)
	defer cancel()
	for index, endpoint := range endpoints {
		if endpoint == "" {
			continue
		}
		attemptContext := familyContext
		var attemptCancel context.CancelFunc
		if len(endpoints) > 1 {
			attemptContext, attemptCancel = context.WithTimeout(familyContext, publicIPProbeTimeout/2)
		}
		address, err := probePublicIPEndpoint(attemptContext, client, endpoint, wantIPv4)
		if attemptCancel != nil {
			attemptCancel()
		}
		if err == nil {
			return address
		}
		// Only a bounded category reaches the log. A raw transport error formats
		// the configured endpoint URL and its underlying network address, which
		// could expose operator host/path detail even though credentials and
		// query strings are rejected before a request is built.
		slog.Debug("public IP probe endpoint failed", "family_ipv4", wantIPv4, "provider_index", index, "error", publicIPProbeErrorCategory(err))
		if familyContext.Err() != nil {
			break
		}
	}
	return ""
}

func probePublicIPEndpoint(ctx context.Context, client *http.Client, endpoint string, wantIPv4 bool) (string, error) {
	requestContext, cancel := context.WithTimeout(ctx, publicIPProbeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", publicIPProbeSafeError(err)
	}
	request.Header.Set("User-Agent", publicIPProbeUserAgent)
	request.Header.Set("Accept", "text/plain")
	response, err := client.Do(request)
	if err != nil {
		return "", publicIPProbeSafeError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("probe endpoint returned HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, publicIPProbeMaxBodyBytes+1))
	if err != nil {
		return "", publicIPProbeSafeError(err)
	}
	if len(body) > publicIPProbeMaxBodyBytes {
		return "", errors.New("probe response exceeds the size limit")
	}
	match := publicIPProbeResponse.FindSubmatch(body)
	if match == nil {
		return "", errors.New("probe response is not a bare IP address")
	}
	address, err := netip.ParseAddr(string(match[1]))
	if err != nil {
		return "", errors.New("probe response contained an invalid address")
	}
	address = address.Unmap()
	if !netpolicy.IsPublicAddress(address) || netpolicy.IsCloudflareAddress(address) {
		return "", errors.New("probe response is not a trusted globally routable address")
	}
	if address.Is4() != wantIPv4 {
		family := "IPv6"
		if wantIPv4 {
			family = "IPv4"
		}
		return "", fmt.Errorf("probe endpoint answered with the wrong family for %s", family)
	}
	return address.String(), nil
}

// publicIPProbeErrorCategory maps an endpoint failure to a bounded, log-safe
// label. A raw *url.Error formats the full endpoint URL and any underlying
// network address, so it must never be logged directly; only the category is
// retained alongside the family and provider index.
func publicIPProbeErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	var classified *publicIPProbeError
	if errors.As(err, &classified) {
		return classified.category
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var certificateErr *tls.CertificateVerificationError
	if errors.As(err, &certificateErr) {
		return "tls"
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		if networkErr.Timeout() {
			return "timeout"
		}
		return "network"
	}
	return "http"
}

// publicIPProbeSafeError collapses a transport or parse failure into a bounded
// error that never carries the configured endpoint URL, an addressed host or
// path, or a response address. The returned error stays a restricted type the
// log classifier can re-identify, so its category survives the boundary exactly
// once instead of being re-derived into a generic http outcome.
func publicIPProbeSafeError(err error) error {
	if err == nil {
		return nil
	}
	return &publicIPProbeError{category: publicIPProbeErrorCategory(err)}
}

// publicIPProbeError is a restricted, point-in-time classification of an
// endpoint failure. It carries only a bounded category and never the endpoint
// URL, an addressed host or path, or a response address.
type publicIPProbeError struct {
	category string
}

func (err *publicIPProbeError) Error() string {
	return err.category
}
