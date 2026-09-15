package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
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

// TestWSSMirrorFeatureDowngradeWithPostgreSQL models the real ordering bug: the
// stored Agent features still advertise mihomo-development-source-v1 when the
// websocket reconnects, so dispatch must wait until this connection's first
// heartbeat is persisted. Before that heartbeat no task is delivered; after a
// heartbeat that drops the feature, the running mirror task is failed
// atomically and the pending mirror task stays pending, while an official
// omitted-source install becomes dispatchable.
func TestWSSMirrorFeatureDowngradeWithPostgreSQL(t *testing.T) {
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

	adminToken := strings.Repeat("d", 48)
	trustedProxies, err := authn.ParseTrustedProxies([]string{"127.0.0.1/32"})
	if err != nil {
		t.Fatalf("parse trusted proxy fixture: %v", err)
	}
	httpServer := httptest.NewServer(New(dataStore, Config{
		AdminToken:     adminToken,
		TrustedProxies: trustedProxies,
		AgentBinary:    []byte("test-agent-binary"),
		AgentVersion:   "test-version",
	}).Handler())
	defer httpServer.Close()

	// Agent A holds a running mirror install and a pending omitted-source
	// install. After the reconnecting Agent drops the source feature, the
	// running mirror must be failed, the pending mirror skipped, and the
	// official task dispatched.
	a, aKey := enrollWSSTestSourceAgent(t, ctx, httpServer.URL, databaseURL, dataStore, "wss-downgrade-a")
	running, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: a, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	})
	if err != nil {
		t.Fatalf("create running mirror task: %v", err)
	}
	claimed, err := dataStore.ClaimTask(ctx, a)
	if err != nil || claimed == nil || claimed.ID != running.ID {
		t.Fatalf("claim running mirror before reconnect = %+v, %v; want %s", claimed, err, running.ID)
	}
	official, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: a, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment,
	})
	if err != nil || official.CoreSource != string(core.CoreSourceOfficial) {
		t.Fatalf("create official omitted-source task = %+v, %v; want official source", official, err)
	}
	connA, dispatchedA := downgradeReconnect(t, ctx, httpServer.URL, a, aKey, func() {
		if got, err := dataStore.GetTask(ctx, running.ID); err != nil || got.Status != core.TaskRunning {
			t.Fatalf("running mirror was dispatched before the first heartbeat: %+v, %v", got, err)
		}
	})
	if dispatchedA == nil {
		t.Fatal("expected an official task dispatch after the downgraded heartbeat")
	}
	if dispatchedA.Task.ID != official.ID || dispatchedA.Task.CoreSource != string(core.CoreSourceOfficial) {
		t.Fatalf("expected official task %s after heartbeat, got %s (source %q)", official.ID, dispatchedA.Task.ID, dispatchedA.Task.CoreSource)
	}
	assertRawTaskTerminalCleanAPI(t, ctx, databaseURL, running.ID)
	assertEmptyAgentFeaturesAndMirrorBlocked(t, ctx, dataStore, a)
	connA.Close(websocket.StatusNormalClosure, "scenario complete")

	// Agent B holds only a pending mirror install that must stay pending and
	// never be dispatched after the source feature is dropped.
	b, bKey := enrollWSSTestSourceAgent(t, ctx, httpServer.URL, databaseURL, dataStore, "wss-downgrade-b")
	pending, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: b, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	})
	if err != nil {
		t.Fatalf("create pending mirror task: %v", err)
	}
	connB, dispatchedB := downgradeReconnect(t, ctx, httpServer.URL, b, bKey, nil)
	if dispatchedB != nil {
		t.Fatalf("pending mirror task was dispatched after downgrade: %+v", dispatchedB.Task)
	}
	if got, err := dataStore.GetTask(ctx, pending.ID); err != nil || got.Status != core.TaskPending {
		t.Fatalf("pending mirror after downgraded heartbeat = %+v, %v; want pending", got, err)
	}
	assertEmptyAgentFeaturesAndMirrorBlocked(t, ctx, dataStore, b)
	connB.Close(websocket.StatusNormalClosure, "scenario complete")
}

// assertEmptyAgentFeaturesAndMirrorBlocked verifies that an empty-feature
// heartbeat cleared the stale source capability and that the API mirror gate
// now rejects a mirror install.
func assertEmptyAgentFeaturesAndMirrorBlocked(t *testing.T, ctx context.Context, dataStore *store.Store, agentID string) {
	t.Helper()
	current, err := dataStore.GetAgent(ctx, agentID)
	if err != nil || len(current.Features) != 0 {
		t.Fatalf("agent features after empty-feature heartbeat = %+v, %v; want empty", current.Features, err)
	}
	if _, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agentID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("mirror create after empty-feature heartbeat = %v, want ErrConflict", err)
	}
}

// enrollWSSTestSourceAgent enrolls a Mihomo-capable Agent advertising the
// negotiated mihomo-development-source-v1 feature and registers cleanup.
func enrollWSSTestSourceAgent(t *testing.T, ctx context.Context, base, databaseURL string, dataStore *store.Store, name string) (string, ed25519.PrivateKey) {
	t.Helper()
	enrollment, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: name, TTLMinutes: 5, MaxUses: 1})
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	body, _ := json.Marshal(core.EnrollRequest{
		Name: name, OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo},
		Features:     []string{core.AgentFeatureSelfUpgrade, core.AgentFeatureMihomoDevelopmentSource},
		PublicKey:    authn.EncodePublicKey(publicKey),
	})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/agent/v1/enroll", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+enrollment.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("enroll %s: %v", name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("enroll %s status = %s", name, response.Status)
	}
	var enrolled core.EnrollResponse
	if err := json.NewDecoder(response.Body).Decode(&enrolled); err != nil {
		t.Fatalf("decode enrollment %s: %v", name, err)
	}
	t.Cleanup(func() { cleanupTaskAPIFixture(t, databaseURL, enrollment.ID, []string{enrolled.AgentID}) })
	return enrolled.AgentID, privateKey
}

// downgradeReconnect dials the websocket with the given Agent key, reads Hello,
// runs an optional preHeartbeat assertion, sends a heartbeat that drops
// mihomo-development-source-v1, and returns the connection plus the first task
// message that arrives afterwards (nil if none arrives).
func downgradeReconnect(t *testing.T, ctx context.Context, base, agentID string, privateKey ed25519.PrivateKey, preHeartbeat func()) (*websocket.Conn, *core.WireMessage) {
	t.Helper()
	websocketURL := "ws" + strings.TrimPrefix(base, "http") + "/agent/v1/connect"
	handshake, _ := http.NewRequestWithContext(ctx, http.MethodGet, websocketURL, nil)
	if err := authn.SignRequest(handshake, nil, agentID, privateKey, time.Now().UTC()); err != nil {
		t.Fatalf("sign WSS handshake: %v", err)
	}
	handshake.Header.Set("X-Forwarded-For", "192.0.2.44")
	connection, dialResponse, err := websocket.Dial(ctx, websocketURL, &websocket.DialOptions{
		HTTPHeader: handshake.Header, Subprotocols: []string{"qcontrolhub.agent.v1"},
	})
	if err != nil {
		if dialResponse != nil {
			t.Fatalf("dial WSS: %v (%s)", err, dialResponse.Status)
		}
		t.Fatalf("dial WSS: %v", err)
	}
	var hello core.WireMessage
	if err := wsjson.Read(ctx, connection, &hello); err != nil || hello.Type != core.WireHello {
		t.Fatalf("read hello: message=%+v error=%v", hello, err)
	}
	if preHeartbeat != nil {
		preHeartbeat()
	}
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
		Version: "downgraded",
	}}); err != nil {
		t.Fatalf("write downgraded heartbeat: %v", err)
	}
	postCtx, cancelPost := context.WithTimeout(ctx, 2*time.Second)
	defer cancelPost()
	var dispatched core.WireMessage
	if err := wsjson.Read(postCtx, connection, &dispatched); err != nil {
		return connection, nil
	}
	return connection, &dispatched
}

// assertRawTaskTerminalCleanAPI reads the underlying tasks row directly and
// asserts the terminal-state cleanup and unknown-outcome wording. GetTask does
// not expose lease_id or config_content, so the raw columns must be checked
// here to avoid a false positive during the downgrade-fail regression.
func assertRawTaskTerminalCleanAPI(t *testing.T, ctx context.Context, databaseURL, taskID string) {
	t.Helper()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect for raw terminal task: %v", err)
	}
	defer connection.Close(ctx)
	var status string
	var finishedAt *time.Time
	var leaseID *string
	var configContent *string
	var errMsg string
	if err := connection.QueryRow(ctx, `
		SELECT status, finished_at, lease_id, config_content, COALESCE(error,'')
		FROM tasks WHERE id=$1`, taskID).Scan(&status, &finishedAt, &leaseID, &configContent, &errMsg); err != nil {
		t.Fatalf("read raw terminal task: %v", err)
	}
	if status != string(core.TaskFailed) {
		t.Fatalf("raw terminal task status = %q, want %q", status, core.TaskFailed)
	}
	if finishedAt == nil {
		t.Fatalf("raw terminal task finished_at is NULL")
	}
	if leaseID != nil {
		t.Fatalf("raw terminal task lease must be NULL, got %q", *leaseID)
	}
	if configContent != nil {
		t.Fatalf("raw terminal task config_content must be NULL, got %q", *configContent)
	}
	if !strings.Contains(errMsg, core.AgentFeatureMihomoDevelopmentSource) || !strings.Contains(errMsg, "cannot be safely resumed") || !strings.Contains(errMsg, "unknown whether the previous Agent executed it") {
		t.Fatalf("raw terminal task error = %q, want feature + safe-resume + unknown-outcome wording", errMsg)
	}
	if strings.Contains(errMsg, "was not executed") {
		t.Fatalf("raw terminal task error must not claim the task was never executed: %q", errMsg)
	}
}
