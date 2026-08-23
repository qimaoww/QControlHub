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
// with outbound probes to well-known echo services. The control plane's
// connection-derived observation can only ever see the family the WSS
// connection used, so these probes close the dual-stack blind spot for
// client-access address candidates.
type PublicIPProber struct {
	mu        sync.Mutex
	addresses [2]string

	httpV4    *http.Client
	httpV6    *http.Client
	endpoints [2][]string
	interval  time.Duration
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

// NewPublicIPProber builds a prober from operator-supplied endpoints. The
// endpoints are never filled with third-party echo services by default: a
// caller that leaves both lists empty gets a nil prober, whose methods stay
// safe to call. Probes use a direct connection so an operator proxy can never
// turn a proxy egress address into a node address.
func NewPublicIPProber(interval time.Duration, ipv4Endpoints, ipv6Endpoints []string) *PublicIPProber {
	ipv4 := filteredProbeEndpoints(ipv4Endpoints)
	ipv6 := filteredProbeEndpoints(ipv6Endpoints)
	if len(ipv4) == 0 && len(ipv6) == 0 {
		return nil
	}
	return &PublicIPProber{
		httpV4:    publicIPProbeClient("tcp4"),
		httpV6:    publicIPProbeClient("tcp6"),
		endpoints: [2][]string{ipv4, ipv6},
		interval:  normalizePublicIPProbeEvery(interval),
	}
}

// filteredProbeEndpoints returns the trimmed, non-empty configured endpoints.
// An empty input yields nil so an operator that has not opted into probing
// never causes an outbound request.
func filteredProbeEndpoints(configured []string) []string {
	filtered := make([]string, 0, len(configured))
	for _, endpoint := range configured {
		if endpoint = strings.TrimSpace(endpoint); endpoint != "" {
			filtered = append(filtered, endpoint)
		}
	}
	return filtered
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

// Run probes both families immediately and then on every tick. A failed probe
// keeps the previous address so a transient outage never blanks a known value.
func (prober *PublicIPProber) Run(ctx context.Context) {
	if prober == nil {
		return
	}
	prober.probeAll(ctx)
	ticker := time.NewTicker(prober.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prober.probeAll(ctx)
		}
	}
}

func (prober *PublicIPProber) probeAll(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	prober.mu.Lock()
	clients := [2]*http.Client{prober.httpV4, prober.httpV6}
	prober.mu.Unlock()
	for family := range prober.endpoints {
		address := probePublicIPFamily(ctx, clients[family], prober.endpoints[family], family == 0)
		if address == "" {
			continue
		}
		prober.mu.Lock()
		previous := prober.addresses[family]
		prober.addresses[family] = address
		prober.mu.Unlock()
		if previous != address {
			label := "IPv6"
			if family == 0 {
				label = "IPv4"
			}
			if previous == "" {
				slog.Info("probed public egress address", "family", label, "address", address)
			} else {
				slog.Info("public egress address changed", "family", label, "previous", previous, "address", address)
			}
		}
	}
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

// probePublicIPFamily queries each endpoint in order and returns the first
// response that is a globally routable address of the requested family.
func probePublicIPFamily(ctx context.Context, client *http.Client, endpoints []string, wantIPv4 bool) string {
	for _, endpoint := range endpoints {
		address, err := probePublicIPEndpoint(ctx, client, endpoint, wantIPv4)
		if err == nil && address != "" {
			return address
		}
		slog.Debug("public IP probe endpoint failed", "endpoint", endpoint, "error", err)
	}
	return ""
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
	if !netpolicy.IsPublicAddress(address) {
		return "", errors.New("probe response is not a globally routable address")
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
