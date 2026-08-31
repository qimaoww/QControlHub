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
