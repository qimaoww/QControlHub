//go:build linux

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestRunWebSocketAppliesCapabilityGatedPublicIPProbeMessage(t *testing.T) {
	requireAgentRoot(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	heartbeatSeen := make(chan core.HeartbeatRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(w, request, &websocket.AcceptOptions{Subprotocols: []string{"qcontrolhub.agent.v1"}})
		if err != nil {
			return
		}
		defer connection.Close(websocket.StatusNormalClosure, "test complete")
		if err := wsjson.Write(request.Context(), connection, core.WireMessage{Type: core.WireHello}); err != nil {
			return
		}
		var heartbeat core.WireMessage
		if err := wsjson.Read(request.Context(), connection, &heartbeat); err != nil || heartbeat.Type != core.WireHeartbeat || heartbeat.Heartbeat == nil {
			return
		}
		heartbeatSeen <- *heartbeat.Heartbeat
		probe := core.PublicIPProbeConfig{IPv4Endpoint: "https://probe.example.test/v4", IntervalSeconds: 300}
		if err := wsjson.Write(request.Context(), connection, core.WireMessage{Type: core.WirePublicIPProbe, PublicIPProbe: &probe}); err != nil {
			return
		}
		var final core.WireMessage
		_ = wsjson.Read(request.Context(), connection, &final)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		ServerURL: server.URL, StatePath: filepath.Join(t.TempDir(), "agent-state.json"),
		HeartbeatEvery: 30 * time.Second, MetricsEvery: 30 * time.Second,
	}, testClientExecutor(t))
	if err != nil {
		t.Fatalf("new Client: %v", err)
	}
	client.creds = credentials{AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(privateKey)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- client.runWebSocket(ctx) }()

	select {
	case heartbeat := <-heartbeatSeen:
		found := false
		for _, feature := range heartbeat.Features {
			found = found || feature == core.AgentFeatureManagedPublicIPProbe
		}
		if !found {
			t.Fatalf("initial heartbeat features = %v; managed capability missing", heartbeat.Features)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for initial heartbeat")
	}
	for {
		client.publicIP.mu.Lock()
		endpoint := ""
		if len(client.publicIP.config.endpoints[0]) > 0 {
			endpoint = client.publicIP.config.endpoints[0][0]
		}
		source := client.publicIP.config.source
		client.publicIP.mu.Unlock()
		if endpoint == "https://probe.example.test/v4" && source == core.PublicIPProbeSourceControlPlane {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("WSS ended before managed config applied: %v", err)
		case <-ctx.Done():
			t.Fatal("managed public IP probe message was not applied")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-finished; err != nil {
		t.Fatalf("runWebSocket after cancellation: %v", err)
	}
}
