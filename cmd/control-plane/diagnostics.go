//go:build linux

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"
)

// diagnosticAddress is the address the profiling endpoints would bind, or an
// empty string when profiling is not requested.
func diagnosticAddress() string {
	return strings.TrimSpace(os.Getenv("QCH_PPROF_ADDRESS"))
}

// startDiagnosticListener serves Go's runtime profiling endpoints when
// QCH_PPROF_ADDRESS names an address to bind.
//
// It is off unless that variable is set, because the endpoints expose a heap
// dump, a goroutine dump, and an on-demand CPU profile of the process. The
// intended use is a short, deliberate profiling session on an instance whose
// CPU is not otherwise explained: a 30 second profile attributes the control
// plane's busy cycles to a function rather than to a guess. Bind it to a
// loopback address and reach it through an SSH tunnel.
//
// The handlers are registered on a private mux rather than the default one, so
// importing net/http/pprof cannot leak endpoints onto whatever mux the API
// server builds.
func startDiagnosticListener(ctx context.Context) {
	address := diagnosticAddress()
	if address == "" {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			slog.Warn("diagnostic listener shutdown", "error", err)
		}
	}()
	slog.Warn("diagnostic profiling listener enabled", "address", address,
		"note", "profiling endpoints are unauthenticated; keep this bound to a loopback address")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("diagnostic listener stopped", "error", err)
	}
}
