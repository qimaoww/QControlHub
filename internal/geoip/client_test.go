package geoip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"
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
