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
	endpoints [2]string
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
// operator-supplied endpoints. No third-party service is filled by default;
// empty lists keep the object idle until the authenticated WSS session supplies
// capability-gated managed configuration. Probes use a direct connection so an operator proxy
// can never turn a proxy egress address into a node address.
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
		prober.config = publicIPProbeRuntimeConfig{endpoints: [2]string{ipv4, ipv6}, source: core.PublicIPProbeSourceAgent}
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
	for family, endpoint := range config.endpoints {
		if endpoint == "" {
			continue
		}
		wait.Add(1)
		go func(family int, endpoint string) {
			defer wait.Done()
			results[family] = probePublicIPFamily(ctx, clients[family], endpoint, family == 0)
		}(family, endpoint)
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
	config.IPv6Endpoint = strings.TrimSpace(config.IPv6Endpoint)
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
	next := publicIPProbeRuntimeConfig{endpoints: [2]string{config.IPv4Endpoint, config.IPv6Endpoint}, source: core.PublicIPProbeSourceControlPlane}
	if prober.config == next && prober.interval == interval {
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
	return prober.config.endpoints[0] != "" || prober.config.endpoints[1] != ""
}

// probePublicIPFamily queries the family's single configured endpoint.
func probePublicIPFamily(ctx context.Context, client *http.Client, endpoint string, wantIPv4 bool) string {
	address, err := probePublicIPEndpoint(ctx, client, endpoint, wantIPv4)
	if err != nil {
		// Do not log the configured URL: an operator-controlled path can contain
		// deployment detail even though credentials and query strings are rejected.
		slog.Debug("public IP probe endpoint failed", "family_ipv4", wantIPv4, "error", err)
		return ""
	}
	return address
}

func probePublicIPEndpoint(ctx context.Context, client *http.Client, endpoint string, wantIPv4 bool) (string, error) {
	requestContext, cancel := context.WithTimeout(ctx, publicIPProbeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", publicIPProbeUserAgent)
	request.Header.Set("Accept", "text/plain")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("probe endpoint returned %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, publicIPProbeMaxBodyBytes+1))
	if err != nil {
		return "", err
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
		return "", fmt.Errorf("parse probe response: %w", err)
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
