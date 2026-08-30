// Package geoip resolves public node addresses to ISO country codes.
package geoip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

const (
	defaultEndpoint  = "https://get.geojs.io/v1/ip/geo"
	requestTimeout   = 5 * time.Second
	maxResponseBytes = 64 << 10
	cacheTTL         = 48 * time.Hour
	maxCachedRegions = 4096
)

// Region is the country/region returned by the GeoIP provider.
type Region struct {
	ISOCode string
	Name    string
}

type cachedRegion struct {
	value     Region
	expiresAt time.Time
}

// Client looks up public addresses and keeps a short-lived in-memory cache.
type Client struct {
	http     *http.Client
	endpoint string

	mu    sync.Mutex
	cache map[string]cachedRegion
}

// New creates a GeoIP client. The HTTP client is injectable for tests.
func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	return &Client{
		http:     httpClient,
		endpoint: defaultEndpoint,
		cache:    make(map[string]cachedRegion),
	}
}

// Lookup resolves one public address through the configured GeoIP provider.
func (client *Client) Lookup(ctx context.Context, address netip.Addr) (Region, error) {
	address = address.Unmap()
	if !netpolicy.IsPublicAddress(address) {
		return Region{}, errors.New("GeoIP lookup requires a public IP address")
	}
	key := address.String()
	now := time.Now()
	client.mu.Lock()
	if cached, ok := client.cache[key]; ok {
		if now.Before(cached.expiresAt) {
			client.mu.Unlock()
			return cached.value, nil
		}
		delete(client.cache, key)
	}
	client.mu.Unlock()

	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestContext,
		http.MethodGet,
		strings.TrimRight(client.endpoint, "/")+"/"+key+".json",
		nil,
	)
	if err != nil {
		return Region{}, fmt.Errorf("create GeoIP request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return Region{}, fmt.Errorf("request GeoIP provider: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Region{}, fmt.Errorf("GeoIP provider returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Region{}, fmt.Errorf("read GeoIP response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return Region{}, errors.New("GeoIP response is too large")
	}
	var payload struct {
		CountryCode string `json:"country_code"`
		Country     string `json:"country"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Region{}, fmt.Errorf("decode GeoIP response: %w", err)
	}
	code := strings.ToUpper(strings.TrimSpace(payload.CountryCode))
	if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return Region{}, errors.New("GeoIP provider returned an invalid country code")
	}
	region := Region{ISOCode: code, Name: strings.TrimSpace(payload.Country)}
	client.mu.Lock()
	if len(client.cache) >= maxCachedRegions {
		client.cache = make(map[string]cachedRegion)
	}
	client.cache[key] = cachedRegion{value: region, expiresAt: now.Add(cacheTTL)}
	client.mu.Unlock()
	return region, nil
}
