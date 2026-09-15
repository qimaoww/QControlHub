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
	"time"

	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

// publicIPProbeResponse matches a body that is exactly one IP address with
// optional surrounding whitespace. Multiline mode is intentionally disabled so
// a response carrying extra content on another line is rejected.
var publicIPProbeResponse = regexp.MustCompile(`^\s*([0-9a-fA-F:.]+)\s*$`)

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
