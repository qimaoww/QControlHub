//go:build linux

package agent

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewClientTrustsConfiguredPrivateCA(t *testing.T) {
	requireAgentRoot(t)
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer tlsServer.Close()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw})
	caPath := filepath.Join(t.TempDir(), "control-plane-ca.pem")
	if err := os.WriteFile(caPath, certificate, 0o600); err != nil {
		t.Fatalf("write private CA: %v", err)
	}
	executor := testClientExecutor(t)
	client, err := NewClient(ClientConfig{
		ServerURL: "wss" + strings.TrimPrefix(tlsServer.URL, "https"), TLSCAFile: caPath,
	}, executor)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, client.config.ServerURL, nil)
	response, err := client.http.Do(request)
	if err != nil {
		t.Fatalf("request through configured private CA: %v", err)
	}
	response.Body.Close()
}
