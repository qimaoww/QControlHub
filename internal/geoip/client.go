// Package geoip resolves public addresses to countries and mainland provinces.
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
	defaultEndpoint = "https://get.geojs.io/v1/ip/geo"
	// Free tiers: IPWho allows 1,000 requests/day; FreeIPAPI allows 10/10s
	// and 60/minute. Their documented response fields include regions.
	defaultIPWhoEndpoint     = "https://ipwho.is"
	defaultFreeIPAPIEndpoint = "https://free.freeipapi.com/api/v1/json"
	defaultFlagEndpoint      = "https://raw.githubusercontent.com/lipis/flag-icons/main/flags/4x3"
	requestTimeout           = 5 * time.Second
	maxResponseBytes         = 64 << 10
	maxFlagBytes             = 512 << 10 // Detailed coats of arms (e.g. Spain and Serbia) exceed 64 KiB.
	cacheTTL                 = 48 * time.Hour
	partialChinaTTL          = 6 * time.Hour
	maxCachedRegions         = 4096
	maxCachedFlags           = 512
)

// Region is the country/region returned by the GeoIP provider.
type Region struct {
	ISOCode  string
	Name     string
	Province string
}

type cachedRegion struct {
	value           Region
	expiresAt       time.Time
	provinceChecked bool
}

// Client looks up public addresses and keeps a short-lived in-memory cache.
type Client struct {
	http              *http.Client
	endpoint          string
	ipWhoEndpoint     string
	freeIPAPIEndpoint string
	flagEndpoint      string

	mu           sync.Mutex
	cache        map[string]cachedRegion
	flagCache    map[string][]byte
	freeAPICalls []time.Time
}

// New creates a GeoIP client. The HTTP client is injectable for tests.
func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	return &Client{
		http:              httpClient,
		endpoint:          defaultEndpoint,
		ipWhoEndpoint:     defaultIPWhoEndpoint,
		freeIPAPIEndpoint: defaultFreeIPAPIEndpoint,
		flagEndpoint:      defaultFlagEndpoint,
		cache:             make(map[string]cachedRegion),
		flagCache:         make(map[string][]byte),
	}
}

// Lookup resolves one public address, including a mainland province when a
// provider can identify one.
func (client *Client) Lookup(ctx context.Context, address netip.Addr) (Region, error) {
	return client.lookup(ctx, address, true)
}

// LookupCountry skips province fallbacks for callers that only show flags.
func (client *Client) LookupCountry(ctx context.Context, address netip.Addr) (Region, error) {
	return client.lookup(ctx, address, false)
}

func (client *Client) lookup(ctx context.Context, address netip.Addr, needProvince bool) (Region, error) {
	address = address.Unmap()
	if !netpolicy.IsPublicAddress(address) {
		return Region{}, errors.New("GeoIP lookup requires a public IP address")
	}
	key := address.String()
	now := time.Now()
	client.mu.Lock()
	if cached, ok := client.cache[key]; ok {
		if now.Before(cached.expiresAt) && (!needProvince || cached.value.ISOCode != "CN" || cached.value.Province != "" || cached.provinceChecked) {
			client.mu.Unlock()
			return cached.value, nil
		}
		delete(client.cache, key)
	}
	client.mu.Unlock()

	var region Region
	var err error
	if needProvince {
		region, err = client.lookupRegion(ctx, key)
	} else {
		region, err = client.lookupGeoJS(ctx, key)
	}
	if err != nil {
		return Region{}, err
	}
	ttl := cacheTTL
	if needProvince && region.ISOCode == "CN" && region.Province == "" {
		ttl = partialChinaTTL
	}
	client.mu.Lock()
	if len(client.cache) >= maxCachedRegions {
		client.cache = make(map[string]cachedRegion)
	}
	client.cache[key] = cachedRegion{value: region, expiresAt: now.Add(ttl), provinceChecked: needProvince}
	client.mu.Unlock()
	return region, nil
}

// The secondary providers are queried when GeoJS fails or cannot identify a
// mainland province. A country disagreement never overwrites a successful
// primary result.
func (client *Client) lookupRegion(ctx context.Context, ip string) (Region, error) {
	primary, primaryErr := client.lookupGeoJS(ctx, ip)
	if primaryErr == nil && (primary.ISOCode != "CN" || primary.Province != "") {
		return primary, nil
	}
	best := primary
	for _, lookup := range []func(context.Context, string) (Region, error){client.lookupIPWho, client.lookupFreeIPAPI} {
		if ctx.Err() != nil {
			break
		}
		candidate, err := lookup(ctx, ip)
		if err != nil {
			continue
		}
		if primaryErr == nil && candidate.ISOCode != primary.ISOCode {
			continue
		}
		if best.ISOCode == "" {
			best = candidate
		}
		if candidate.ISOCode == "CN" && candidate.Province != "" {
			if best.Name != "" {
				candidate.Name = best.Name
			}
			return candidate, nil
		}
		if best.ISOCode != "CN" {
			return best, nil
		}
	}
	if best.ISOCode != "" {
		return best, nil
	}
	if ctx.Err() != nil {
		return Region{}, ctx.Err()
	}
	return Region{}, primaryErr
}

func (client *Client) lookupGeoJS(ctx context.Context, ip string) (Region, error) {
	var payload struct {
		CountryCode string `json:"country_code"`
		Country     string `json:"country"`
		Region      string `json:"region"`
	}
	err := client.readJSON(ctx, strings.TrimRight(client.endpoint, "/")+"/"+ip+".json", &payload)
	if err != nil {
		return Region{}, err
	}
	return providerRegion(payload.CountryCode, payload.Country, payload.Region)
}

func (client *Client) lookupIPWho(ctx context.Context, ip string) (Region, error) {
	if client.ipWhoEndpoint == "" {
		return Region{}, errors.New("IPWho disabled")
	}
	var payload struct {
		Success     bool   `json:"success"`
		IP          string `json:"ip"`
		CountryCode string `json:"country_code"`
		Country     string `json:"country"`
		Region      string `json:"region"`
	}
	err := client.readJSON(ctx, strings.TrimRight(client.ipWhoEndpoint, "/")+"/"+ip, &payload)
	if err != nil {
		return Region{}, err
	}
	if !payload.Success || !sameIP(payload.IP, ip) {
		return Region{}, errors.New("IPWho returned no location for the requested IP")
	}
	return providerRegion(payload.CountryCode, payload.Country, payload.Region)
}

func (client *Client) lookupFreeIPAPI(ctx context.Context, ip string) (Region, error) {
	if client.freeIPAPIEndpoint == "" {
		return Region{}, errors.New("FreeIPAPI disabled")
	}
	if !client.takeFreeAPIQuota() {
		return Region{}, errors.New("FreeIPAPI local rate limit reached")
	}
	var payload struct {
		IP          string `json:"ipAddress"`
		CountryCode string `json:"countryCode"`
		Country     string `json:"countryName"`
		Region      string `json:"regionName"`
	}
	err := client.readJSON(ctx, strings.TrimRight(client.freeIPAPIEndpoint, "/")+"/"+ip, &payload)
	if err != nil {
		return Region{}, err
	}
	if !sameIP(payload.IP, ip) {
		return Region{}, errors.New("FreeIPAPI returned a different IP")
	}
	return providerRegion(payload.CountryCode, payload.Country, payload.Region)
}

func sameIP(got, want string) bool {
	a, err := netip.ParseAddr(got)
	if err != nil {
		return false
	}
	b, _ := netip.ParseAddr(want)
	return a.Unmap() == b.Unmap()
}

func (client *Client) takeFreeAPIQuota() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	now := time.Now()
	active := client.freeAPICalls[:0]
	lastTenSeconds := 0
	for _, at := range client.freeAPICalls {
		if now.Sub(at) >= time.Minute {
			continue
		}
		active = append(active, at)
		if now.Sub(at) < 10*time.Second {
			lastTenSeconds++
		}
	}
	client.freeAPICalls = active
	if len(active) >= 60 || lastTenSeconds >= 10 {
		return false
	}
	client.freeAPICalls = append(active, now)
	return true
}

func providerRegion(countryCode, country, subdivision string) (Region, error) {
	code := strings.ToUpper(strings.TrimSpace(countryCode))
	if !ValidRegionCode(code) {
		return Region{}, errors.New("GeoIP provider returned an invalid country code")
	}
	if len(country) > 200 || strings.ContainsRune(country, '\x00') {
		return Region{}, errors.New("GeoIP provider returned an invalid country name")
	}
	region := Region{ISOCode: code, Name: strings.TrimSpace(country)}
	if code == "CN" {
		region.Province = chinaProvince(subdivision)
	}
	return region, nil
}

func (client *Client) readJSON(ctx context.Context, url string, payload any) error {
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create GeoIP request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("request GeoIP provider: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GeoIP provider returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read GeoIP response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return errors.New("GeoIP response is too large")
	}
	if err := json.Unmarshal(body, payload); err != nil {
		return fmt.Errorf("decode GeoIP response: %w", err)
	}
	return nil
}

// Flag returns a compact 4:3 SVG flag for one ISO country/region code. Artwork
// is fetched from lipis/flag-icons and cached in memory; callers only ever
// supply the validated two-letter code returned by the GeoIP provider.
func (client *Client) Flag(ctx context.Context, isoCode string) ([]byte, error) {
	code := strings.ToLower(strings.TrimSpace(isoCode))
	if len(code) != 2 || code[0] < 'a' || code[0] > 'z' || code[1] < 'a' || code[1] > 'z' {
		return nil, errors.New("flag requires a valid ISO country code")
	}
	client.mu.Lock()
	if cached, ok := client.flagCache[code]; ok {
		client.mu.Unlock()
		return append([]byte(nil), cached...), nil
	}
	client.mu.Unlock()

	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestContext,
		http.MethodGet,
		strings.TrimRight(client.flagEndpoint, "/")+"/"+code+".svg",
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create flag request: %w", err)
	}
	request.Header.Set("Accept", "image/svg+xml")
	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request flag provider: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("flag provider returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFlagBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read flag response: %w", err)
	}
	if len(body) > maxFlagBytes {
		return nil, errors.New("flag response is too large")
	}
	normalized := strings.ToLower(string(body))
	if !strings.Contains(normalized, "<svg") || strings.Contains(normalized, "<script") || strings.Contains(normalized, "<foreignobject") || strings.Contains(normalized, "onload=") {
		return nil, errors.New("flag provider returned unsafe SVG")
	}
	client.mu.Lock()
	if len(client.flagCache) >= maxCachedFlags {
		client.flagCache = make(map[string][]byte)
	}
	client.flagCache[code] = append([]byte(nil), body...)
	client.mu.Unlock()
	return body, nil
}
