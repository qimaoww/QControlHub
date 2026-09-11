//go:build linux

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestDiagnosticListenerIsOptIn covers the contract that matters for a profiling
// endpoint: it must stay off unless it is explicitly requested, because it
// exposes a heap dump and a goroutine dump of a process holding database
// credentials.
func TestDiagnosticListenerIsOptIn(t *testing.T) {
	t.Setenv("QCH_PPROF_ADDRESS", "")

	// A listener that never binds cannot be observed directly, so this asserts
	// the decision the function makes rather than a side effect: with no
	// address, nothing is started and the call returns immediately.
	startDiagnosticListener(context.Background())
	if got := diagnosticAddress(); got != "" {
		t.Fatalf("diagnostic address = %q with the variable unset, want empty", got)
	}
}

func TestDiagnosticListenerServesProfilesWhenEnabled(t *testing.T) {
	// Bind an ephemeral port so the test cannot collide with anything local.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	probe.Close()

	t.Setenv("QCH_PPROF_ADDRESS", address)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go startDiagnosticListener(ctx)

	deadline := time.Now().Add(5 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		response, err := client.Get(fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=1", address))
		if err == nil {
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("goroutine profile status = %d, want 200", response.StatusCode)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("diagnostic listener at %s never answered", address)
}
