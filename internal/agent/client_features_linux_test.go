//go:build linux

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientEnrollAdvertisesCoreLogsPerServiceManager(t *testing.T) {
	run := func(t *testing.T, kind string, wantsCoreLogs bool) {
		t.Helper()
		var captured core.EnrollRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/agent/v1/enroll" {
				http.NotFound(w, request)
				return
			}
			if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
				t.Fatalf("decode enroll request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(core.EnrollResponse{AgentID: "agt_0123456789abcdef"})
		}))
		defer server.Close()

		manager, err := NewServiceManager(kind)
		if err != nil {
			t.Fatal(err)
		}
		executor := &Executor{Services: manager}
		client := &Client{
			config: ClientConfig{
				ServerURL: server.URL, EnrollmentToken: "token", Name: "node", Version: "1.0.0",
				Capabilities: []core.Engine{core.EngineXray}, Labels: map[string]string{},
			},
			http:     server.Client(),
			executor: executor,
		}
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := client.enroll(ctx, publicKey, privateKey); err != nil {
			t.Fatalf("enroll() error = %v", err)
		}
		if got := stringInSlice(core.AgentFeatureCoreLogs, captured.Features); got != wantsCoreLogs {
			t.Fatalf("core-logs advertised = %v; want %v (features: %v)", got, wantsCoreLogs, captured.Features)
		}
		if got := stringInSlice(core.AgentFeatureCoreLogStatus, captured.Features); got != wantsCoreLogs {
			t.Fatalf("core-log status advertised = %v; want %v (features: %v)", got, wantsCoreLogs, captured.Features)
		}
		if captured.OS == "" || captured.Arch != runtime.GOARCH {
			t.Fatalf("enrollment platform = %q/%q; want detected OS/%q", captured.OS, captured.Arch, runtime.GOARCH)
		}
		if !stringInSlice(core.AgentFeatureSelfUpgrade, captured.Features) || !stringInSlice(core.AgentFeaturePortTraffic, captured.Features) {
			t.Fatalf("self-upgrade or port-traffic feature missing: %v", captured.Features)
		}
	}

	t.Run("systemd", func(t *testing.T) { run(t, ServiceManagerSystemd, true) })
	t.Run("openrc", func(t *testing.T) { run(t, ServiceManagerOpenRC, true) })
}

func TestClientHeartbeatAdvertisesCoreLogsPerServiceManager(t *testing.T) {
	run := func(t *testing.T, kind string, wantsCoreLogs bool) {
		t.Helper()
		manager, err := NewServiceManager(kind)
		if err != nil {
			t.Fatal(err)
		}
		statePath := filepath.Join(t.TempDir(), "agent-state.json")
		client := &Client{
			config:   ClientConfig{Version: "1.0.0", StatePath: statePath},
			executor: &Executor{Services: manager},
			metrics:  NewMetricsCollector(),
			traffic:  NewTrafficManager(statePath),
		}
		outgoing := make(chan core.WireMessage, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.queueHeartbeat(ctx, outgoing); err != nil {
			t.Fatalf("queueHeartbeat() error = %v", err)
		}
		message := <-outgoing
		if message.Heartbeat == nil {
			t.Fatal("heartbeat message has no payload")
		}
		if message.Heartbeat.OS == "" || message.Heartbeat.Arch != runtime.GOARCH {
			t.Fatalf("heartbeat platform = %q/%q; want detected OS/%q", message.Heartbeat.OS, message.Heartbeat.Arch, runtime.GOARCH)
		}
		if got := stringInSlice(core.AgentFeatureCoreLogs, message.Heartbeat.Features); got != wantsCoreLogs {
			t.Fatalf("heartbeat core-logs advertised = %v; want %v (features: %v)", got, wantsCoreLogs, message.Heartbeat.Features)
		}
		if got := stringInSlice(core.AgentFeatureCoreLogStatus, message.Heartbeat.Features); got != wantsCoreLogs {
			t.Fatalf("heartbeat core-log status advertised = %v; want %v (features: %v)", got, wantsCoreLogs, message.Heartbeat.Features)
		}
		if !stringInSlice(core.AgentFeatureSelfUpgrade, message.Heartbeat.Features) || !stringInSlice(core.AgentFeaturePortTraffic, message.Heartbeat.Features) {
			t.Fatalf("heartbeat self-upgrade or port-traffic feature missing: %v", message.Heartbeat.Features)
		}
	}

	t.Run("systemd", func(t *testing.T) { run(t, ServiceManagerSystemd, true) })
	t.Run("openrc", func(t *testing.T) { run(t, ServiceManagerOpenRC, true) })
}
