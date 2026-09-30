package geoip

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLookupReadsCountryAndCachesByAddress(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/8.8.8.8.json" {
			t.Fatalf("GeoIP path = %q", request.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"country_code":"us","country":"United States"}`))
	}))
	defer server.Close()
	client := New(server.Client())
	client.endpoint = server.URL

	address := netip.MustParseAddr("8.8.8.8")
	first, err := client.Lookup(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Lookup(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.ISOCode != "US" || first.Name != "United States" {
		t.Fatalf("unexpected region: first=%+v second=%+v", first, second)
	}
	if requests != 1 {
		t.Fatalf("GeoIP provider requests = %d, want one cached request", requests)
	}
}

func TestLookupRejectsNonPublicAddressBeforeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		t.Fatal("private address unexpectedly reached GeoIP provider")
	}))
	defer server.Close()
	client := New(server.Client())
	client.endpoint = server.URL
	if _, err := client.Lookup(context.Background(), netip.MustParseAddr("192.168.1.10")); err == nil {
		t.Fatal("private address unexpectedly accepted")
	}
}

func TestLookupFillsMissingChinaProvinceFromAdditionalProviders(t *testing.T) {
	for _, test := range []struct {
		name, geoJS, ipWho, freeAPI, want string
		freeCalls                         int
	}{
		{"IPWho", `{"country_code":"CN","country":"China"}`, `{"success":true,"ip":"114.114.114.114","country_code":"CN","country":"China","region":"Jiangsu Sheng"}`, "", "江苏", 0},
		{"FreeIPAPI", `{"country_code":"CN","country":"China"}`, `{"success":false}`, `{"ipAddress":"114.114.114.114","countryCode":"CN","countryName":"China","regionName":"Zhejiang"}`, "浙江", 1},
		{"reject foreign fallback", `{"country_code":"CN","country":"China"}`, `{"success":true,"ip":"114.114.114.114","country_code":"US","country":"United States","region":"California"}`, `{"ipAddress":"114.114.114.114","countryCode":"CN","countryName":"China","regionName":"Guangdong"}`, "广东", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls[request.URL.Path]++
				switch request.URL.Path {
				case "/geo/114.114.114.114.json":
					_, _ = w.Write([]byte(test.geoJS))
				case "/who/114.114.114.114":
					_, _ = w.Write([]byte(test.ipWho))
				case "/free/114.114.114.114":
					_, _ = w.Write([]byte(test.freeAPI))
				default:
					t.Errorf("unexpected lookup: %s", request.URL.Path)
				}
			}))
			defer server.Close()
			client := New(server.Client())
			client.endpoint, client.ipWhoEndpoint, client.freeIPAPIEndpoint = server.URL+"/geo", server.URL+"/who", server.URL+"/free"
			for range 2 {
				region, err := client.Lookup(context.Background(), netip.MustParseAddr("114.114.114.114"))
				if err != nil || region.ISOCode != "CN" || region.Province != test.want {
					t.Fatalf("region=%+v err=%v", region, err)
				}
			}
			if calls["/geo/114.114.114.114.json"] != 1 || calls["/who/114.114.114.114"] != 1 || calls["/free/114.114.114.114"] != test.freeCalls {
				t.Fatalf("provider calls=%v", calls)
			}
		})
	}
}

func TestFreeIPAPICallBudget(t *testing.T) {
	client := New(nil)
	for range 10 {
		if !client.takeFreeAPIQuota() {
			t.Fatal("free provider rejected a permitted request")
		}
	}
	if client.takeFreeAPIQuota() {
		t.Fatal("free provider exceeded its 10 requests per 10 seconds limit")
	}
}

func TestCountryLookupDoesNotHideLaterProvinceEnrichment(t *testing.T) {
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls[request.URL.Path]++
		if strings.HasPrefix(request.URL.Path, "/geo/") {
			_, _ = w.Write([]byte(`{"country_code":"CN","country":"China"}`))
		} else {
			_, _ = w.Write([]byte(`{"success":true,"ip":"114.114.114.114","country_code":"CN","country":"China","region":"Jiangsu Sheng"}`))
		}
	}))
	defer server.Close()
	client := New(server.Client())
	client.endpoint, client.ipWhoEndpoint, client.freeIPAPIEndpoint = server.URL+"/geo", server.URL+"/who", ""
	ip := netip.MustParseAddr("114.114.114.114")
	if region, err := client.LookupCountry(context.Background(), ip); err != nil || region.ISOCode != "CN" {
		t.Fatalf("country=%+v err=%v", region, err)
	}
	if calls["/who/114.114.114.114"] != 0 {
		t.Fatal("flag lookup queried province provider")
	}
	if region, err := client.Lookup(context.Background(), ip); err != nil || region.Province != "江苏" {
		t.Fatalf("province=%+v err=%v", region, err)
	}
	if calls["/geo/114.114.114.114.json"] != 2 || calls["/who/114.114.114.114"] != 1 {
		t.Fatalf("calls=%v", calls)
	}
}

func TestLookupCountryFailureDoesNotBlockFallbacks(t *testing.T) {
	for _, test := range []struct {
		name, ipWho, freeAPI, wantProvince string
		freeCalls                          int
	}{
		{"IPWho", `{"success":true,"ip":"114.114.114.114","country_code":"CN","country":"China","region":"Jiangsu Sheng"}`, "", "江苏", 0},
		{"FreeIPAPI", `{"success":false}`, `{"ipAddress":"114.114.114.114","countryCode":"CN","countryName":"China","regionName":"Zhejiang"}`, "浙江", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls[request.URL.Path]++
				switch request.URL.Path {
				case "/geo/114.114.114.114.json":
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
				case "/who/114.114.114.114":
					_, _ = w.Write([]byte(test.ipWho))
				case "/free/114.114.114.114":
					_, _ = w.Write([]byte(test.freeAPI))
				default:
					t.Errorf("unexpected lookup: %s", request.URL.Path)
				}
			}))
			defer server.Close()
			client := New(server.Client())
			client.endpoint, client.ipWhoEndpoint, client.freeIPAPIEndpoint = server.URL+"/geo", server.URL+"/who", server.URL+"/free"
			address := netip.MustParseAddr("114.114.114.114")

			if _, err := client.LookupCountry(context.Background(), address); err == nil {
				t.Fatal("failing GeoJS unexpectedly resolved")
			}
			if _, err := client.LookupCountry(context.Background(), address); !errors.Is(err, errRecentFailure) {
				t.Fatalf("repeated country lookup error = %v, want the remembered failure", err)
			}
			region, err := client.Lookup(context.Background(), address)
			if err != nil || region.ISOCode != "CN" || region.Province != test.wantProvince {
				t.Fatalf("fallback after country failure: region=%+v err=%v", region, err)
			}
			country, err := client.LookupCountry(context.Background(), address)
			if err != nil || country != region {
				t.Fatalf("country after fallback: region=%+v err=%v", country, err)
			}
			if calls["/geo/114.114.114.114.json"] != 2 || calls["/who/114.114.114.114"] != 1 || calls["/free/114.114.114.114"] != test.freeCalls {
				t.Fatalf("provider calls=%v", calls)
			}
		})
	}
}

func TestLookupProvinceFailureKeepsRecoveredCountryCache(t *testing.T) {
	var requests atomic.Int32
	var providerAvailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if !providerAvailable.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		switch request.URL.Path {
		case "/geo/114.114.114.114.json":
			_, _ = w.Write([]byte(`{"country_code":"CN","country":"China"}`))
		case "/who/114.114.114.114":
			_, _ = w.Write([]byte(`{"success":true,"ip":"114.114.114.114","country_code":"CN","country":"China","region":"Jiangsu Sheng"}`))
		default:
			t.Errorf("unexpected lookup: %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := New(server.Client())
	client.endpoint, client.ipWhoEndpoint, client.freeIPAPIEndpoint = server.URL+"/geo", server.URL+"/who", server.URL+"/free"
	address := netip.MustParseAddr("114.114.114.114")

	if _, err := client.Lookup(context.Background(), address); err == nil {
		t.Fatal("failing providers unexpectedly resolved")
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("initial provider requests = %d, want all three providers", got)
	}
	providerAvailable.Store(true)
	country, err := client.LookupCountry(context.Background(), address)
	if err != nil || country.ISOCode != "CN" || country.Province != "" {
		t.Fatalf("country after recovery: region=%+v err=%v", country, err)
	}
	if got := requests.Load(); got != 4 {
		t.Fatalf("provider requests after recovery = %d, want one country retry", got)
	}

	providerAvailable.Store(false)
	if _, err := client.Lookup(context.Background(), address); !errors.Is(err, errRecentFailure) {
		t.Fatalf("province lookup error = %v, want the remembered failure", err)
	}
	cachedCountry, err := client.LookupCountry(context.Background(), address)
	if err != nil || cachedCountry != country {
		t.Fatalf("cached country after province failure: region=%+v err=%v", cachedCountry, err)
	}
	if got := requests.Load(); got != 4 {
		t.Fatalf("provider requests = %d, want the cached country without another request", got)
	}

	// Keeping the country entry must still permit province enrichment once
	// the failure window expires.
	client.mu.Lock()
	client.failures[lookupFailureKey{address: address.String(), needProvince: true}] = time.Now().Add(-failureTTL - time.Second)
	client.mu.Unlock()
	providerAvailable.Store(true)
	region, err := client.Lookup(context.Background(), address)
	if err != nil || region.ISOCode != "CN" || region.Province != "江苏" {
		t.Fatalf("province after failure window: region=%+v err=%v", region, err)
	}
	if got := requests.Load(); got != 6 {
		t.Fatalf("provider requests after enrichment = %d, want GeoJS and IPWho retries", got)
	}
}

func TestLookupRemembersProviderFailures(t *testing.T) {
	for _, test := range []struct {
		name          string
		lookup        func(*Client, context.Context, netip.Addr) (Region, error)
		needProvince  bool
		providerCalls int32
	}{
		{"country", (*Client).LookupCountry, false, 1},
		{"region", (*Client).Lookup, true, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}))
			defer server.Close()
			client := New(server.Client())
			client.endpoint, client.ipWhoEndpoint, client.freeIPAPIEndpoint = server.URL+"/geo", server.URL+"/who", server.URL+"/free"
			address := netip.MustParseAddr("8.8.8.8")

			if _, err := test.lookup(client, context.Background(), address); err == nil {
				t.Fatal("failing provider unexpectedly resolved")
			}
			if _, err := test.lookup(client, context.Background(), address); !errors.Is(err, errRecentFailure) {
				t.Fatalf("second lookup error = %v, want the remembered failure", err)
			}
			if requests.Load() != test.providerCalls {
				t.Fatalf("GeoIP provider requests = %d, want the failure window to skip the providers", requests.Load())
			}

			// The memory expires, so a provider that recovered is consulted again.
			client.mu.Lock()
			client.failures[lookupFailureKey{address: address.String(), needProvince: test.needProvince}] = time.Now().Add(-failureTTL - time.Second)
			client.mu.Unlock()
			if _, err := test.lookup(client, context.Background(), address); err == nil {
				t.Fatal("still failing provider unexpectedly resolved")
			}
			if requests.Load() != 2*test.providerCalls {
				t.Fatalf("GeoIP provider requests = %d, want a retry after the failure window", requests.Load())
			}
		})
	}
}

func TestLookupRetriesAfterCallerCancellation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"country_code":"us","country":"United States"}`))
	}))
	defer server.Close()
	client := New(server.Client())
	client.endpoint = server.URL
	address := netip.MustParseAddr("8.8.8.8")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.LookupCountry(cancelled, address); err == nil {
		t.Fatal("cancelled lookup unexpectedly resolved")
	}
	region, err := client.LookupCountry(context.Background(), address)
	if err != nil || region.ISOCode != "US" {
		t.Fatalf("retry after caller cancellation: %+v, %v", region, err)
	}
	if requests != 1 {
		t.Fatalf("GeoIP provider requests = %d, want one request", requests)
	}
}

func TestLookupRetriesAfterCallerDeadline(t *testing.T) {
	for _, test := range []struct {
		name   string
		lookup func(*Client, context.Context, netip.Addr) (Region, error)
	}{
		{"country", (*Client).LookupCountry},
		{"region", (*Client).Lookup},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if requests.Add(1) == 1 {
					<-request.Context().Done()
					return
				}
				_, _ = w.Write([]byte(`{"country_code":"US","country":"United States"}`))
			}))
			defer server.Close()
			client := New(server.Client())
			client.endpoint, client.ipWhoEndpoint, client.freeIPAPIEndpoint = server.URL, "", ""
			address := netip.MustParseAddr("8.8.8.8")

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if _, err := test.lookup(client, ctx, address); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("short caller deadline error = %v, want context.DeadlineExceeded", err)
			}
			region, err := test.lookup(client, context.Background(), address)
			if err != nil || region.ISOCode != "US" {
				t.Fatalf("retry after caller deadline: region=%+v err=%v", region, err)
			}
			if requests.Load() != 2 {
				t.Fatalf("GeoIP provider requests = %d, want a retry after the caller deadline", requests.Load())
			}
		})
	}
}

func TestLookupRemembersProviderTimeout(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		<-request.Context().Done()
	}))
	defer server.Close()
	client := New(server.Client())
	client.endpoint = server.URL
	address := netip.MustParseAddr("8.8.8.8")

	if _, err := client.LookupCountry(context.Background(), address); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("provider timeout error = %v, want context.DeadlineExceeded", err)
	}
	if _, err := client.LookupCountry(context.Background(), address); !errors.Is(err, errRecentFailure) {
		t.Fatalf("retry after provider timeout error = %v, want the remembered failure", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("GeoIP provider requests = %d, want the failure window to skip the timed out provider", requests.Load())
	}
}

func TestFlagReadsSafeSVGAndCachesByCode(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/cn.svg" {
			t.Fatalf("flag path = %q", request.URL.Path)
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16"/></svg>`))
	}))
	defer server.Close()
	client := New(server.Client())
	client.flagEndpoint = server.URL

	first, err := client.Flag(context.Background(), "CN")
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Flag(context.Background(), "cn")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || requests != 1 {
		t.Fatalf("flag cache mismatch: requests=%d first=%q second=%q", requests, first, second)
	}
}

func flagSVGWithSize(size int) string {
	const prefix = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 480"><!--`
	const suffix = `--></svg>`
	return prefix + strings.Repeat(" ", size-len(prefix)-len(suffix)) + suffix
}

func TestFlagAcceptsLargeCatalogArtwork(t *testing.T) {
	// These catalog assets exceed the old 64 KiB limit. Use their upstream
	// sizes without requiring network access or checking in large SVGs.
	for _, tc := range []struct {
		code string
		size int
	}{
		{"BO", 102880},
		{"ES", 80958},
		{"MX", 84753},
		{"RS", 181634},
		{"SV", 77273},
		{"SG", maxFlagBytes},
	} {
		t.Run(tc.code, func(t *testing.T) {
			artwork := flagSVGWithSize(tc.size)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.URL.Path != "/"+strings.ToLower(tc.code)+".svg" {
					t.Errorf("flag path = %q", request.URL.Path)
				}
				w.Header().Set("Content-Type", "image/svg+xml")
				_, _ = w.Write([]byte(artwork))
			}))
			defer server.Close()
			client := New(server.Client())
			client.flagEndpoint = server.URL

			first, err := client.Flag(context.Background(), tc.code)
			if err != nil {
				t.Fatal(err)
			}
			if string(first) != artwork {
				t.Fatalf("artwork was truncated: got %d bytes, want %d", len(first), len(artwork))
			}
			first[0] = 'x'
			cached, err := client.Flag(context.Background(), " "+strings.ToLower(tc.code)+" ")
			if err != nil || string(cached) != artwork || requests.Load() != 1 {
				t.Fatalf("cached artwork mismatch: bytes=%d requests=%d err=%v", len(cached), requests.Load(), err)
			}
		})
	}
}

func TestFlagRejectsOversizedAndUnsafeArtwork(t *testing.T) {
	for _, tc := range []struct {
		name, artwork, wantError string
	}{
		{"oversized", flagSVGWithSize(maxFlagBytes + 1), "flag response is too large"},
		{"not-svg", `<html>upstream failure</html>`, "unsafe SVG"},
		{"script", `<svg><SCRIPT>alert(1)</SCRIPT></svg>`, "unsafe SVG"},
		{"foreign-object", `<svg><foreignObject><p>HTML</p></foreignObject></svg>`, "unsafe SVG"},
		{"onload", `<svg onload="alert(1)"></svg>`, "unsafe SVG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "image/svg+xml")
				_, _ = w.Write([]byte(tc.artwork))
			}))
			defer server.Close()
			client := New(server.Client())
			client.flagEndpoint = server.URL

			for range 2 {
				body, err := client.Flag(context.Background(), "ES")
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || body != nil {
					t.Fatalf("unsafe response: bytes=%d err=%v, want %q", len(body), err, tc.wantError)
				}
			}
			if requests.Load() != 2 {
				t.Fatal("rejected artwork must not be cached")
			}
		})
	}
}

func TestFlagLiveCatalog(t *testing.T) {
	if os.Getenv("QCH_TEST_LIVE_REGION_FLAGS") != "1" {
		t.Skip("set QCH_TEST_LIVE_REGION_FLAGS=1 to verify upstream artwork for the full catalog")
	}
	client := New(nil)
	for _, code := range RegionCodes() {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			if _, err := client.Flag(context.Background(), code); err != nil {
				t.Fatal(err)
			}
		})
	}
}
