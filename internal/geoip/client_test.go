package geoip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
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
