package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbePublicIPEndpointValidatesResponse(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		status   int
		wantIPv4 bool
		want     string
	}{
		{name: "ipv4 echo", body: "93.184.216.34\n", status: http.StatusOK, wantIPv4: true, want: "93.184.216.34"},
		{name: "ipv4 echo without newline", body: "198.35.26.96", status: http.StatusOK, wantIPv4: true, want: "198.35.26.96"},
		{name: "ipv6 echo", body: "2606:4700:4700::1111\n", status: http.StatusOK, wantIPv4: false, want: "2606:4700:4700::1111"},
		{name: "wrong family rejected", body: "2606:4700:4700::1111\n", status: http.StatusOK, wantIPv4: true},
		{name: "v4-mapped v6 rejected for v6", body: "::ffff:93.184.216.34\n", status: http.StatusOK, wantIPv4: false},
		{name: "private address rejected", body: "10.0.0.8\n", status: http.StatusOK, wantIPv4: true},
		{name: "documentation range rejected", body: "2001:db8::1\n", status: http.StatusOK, wantIPv4: false},
		{name: "not an address", body: "your ip is 93.184.216.34\n", status: http.StatusOK, wantIPv4: true},
		{name: "html rejected", body: "<html>1.2.3.4</html>", status: http.StatusOK, wantIPv4: true},
		{name: "ipv4 with trailing junk", body: "93.184.216.34\njunk", status: http.StatusOK, wantIPv4: true},
		{name: "ipv4 after junk line", body: "junk\n93.184.216.34\n", status: http.StatusOK, wantIPv4: true},
		{name: "ipv4 with a second ip", body: "93.184.216.34\n1.1.1.1", status: http.StatusOK, wantIPv4: true},
		{name: "http error", body: "93.184.216.34", status: http.StatusTeapot, wantIPv4: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()
			got, err := probePublicIPEndpoint(context.Background(), server.Client(), server.URL, testCase.wantIPv4)
			if testCase.want == "" {
				if err == nil {
					t.Fatalf("expected rejection, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("got %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestProbePublicIPEndpointRejectsOversizedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, publicIPProbeMaxBodyBytes+2))
	}))
	defer server.Close()
	if got, err := probePublicIPEndpoint(context.Background(), server.Client(), server.URL, true); err == nil {
		t.Fatalf("expected oversized response to be rejected, got %q", got)
	}
}

func TestPublicIPProberFallsBackToEndpointsAndKeepsPreviousValue(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("198.35.26.10\n"))
	}))
	defer server.Close()
	prober := NewPublicIPProber(0, []string{server.URL + "/first", server.URL + "/second"}, nil)
	prober.probeAll(context.Background())
	ipv4, ipv6 := prober.Snapshot()
	if ipv4 != "198.35.26.10" {
		t.Fatalf("expected fallback endpoint to win, got ipv4=%q ipv6=%q", ipv4, ipv6)
	}
	if ipv6 != "" {
		t.Fatalf("expected no IPv6 probe result, got %q", ipv6)
	}
	// An all-endpoints failure must keep the last known address.
	requests = 1 << 30
	prober.probeAll(context.Background())
	if got, _ := prober.Snapshot(); got != "198.35.26.10" {
		t.Fatalf("failed probe must keep the previous address, got %q", got)
	}
}

func TestPublicIPProberNilIsSafe(t *testing.T) {
	var prober *PublicIPProber
	if ipv4, ipv6 := prober.Snapshot(); ipv4 != "" || ipv6 != "" {
		t.Fatal("nil prober snapshot must be empty")
	}
	prober.Run(context.Background())
}

func TestFilteredProbeEndpointsDropsBlankAndNeverDefaults(t *testing.T) {
	if got := filteredProbeEndpoints(nil); len(got) != 0 {
		t.Fatalf("nil endpoints must yield an empty list, got %v", got)
	}
	if got := filteredProbeEndpoints([]string{" ", "https://echo.example.com"}); len(got) != 1 || got[0] != "https://echo.example.com" {
		t.Fatalf("blank endpoints must be dropped, got %v", got)
	}
}

func TestPublicIPProberEmptyEndpointsDisables(t *testing.T) {
	if prober := NewPublicIPProber(0, nil, nil); prober != nil {
		t.Fatalf("empty endpoint lists must disable the prober, got %+v", prober)
	}
	if prober := NewPublicIPProber(0, []string{"https://echo.example.com"}, nil); prober == nil {
		t.Fatal("a non-empty IPv4 endpoint list must keep the prober")
	}
}

func TestNormalizePublicIPProbeEvery(t *testing.T) {
	cases := map[time.Duration]time.Duration{
		0:                      defaultPublicIPProbeEvery,
		10 * time.Millisecond:  minPublicIPProbeEvery,
		time.Duration(1 << 62): maxPublicIPProbeEvery,
		30 * time.Minute:       30 * time.Minute,
	}
	for input, want := range cases {
		if got := normalizePublicIPProbeEvery(input); got != want {
			t.Fatalf("normalizePublicIPProbeEvery(%v) = %v, want %v", input, got, want)
		}
	}
}
