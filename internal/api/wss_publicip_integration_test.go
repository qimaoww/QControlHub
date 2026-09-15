package api

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestWSSManagedPublicIPProbeUsesCurrentSessionCapabilityWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := store.Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	trustedProxies, err := authn.ParseTrustedProxies([]string{"127.0.0.1/32"})
	if err != nil {
		t.Fatalf("parse trusted proxy fixture: %v", err)
	}
	httpServer := httptest.NewServer(New(dataStore, Config{
		AdminToken: strings.Repeat("p", 48), TrustedProxies: trustedProxies,
		AgentBinary: []byte("test-agent-binary"), AgentVersion: "test-version",
		PublicIPProbe: core.PublicIPProbeConfig{
			IPv4Endpoint:         core.DefaultPublicIPProbeIPv4Endpoint,
			IPv4FallbackEndpoint: core.DefaultPublicIPProbeIPv4Fallback,
			IPv6Endpoint:         core.DefaultPublicIPProbeIPv6Endpoint,
			IPv6FallbackEndpoint: core.DefaultPublicIPProbeIPv6Fallback,
			IntervalSeconds:      300,
		},
	}).Handler())
	defer httpServer.Close()

	agentID, privateKey := enrollWSSTestSourceAgent(t, ctx, httpServer.URL, databaseURL, dataStore, "wss-public-ip-capability")
	connection := dialPublicIPProbeWSS(t, ctx, httpServer.URL, agentID, privateKey)
	defer connection.Close(websocket.StatusNormalClosure, "test complete")

	managedMetrics := core.HostMetrics{
		PublicIPv4: "198.35.26.96", PublicIPv4Source: core.PublicIPProbeSourceControlPlane,
		PublicIPv6: "2001:4860:4860::8888", PublicIPv6Source: core.PublicIPProbeSourceControlPlane,
	}
	// Metrics-only traffic before the current connection's first complete
	// heartbeat cannot inherit the enrollment or previous session's features.
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireMetrics, Metrics: &managedMetrics}); err != nil {
		t.Fatalf("write pre-heartbeat metrics: %v", err)
	}
	waitRawPublicIPProbeRowAPI(t, ctx, databaseURL, agentID, "", "", "", "", nil)

	// The first capable heartbeat establishes this session's capability but
	// precedes configuration delivery, so managed values in that same envelope
	// are still untrusted.
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
		Version: "capable-v1", Features: []string{core.AgentFeatureManagedPublicIPProbe}, Metrics: &managedMetrics,
	}}); err != nil {
		t.Fatalf("write capable heartbeat: %v", err)
	}
	var probeConfig core.WireMessage
	if err := wsjson.Read(ctx, connection, &probeConfig); err != nil {
		t.Fatalf("read managed probe config: %v", err)
	}
	if probeConfig.Type != core.WirePublicIPProbe || probeConfig.PublicIPProbe == nil || probeConfig.PublicIPProbe.IPv4Endpoint != core.DefaultPublicIPProbeIPv4Endpoint || probeConfig.PublicIPProbe.IPv4FallbackEndpoint != core.DefaultPublicIPProbeIPv4Fallback || probeConfig.PublicIPProbe.IPv6Endpoint != core.DefaultPublicIPProbeIPv6Endpoint || probeConfig.PublicIPProbe.IPv6FallbackEndpoint != core.DefaultPublicIPProbeIPv6Fallback {
		t.Fatalf("managed probe message = %+v", probeConfig)
	}
	waitRawPublicIPProbeRowAPI(t, ctx, databaseURL, agentID, "", "", "", "", []string{core.AgentFeatureManagedPublicIPProbe})

	// Once the config was delivered, metrics-only and complete heartbeat paths
	// both accept the two explicitly configured families.
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireMetrics, Metrics: &managedMetrics}); err != nil {
		t.Fatalf("write trusted metrics: %v", err)
	}
	waitRawPublicIPProbeRowAPI(t, ctx, databaseURL, agentID, "198.35.26.96", core.PublicIPProbeSourceControlPlane, "2001:4860:4860::8888", core.PublicIPProbeSourceControlPlane, nil)
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
		Version: "capable-v1", Features: []string{core.AgentFeatureManagedPublicIPProbe}, Metrics: &managedMetrics,
	}}); err != nil {
		t.Fatalf("write stable capable heartbeat: %v", err)
	}
	waitRawPublicIPProbeRowAPI(t, ctx, databaseURL, agentID, "198.35.26.96", core.PublicIPProbeSourceControlPlane, "2001:4860:4860::8888", core.PublicIPProbeSourceControlPlane, []string{core.AgentFeatureManagedPublicIPProbe})

	// An in-session downgrade is persisted fail closed and forces a reconnect;
	// the current connection can no longer use its previously delivered config.
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
		Version: "legacy", Metrics: &managedMetrics,
	}}); err != nil {
		t.Fatalf("write downgraded heartbeat: %v", err)
	}
	waitRawPublicIPProbeRowAPI(t, ctx, databaseURL, agentID, "", "", "", "", []string{})
	var closed core.WireMessage
	if err := wsjson.Read(ctx, connection, &closed); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("downgraded connection close status = %v, want policy violation", err)
	}

	// A legacy reconnect starts without managed config. Its pre-heartbeat
	// managed metrics remain rejected, while the v1 missing-source local probe
	// compatibility path survives. A queued task is the next server message,
	// proving no managed config was inserted before it.
	legacy := dialPublicIPProbeWSS(t, ctx, httpServer.URL, agentID, privateKey)
	defer legacy.Close(websocket.StatusNormalClosure, "test complete")
	if err := wsjson.Write(ctx, legacy, core.WireMessage{Type: core.WireMetrics, Metrics: &managedMetrics}); err != nil {
		t.Fatalf("write legacy pre-heartbeat metrics: %v", err)
	}
	waitRawPublicIPProbeRowAPI(t, ctx, databaseURL, agentID, "", "", "", "", nil)
	task, err := dataStore.CreateTask(ctx, core.TaskRequest{AgentID: agentID, Action: core.ActionStatus, Engine: core.EngineMihomo})
	if err != nil {
		t.Fatalf("create legacy dispatch sentinel: %v", err)
	}
	legacyMetrics := core.HostMetrics{
		PublicIPv4: "198.35.26.96",
		PublicIPv6: "2001:4860:4860::8888", PublicIPv6Source: core.PublicIPProbeSourceControlPlane,
	}
	if err := wsjson.Write(ctx, legacy, core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
		Version: "legacy", Metrics: &legacyMetrics,
	}}); err != nil {
		t.Fatalf("write legacy heartbeat: %v", err)
	}
	var dispatched core.WireMessage
	if err := wsjson.Read(ctx, legacy, &dispatched); err != nil {
		t.Fatalf("read legacy task sentinel: %v", err)
	}
	if dispatched.Type != core.WireTask || dispatched.Task == nil || dispatched.Task.ID != task.ID {
		t.Fatalf("legacy next message = %+v; want task %s and no managed config", dispatched, task.ID)
	}
	waitRawPublicIPProbeRowAPI(t, ctx, databaseURL, agentID, "198.35.26.96", core.PublicIPProbeSourceAgent, "", "", []string{})
}

func TestWSSManagedAgentPolicyDeliversValidPolicyWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := store.Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	httpServer := httptest.NewServer(New(dataStore, Config{AdminToken: strings.Repeat("m", 48)}).Handler())
	defer httpServer.Close()
	agentID, privateKey := enrollWSSTestSourceAgent(t, ctx, httpServer.URL, databaseURL, dataStore, "wss-managed-policy")
	connection := dialPublicIPProbeWSS(t, ctx, httpServer.URL, agentID, privateKey)
	defer connection.Close(websocket.StatusNormalClosure, "test complete")
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
		Version: "managed-policy-v1", Features: []string{core.AgentFeatureManagedPolicy},
	}}); err != nil {
		t.Fatalf("write managed-policy heartbeat: %v", err)
	}
	var message core.WireMessage
	if err := wsjson.Read(ctx, connection, &message); err != nil {
		t.Fatalf("read managed Agent policy: %v", err)
	}
	if message.Type != core.WireAgentPolicy || message.AgentPolicy == nil {
		t.Fatalf("managed Agent policy = %+v", message)
	}
	if err := message.AgentPolicy.Validate(); err != nil {
		t.Fatalf("managed Agent policy is invalid: %+v: %v", message.AgentPolicy, err)
	}
}

func dialPublicIPProbeWSS(t *testing.T, ctx context.Context, base, agentID string, privateKey ed25519.PrivateKey) *websocket.Conn {
	t.Helper()
	websocketURL := "ws" + strings.TrimPrefix(base, "http") + "/agent/v1/connect"
	handshake, _ := http.NewRequestWithContext(ctx, http.MethodGet, websocketURL, nil)
	if err := authn.SignRequest(handshake, nil, agentID, privateKey, time.Now().UTC()); err != nil {
		t.Fatalf("sign WSS handshake: %v", err)
	}
	handshake.Header.Set("X-Forwarded-For", "192.0.2.44")
	connection, response, err := websocket.Dial(ctx, websocketURL, &websocket.DialOptions{
		HTTPHeader: handshake.Header, Subprotocols: []string{"qcontrolhub.agent.v1"},
	})
	if err != nil {
		if response != nil {
			t.Fatalf("dial WSS: %v (%s)", err, response.Status)
		}
		t.Fatalf("dial WSS: %v", err)
	}
	var hello core.WireMessage
	if err := wsjson.Read(ctx, connection, &hello); err != nil || hello.Type != core.WireHello || hello.PublicIPProbe != nil {
		t.Fatalf("initial capability-neutral hello = %+v, %v", hello, err)
	}
	return connection
}

func waitRawPublicIPProbeRowAPI(t *testing.T, ctx context.Context, databaseURL, agentID, ipv4, ipv4Source, ipv6, ipv6Source string, features []string) {
	t.Helper()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect for raw Agent row: %v", err)
	}
	defer connection.Close(ctx)
	wantFeatures, _ := json.Marshal(features)
	for attempt := 0; attempt < 100; attempt++ {
		var gotIPv4, gotIPv4Source, gotIPv6, gotIPv6Source *string
		var featuresMatch bool
		err = connection.QueryRow(ctx, `
			SELECT live.metrics->>'public_ipv4', live.metrics->>'public_ipv4_source',
			       live.metrics->>'public_ipv6', live.metrics->>'public_ipv6_source',
			       CASE WHEN $2::jsonb IS NULL THEN true ELSE agents.features=$2::jsonb END
			FROM agents
			JOIN agent_live_state live ON live.agent_id = agents.id
			WHERE agents.id=$1`, agentID, nullableJSON(features, wantFeatures)).Scan(&gotIPv4, &gotIPv4Source, &gotIPv6, &gotIPv6Source, &featuresMatch)
		deref := func(value *string) string {
			if value == nil {
				return ""
			}
			return *value
		}
		if err == nil && deref(gotIPv4) == ipv4 && deref(gotIPv4Source) == ipv4Source && deref(gotIPv6) == ipv6 && deref(gotIPv6Source) == ipv6Source && featuresMatch {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("raw Agent public IP row did not reach ipv4=%q source=%q ipv6=%q source=%q features=%v: %v", ipv4, ipv4Source, ipv6, ipv6Source, features, err)
}

func nullableJSON(features []string, encoded []byte) any {
	if features == nil {
		return nil
	}
	return encoded
}
