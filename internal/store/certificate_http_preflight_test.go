package store

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCertificateHTTPPreflightWaitsForAgentSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstProbe, installed := make(chan struct{}), make(chan struct{})
	var first sync.Once
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Host != "edge.example.test" || r.URL.Path != "/.well-known/acme-challenge/token" {
			t.Error("incorrect challenge Host/path")
		}
		select {
		case <-installed:
			_, _ = w.Write([]byte("proof"))
		default:
			first.Do(func() { close(firstProbe) })
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	go func() {
		select {
		case <-firstProbe:
		case <-ctx.Done():
			return
		}
		timer := time.NewTimer(650 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			close(installed)
		case <-ctx.Done():
		}
	}()
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("2606:4700:4700::1111")}}, nil
	}
	var publicTargets []string
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		publicTargets = append(publicTargets, address)
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
	}
	if err := waitHTTPChallenge(ctx, func(probeCtx context.Context) error {
		return checkHTTPChallengeWith(probeCtx, "edge.example.test", "token", "proof", lookup, dial)
	}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() < 3 || publicTargets[len(publicTargets)-1] != "[2606:4700:4700::1111]:80" {
		t.Fatalf("did not retry the delayed snapshot and verify both addresses: %v", publicTargets)
	}
}

func TestCertificateHTTPPreflightStopsOnCancellationAndRevocation(t *testing.T) {
	for _, denied := range []error{context.Canceled, ErrForbidden} {
		calls := 0
		err := waitHTTPChallenge(context.Background(), func(context.Context) error {
			calls++
			return denied
		})
		if !errors.Is(err, denied) || calls != 1 {
			t.Fatalf("retried a canceled or unauthorized challenge: %v, calls=%d", err, calls)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := waitHTTPChallenge(ctx, func(context.Context) error { return errors.New("route not ready") }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("did not honor challenge deadline: %v", err)
	}
}
