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
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type wssWireFrame struct {
	msg core.WireMessage
	err error
}

func startWSSFrameReader(ctx context.Context, conn *websocket.Conn) <-chan wssWireFrame {
	ch := make(chan wssWireFrame, 16)
	go func() {
		defer close(ch)
		for {
			var msg core.WireMessage
			err := wsjson.Read(ctx, conn, &msg)
			select {
			case ch <- wssWireFrame{msg: msg, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

func readWSSFrameType(t *testing.T, frames <-chan wssWireFrame, want string, timeout time.Duration) core.WireMessage {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatalf("WSS stream closed while waiting for %q", want)
			}
			if frame.err != nil {
				t.Fatalf("WSS read failed while waiting for %q: %v", want, frame.err)
			}
			if frame.msg.Type == want {
				return frame.msg
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for WSS frame %q", want)
		}
	}
}

func assertNoWSSFrame(t *testing.T, frames <-chan wssWireFrame, wait time.Duration, contextLabel string) {
	t.Helper()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatalf("%s: connection closed unexpectedly", contextLabel)
		}
		if frame.err != nil {
			t.Fatalf("%s: unexpected read error: %v", contextLabel, frame.err)
		}
		t.Fatalf("%s: unexpected WSS frame %q: %+v", contextLabel, frame.msg.Type, frame.msg)
	case <-timer.C:
	}
}

func enrollWSSAgentForUser(t *testing.T, ctx context.Context, client configScopeAPIClient, db *store.Store, name string, features []string) (core.Agent, ed25519.PrivateKey) {
	t.Helper()
	var token core.EnrollmentTokenCreated
	client.call("POST", "/enrollment-tokens", map[string]string{"name": name}, http.StatusCreated, &token)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{
		Name:         name,
		OS:           "linux",
		Arch:         "amd64",
		Capabilities: []core.Engine{core.EngineMihomo},
		Features:     features,
		PublicKey:    authn.EncodePublicKey(pub),
	}, token.Token)
	if err != nil {
		t.Fatalf("EnrollAgent %s: %v", name, err)
	}
	return agent, priv
}

func TestHotPolicyReloadIsolationAndLegacyFallbackWithPostgreSQL(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	cfg := configScopeFixtureConfig()
	cfg.PublicIPProbe = core.PublicIPProbeConfig{
		IPv4Endpoint:    core.DefaultPublicIPProbeIPv4Endpoint,
		IntervalSeconds: 300,
	}
	server := New(db, cfg)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	loginLive := func(username, userID string) configScopeAPIClient {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"username": username, "token": configScopeFixturePassword})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login %s on live server: %d %s", username, rec.Code, rec.Body.String())
		}
		var session struct {
			CSRF string `json:"csrf_token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		return configScopeAPIClient{t: t, handler: server.Handler(), userID: userID, cookie: rec.Result().Cookies()[0], csrf: session.CSRF}
	}
	aliceLive := loginLive("alice", alice.userID)
	bobLive := loginLive("bob", bob.userID)

	aliceModernFeatures := []string{
		core.AgentFeatureSharedTraffic,
		core.AgentFeatureSharedEngines,
		core.AgentFeatureManagedConfigRead,
		core.AgentFeatureIndependentEgress,
		core.AgentFeatureSharedCoreInstances,
		core.AgentFeatureSharedCoreLifecycle,
		core.AgentFeatureManagedPolicy,
		core.AgentFeatureManagedPublicIPProbe,
	}
	aliceModern, aliceModernKey := enrollWSSAgentForUser(t, ctx, aliceLive, db, "alice-modern", aliceModernFeatures)
	aliceLegacy, aliceLegacyKey := enrollWSSAgentForUser(t, ctx, aliceLive, db, "alice-legacy", []string{
		core.AgentFeatureManagedPublicIPProbe,
	})
	bobModern, bobModernKey := enrollWSSAgentForUser(t, ctx, bobLive, db, "bob-modern", []string{
		core.AgentFeatureManagedPolicy,
		core.AgentFeatureManagedPublicIPProbe,
	})
	if err := db.Heartbeat(ctx, aliceModern.ID, core.HeartbeatRequest{
		Features: aliceModernFeatures,
		Runtime:  map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}},
	}); err != nil {
		t.Fatalf("seed aliceModern heartbeat before share: %v", err)
	}
	// Grant Bob access to Alice's modern node so both have a shared relationship;
	// Bob's account settings updates must never affect Alice's borrowed node.
	grantConfigScopeAPIAgent(t, ctx, db, aliceModern, bobLive, 21001)

	aliceModernConn := dialPublicIPProbeWSS(t, ctx, httpServer.URL, aliceModern.ID, aliceModernKey)
	defer aliceModernConn.Close(websocket.StatusNormalClosure, "done")
	aliceLegacyConn := dialPublicIPProbeWSS(t, ctx, httpServer.URL, aliceLegacy.ID, aliceLegacyKey)
	defer aliceLegacyConn.Close(websocket.StatusNormalClosure, "done")
	bobModernConn := dialPublicIPProbeWSS(t, ctx, httpServer.URL, bobModern.ID, bobModernKey)
	defer bobModernConn.Close(websocket.StatusNormalClosure, "done")

	aliceModernFrames := startWSSFrameReader(ctx, aliceModernConn)
	aliceLegacyFrames := startWSSFrameReader(ctx, aliceLegacyConn)
	bobModernFrames := startWSSFrameReader(ctx, bobModernConn)

	for _, item := range []struct {
		conn     *websocket.Conn
		features []string
	}{
		{aliceModernConn, aliceModernFeatures},
		{aliceLegacyConn, []string{core.AgentFeatureManagedPublicIPProbe}},
		{bobModernConn, []string{core.AgentFeatureManagedPolicy, core.AgentFeatureManagedPublicIPProbe}},
	} {
		if err := wsjson.Write(ctx, item.conn, core.WireMessage{
			Type: core.WireHeartbeat,
			Heartbeat: &core.HeartbeatRequest{
				Version:  "test-v1",
				Features: item.features,
			},
		}); err != nil {
			t.Fatalf("write initial heartbeat: %v", err)
		}
	}

	_ = readWSSFrameType(t, aliceModernFrames, core.WirePublicIPProbe, 3*time.Second)
	_ = readWSSFrameType(t, aliceModernFrames, core.WireAgentPolicy, 3*time.Second)
	_ = readWSSFrameType(t, aliceLegacyFrames, core.WirePublicIPProbe, 3*time.Second)
	_ = readWSSFrameType(t, bobModernFrames, core.WirePublicIPProbe, 3*time.Second)
	_ = readWSSFrameType(t, bobModernFrames, core.WireAgentPolicy, 3*time.Second)

	// Drain any initial quiet period before triggering settings hot reload.
	assertNoWSSFrame(t, aliceModernFrames, 150*time.Millisecond, "pre-update aliceModern")
	assertNoWSSFrame(t, bobModernFrames, 150*time.Millisecond, "pre-update bobModern")

	var aliceCurrent core.PanelSettings
	aliceLive.call("GET", "/settings", nil, http.StatusOK, &aliceCurrent)
	aliceUpdated := aliceCurrent
	aliceUpdated.AgentHeartbeatIntervalSeconds = 30
	aliceUpdated.AgentOfflineThresholdSeconds = 90
	aliceUpdated.AgentMetricsIntervalSeconds = 15
	aliceUpdated.PublicIPProbeIntervalSeconds = 900
	aliceLive.call("PUT", "/settings", aliceUpdated, http.StatusOK, nil)

	// aliceModern receives updated probe & policy in-place without disconnecting.
	probeMsg := readWSSFrameType(t, aliceModernFrames, core.WirePublicIPProbe, 3*time.Second)
	if probeMsg.PublicIPProbe == nil || probeMsg.PublicIPProbe.IntervalSeconds != 900 {
		t.Fatalf("aliceModern updated probe = %+v, want IntervalSeconds=900", probeMsg.PublicIPProbe)
	}
	policyMsg := readWSSFrameType(t, aliceModernFrames, core.WireAgentPolicy, 3*time.Second)
	if policyMsg.AgentPolicy == nil || policyMsg.AgentPolicy.HeartbeatIntervalSeconds != 30 || policyMsg.AgentPolicy.MetricsIntervalSeconds != 15 {
		t.Fatalf("aliceModern updated policy = %+v", policyMsg.AgentPolicy)
	}

	// aliceLegacy lacks managed policy support and must be cleanly disconnected.
	select {
	case frame, ok := <-aliceLegacyFrames:
		if ok && frame.err == nil {
			t.Fatalf("aliceLegacy received unexpected message instead of disconnect: %+v", frame.msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("aliceLegacy was not disconnected after policy update")
	}

	// Bob's own node must not receive Alice's settings update.
	assertNoWSSFrame(t, bobModernFrames, 250*time.Millisecond, "bobModern after alice settings update")

	// Bob updates Bob's settings: bobModern gets Bob's update in-place while borrowed aliceModern is unaffected.
	var bobCurrent core.PanelSettings
	bobLive.call("GET", "/settings", nil, http.StatusOK, &bobCurrent)
	bobUpdated := bobCurrent
	bobUpdated.AgentMetricsIntervalSeconds = 5
	bobUpdated.PublicIPProbeIntervalSeconds = 3600
	bobLive.call("PUT", "/settings", bobUpdated, http.StatusOK, nil)

	bobProbe := readWSSFrameType(t, bobModernFrames, core.WirePublicIPProbe, 3*time.Second)
	if bobProbe.PublicIPProbe == nil || bobProbe.PublicIPProbe.IntervalSeconds != 3600 {
		t.Fatalf("bobModern updated probe = %+v, want IntervalSeconds=3600", bobProbe.PublicIPProbe)
	}
	bobPolicy := readWSSFrameType(t, bobModernFrames, core.WireAgentPolicy, 3*time.Second)
	if bobPolicy.AgentPolicy == nil || bobPolicy.AgentPolicy.MetricsIntervalSeconds != 5 {
		t.Fatalf("bobModern updated policy = %+v, want MetricsIntervalSeconds=5", bobPolicy.AgentPolicy)
	}
	assertNoWSSFrame(t, aliceModernFrames, 250*time.Millisecond, "borrowed aliceModern after bob settings update")
}

func TestWSSInboundTelemetryFloodProtectionWithPostgreSQL(t *testing.T) {
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
		t.Fatalf("parse trusted proxies: %v", err)
	}
	httpServer := httptest.NewServer(New(dataStore, Config{
		AdminToken:     strings.Repeat("p", 48),
		TrustedProxies: trustedProxies,
		AgentBinary:    []byte("test-agent-binary"),
		AgentVersion:   "test-version",
		PublicIPProbe: core.PublicIPProbeConfig{
			IPv4Endpoint:    core.DefaultPublicIPProbeIPv4Endpoint,
			IntervalSeconds: 300,
		},
	}).Handler())
	defer httpServer.Close()

	agentID, privKey := enrollWSSTestSourceAgent(t, ctx, httpServer.URL, databaseURL, dataStore, "wss-flood-test")
	conn := dialPublicIPProbeWSS(t, ctx, httpServer.URL, agentID, privKey)
	defer conn.Close(websocket.StatusNormalClosure, "test complete")
	frames := startWSSFrameReader(ctx, conn)

	if err := wsjson.Write(ctx, conn, core.WireMessage{
		Type: core.WireHeartbeat,
		Heartbeat: &core.HeartbeatRequest{
			Version:  "flood-v1",
			Features: []string{core.AgentFeatureManagedPublicIPProbe},
		},
	}); err != nil {
		t.Fatalf("write initial heartbeat: %v", err)
	}
	_ = readWSSFrameType(t, frames, core.WirePublicIPProbe, 3*time.Second)

	// Send well over burst (10) + max dropped telemetry frames (25) immediately.
	for i := 0; i < 60; i++ {
		if err := wsjson.Write(ctx, conn, core.WireMessage{
			Type: core.WireMetrics,
			Metrics: &core.HostMetrics{
				CollectedAt:  time.Now().UTC(),
				CPUAvailable: true,
				CPUPercent:   float64(i),
			},
		}); err != nil {
			break
		}
	}

	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatal("reader channel closed without receiving StatusPolicyViolation error")
			}
			if frame.err != nil {
				if websocket.CloseStatus(frame.err) != websocket.StatusPolicyViolation {
					t.Fatalf("flood close status = %v (err=%v); want StatusPolicyViolation", websocket.CloseStatus(frame.err), frame.err)
				}
				return
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for StatusPolicyViolation disconnect under telemetry flood")
		}
	}
}

func TestWSSChallengeSignalCompactTelemetryAndDuplicateSuppressionWithPostgreSQL(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, closeDatabase, fixtureUsers, err := configScopeFixtureDatabase(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = closeDatabase(cleanupCtx)
	})
	db, err := store.OpenWithConfigKey(ctx, databaseURL, true, strings.Repeat("s", 32))
	if err != nil {
		t.Fatalf("open isolated PostgreSQL: %v", err)
	}
	defer db.Close()

	cfg := configScopeFixtureConfig()
	cfg.PublicIPProbe = core.PublicIPProbeConfig{
		IPv4Endpoint:    core.DefaultPublicIPProbeIPv4Endpoint,
		IntervalSeconds: 300,
	}
	server := New(db, cfg)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	var alice core.User
	if fixtureUsers != nil {
		alice = fixtureUsers["alice"]
	} else {
		admin := configScopeAPIClient{t: t, handler: server.Handler(), token: configScopeFixtureAdminToken}
		admin.call("POST", "/users", configScopeFixtureUser("alice"), http.StatusCreated, &alice)
	}
	aliceScope := store.WithConfigScope(ctx, alice.ID, false)

	token, err := db.CreateEnrollmentToken(aliceScope, core.EnrollmentTokenRequest{Name: "wss-challenge-compact", TTLMinutes: 5, MaxUses: 1})
	if err != nil {
		t.Fatalf("CreateEnrollmentToken: %v", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	features := []string{
		core.AgentFeatureCertificates,
		core.AgentFeatureManagedPolicy,
		core.AgentFeatureManagedPublicIPProbe,
	}
	enrolled, err := db.EnrollAgent(ctx, core.EnrollRequest{
		Name:         "wss-challenge-compact",
		OS:           "linux",
		Arch:         "amd64",
		Capabilities: []core.Engine{core.EngineMihomo},
		Features:     features,
		PublicKey:    authn.EncodePublicKey(pub),
	}, token.Token)
	if err != nil {
		t.Fatalf("EnrollAgent: %v", err)
	}

	conn := dialPublicIPProbeWSS(t, ctx, httpServer.URL, enrolled.ID, priv)
	defer conn.Close(websocket.StatusNormalClosure, "test complete")
	frames := startWSSFrameReader(ctx, conn)

	// Send first heartbeat WITH full HostMetrics (including network interfaces & BBR).
	initialTS := time.Now().UTC().Truncate(time.Second)
	if err := wsjson.Write(ctx, conn, core.WireMessage{
		Type: core.WireHeartbeat,
		Heartbeat: &core.HeartbeatRequest{
			Version:  "compact-v1",
			Features: features,
			Metrics: &core.HostMetrics{
				CollectedAt:       initialTS,
				CPUAvailable:      true,
				CPUPercent:        10.0,
				MemoryAvailable:   true,
				MemoryUsedBytes:   4096,
				MemoryTotalBytes:  16384,
				NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.0.2.10"}}},
				BBR:               &core.SystemBBRStatus{CollectedAt: initialTS, Available: true, CongestionControl: "bbr"},
			},
		},
	}); err != nil {
		t.Fatalf("write initial heartbeat: %v", err)
	}

	_ = readWSSFrameType(t, frames, core.WirePublicIPProbe, 3*time.Second)
	_ = readWSSFrameType(t, frames, core.WireAgentPolicy, 3*time.Second)
	// No pending HTTP challenges: initial empty challenge broadcast must be suppressed.
	assertNoWSSFrame(t, frames, 250*time.Millisecond, "initial empty challenge suppression")

	// Create an active ACME order + HTTP-01 challenge, then trigger ChallengeReady via order cancel
	// (no real CA call occurs because RunCertificateMaintenance is not invoked).
	_, key, err := certificates.NewKey("ecdsa256")
	if err != nil {
		t.Fatalf("certificates.NewKey: %v", err)
	}
	account, err := db.CreateCertificateAccount(aliceScope, core.CertificateAccount{
		Name: "signal-test-ca", Kind: "acme", CA: "letsencrypt-staging",
	}, certificates.ACMEAccount{Key: key})
	if err != nil {
		t.Fatalf("CreateCertificateAccount: %v", err)
	}
	cert, err := db.CreateACMECertificate(aliceScope, core.Certificate{
		Name: "signal-http", Domains: []string{"sig.example.test"}, Method: "http-01",
		AgentID: enrolled.ID, AccountID: account.ID, KeyType: "ecdsa256",
	})
	if err != nil {
		t.Fatalf("CreateACMECertificate: %v", err)
	}
	orders, err := db.CertificateOrders(aliceScope, cert.ID)
	if err != nil || len(orders) == 0 {
		t.Fatalf("CertificateOrders = %v, %v", orders, err)
	}

	pgConn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer pgConn.Close(ctx)
	if _, err := pgConn.Exec(ctx, `
		INSERT INTO certificate_http_challenges(order_id,agent_id,domain,token,value,expires_at)
		VALUES($1,$2,'sig.example.test','tok-1','val-1',now()+interval '15 minutes')`,
		orders[0].ID, enrolled.ID); err != nil {
		t.Fatalf("insert test http challenge: %v", err)
	}
	// Disconnect and reconnect to pick up the non-empty challenge on first heartbeat without waiting 10s,
	// or push via TrafficRefresh / next heartbeat? Reconnect tests immediate non-empty push on connect,
	// and cancelling the order tests immediate post-commit ChallengeReady clear push!
	conn.Close(websocket.StatusNormalClosure, "reconnect for active challenge")
	for range frames {
	}

	conn2 := dialPublicIPProbeWSS(t, ctx, httpServer.URL, enrolled.ID, priv)
	defer conn2.Close(websocket.StatusNormalClosure, "test complete")
	frames2 := startWSSFrameReader(ctx, conn2)

	if err := wsjson.Write(ctx, conn2, core.WireMessage{
		Type: core.WireHeartbeat,
		Heartbeat: &core.HeartbeatRequest{
			Version:  "compact-v1",
			Features: features,
		},
	}); err != nil {
		t.Fatalf("write reconnect heartbeat: %v", err)
	}
	_ = readWSSFrameType(t, frames2, core.WirePublicIPProbe, 3*time.Second)
	_ = readWSSFrameType(t, frames2, core.WireAgentPolicy, 3*time.Second)
	activeChal := readWSSFrameType(t, frames2, core.WireCertificateChallenges, 3*time.Second)
	if len(activeChal.CertificateChallenges) != 1 || activeChal.CertificateChallenges[0].Token != "tok-1" {
		t.Fatalf("active challenges = %+v, want 1 challenge tok-1", activeChal.CertificateChallenges)
	}

	// Canceling the order emits ChallengeReady immediately (<2s vs 10s idle tick) to clear the challenge.
	startCancel := time.Now()
	if err := db.ContinueCertificateOrder(aliceScope, cert.ID, orders[0].ID, true); err != nil {
		t.Fatalf("ContinueCertificateOrder cancel: %v", err)
	}
	clearedChal := readWSSFrameType(t, frames2, core.WireCertificateChallenges, 2500*time.Millisecond)
	if elapsed := time.Since(startCancel); elapsed >= 4*time.Second {
		t.Fatalf("challenge clear push took %v, expected immediate signal wake", elapsed)
	}
	if len(clearedChal.CertificateChallenges) != 0 {
		t.Fatalf("cleared challenges = %+v, want empty", clearedChal.CertificateChallenges)
	}
	assertNoWSSFrame(t, frames2, 250*time.Millisecond, "suppressed duplicate empty challenge")

	// Send compact WireMetrics (omits NetworkInterfaces/BBR); verify scalars update & snapshots survive in DB.
	if err := wsjson.Write(ctx, conn2, core.WireMessage{
		Type: core.WireMetrics,
		Metrics: &core.HostMetrics{
			CollectedAt:      initialTS.Add(time.Second),
			CPUAvailable:     true,
			CPUPercent:       77.5,
			MemoryAvailable:  true,
			MemoryUsedBytes:  8192,
			MemoryTotalBytes: 16384,
		},
	}); err != nil {
		t.Fatalf("write compact metrics: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var raw []byte
		err := pgConn.QueryRow(ctx, `SELECT metrics FROM agent_live_state WHERE agent_id=$1`, enrolled.ID).Scan(&raw)
		if err == nil && len(raw) > 0 {
			stored, getErr := db.GetAgent(aliceScope, enrolled.ID)
			if getErr == nil && stored.Metrics.CPUPercent == 77.5 && stored.Metrics.MemoryUsedBytes == 8192 {
				if len(stored.Metrics.NetworkInterfaces) != 1 || stored.Metrics.NetworkInterfaces[0].Name != "eth0" {
					t.Fatalf("compact WireMetrics lost network_interfaces in DB: %+v", stored.Metrics.NetworkInterfaces)
				}
				if stored.Metrics.BBR == nil || stored.Metrics.BBR.CongestionControl != "bbr" {
					t.Fatalf("compact WireMetrics lost bbr in DB: %+v", stored.Metrics.BBR)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("query agent_live_state: %v", err)
			}
			t.Fatal("compact WireMetrics changes did not converge in DB")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func signedAgentBinaryRequestStatus(t *testing.T, ctx context.Context, base, agentID string, priv ed25519.PrivateKey) int {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/agent/v1/binary", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := authn.SignRequest(req, nil, agentID, priv, time.Now().UTC()); err != nil {
		t.Fatalf("SignRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do GET /agent/v1/binary: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestAgentHTTPAuthPostCommitInvalidationConcurrentReadAndRollbackWithPostgreSQL(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, closeDatabase, fixtureUsers, err := configScopeFixtureDatabase(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = closeDatabase(cleanupCtx)
	})
	db, err := store.OpenWithConfigKey(ctx, databaseURL, true, strings.Repeat("s", 32))
	if err != nil {
		t.Fatalf("open isolated PostgreSQL: %v", err)
	}
	defer db.Close()

	server := New(db, configScopeFixtureConfig())
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	var alice core.User
	if fixtureUsers != nil {
		alice = fixtureUsers["alice"]
	} else {
		admin := configScopeAPIClient{t: t, handler: server.Handler(), token: configScopeFixtureAdminToken}
		admin.call("POST", "/users", configScopeFixtureUser("alice"), http.StatusCreated, &alice)
	}
	aliceScope := store.WithConfigScope(ctx, alice.ID, false)
	token, err := db.CreateEnrollmentToken(aliceScope, core.EnrollmentTokenRequest{Name: "auth-race-agent", TTLMinutes: 5, Reusable: true})
	if err != nil {
		t.Fatalf("CreateEnrollmentToken: %v", err)
	}
	pub1, priv1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub2, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := db.EnrollAgent(ctx, core.EnrollRequest{
		Name: "auth-race-agent", OS: "linux", Arch: "amd64", PublicKey: authn.EncodePublicKey(pub1),
	}, token.Token)
	if err != nil {
		t.Fatalf("EnrollAgent pub1: %v", err)
	}

	// Warm pubKeyCache via authenticated HTTP request.
	if status := signedAgentBinaryRequestStatus(t, ctx, httpServer.URL, enrolled.ID, priv1); status == http.StatusUnauthorized {
		t.Fatalf("initial priv1 HTTP auth = 401; want accepted")
	}

	// Uncommitted transaction rollback must keep pub1 valid and pub2 rejected.
	pgConn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer pgConn.Close(ctx)
	tx, err := pgConn.Begin(ctx)
	if err != nil {
		t.Fatalf("pgx Begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET public_key=$2 WHERE id=$1`, enrolled.ID, []byte(pub2)); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if status := signedAgentBinaryRequestStatus(t, ctx, httpServer.URL, enrolled.ID, priv2); status != http.StatusUnauthorized {
		_ = tx.Rollback(ctx)
		t.Fatalf("priv2 accepted before commit: status=%d", status)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("tx.Rollback: %v", err)
	}
	if status := signedAgentBinaryRequestStatus(t, ctx, httpServer.URL, enrolled.ID, priv1); status == http.StatusUnauthorized {
		t.Fatalf("priv1 rejected after rollback: status=401")
	}

	// Concurrent slow AgentPublicKey in HTTP middleware must NOT refill pub1 after reinstall commit.
	db.InvalidateAgentPublicKeyCacheForTesting(enrolled.ID)
	_ = token
	// Use a one-shot DB query stall during first priv1 request while EnrollAgent commits pub2.
	reinstalledCh := make(chan struct{})
	releaseQuery := make(chan struct{})
	db.SetAgentPublicKeyQueryHookForTesting(func(_ context.Context, targetID string) {
		if targetID != enrolled.ID {
			return
		}
		select {
		case reinstalledCh <- struct{}{}:
		default:
		}
		<-releaseQuery
	})
	firstStatus := make(chan int, 1)
	go func() {
		firstStatus <- signedAgentBinaryRequestStatus(t, ctx, httpServer.URL, enrolled.ID, priv1)
	}()
	select {
	case <-reinstalledCh:
	case <-time.After(5 * time.Second):
		close(releaseQuery)
		t.Fatal("timed out waiting for HTTP auth AgentPublicKey query hook")
	}
	if _, err := db.EnrollAgent(ctx, core.EnrollRequest{
		Name: "auth-race-agent", OS: "linux", Arch: "amd64", PublicKey: authn.EncodePublicKey(pub2),
	}, token.Token); err != nil {
		close(releaseQuery)
		t.Fatalf("EnrollAgent reinstall pub2: %v", err)
	}
	close(releaseQuery)
	db.SetAgentPublicKeyQueryHookForTesting(nil)
	<-firstStatus

	if status := signedAgentBinaryRequestStatus(t, ctx, httpServer.URL, enrolled.ID, priv1); status != http.StatusUnauthorized {
		t.Fatalf("old priv1 accepted after reinstall commit + concurrent query: status=%d, want 401", status)
	}
	if status := signedAgentBinaryRequestStatus(t, ctx, httpServer.URL, enrolled.ID, priv2); status == http.StatusUnauthorized {
		t.Fatalf("new priv2 rejected after reinstall commit: status=401")
	}

	// DeleteAgent invalidates HTTP auth immediately even after cache warm-up.
	if err := db.DeleteAgent(aliceScope, enrolled.ID); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if status := signedAgentBinaryRequestStatus(t, ctx, httpServer.URL, enrolled.ID, priv2); status != http.StatusUnauthorized {
		t.Fatalf("priv2 accepted after DeleteAgent: status=%d, want 401", status)
	}
}

func TestWSSTelemetryConvergenceAcrossRateDropAndCompactResyncWithPostgreSQL(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, closeDatabase, fixtureUsers, err := configScopeFixtureDatabase(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = closeDatabase(cleanupCtx)
	})
	db, err := store.OpenWithConfigKey(ctx, databaseURL, true, strings.Repeat("s", 32))
	if err != nil {
		t.Fatalf("open isolated PostgreSQL: %v", err)
	}
	defer db.Close()

	server := New(db, configScopeFixtureConfig())
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	var alice core.User
	if fixtureUsers != nil {
		alice = fixtureUsers["alice"]
	} else {
		admin := configScopeAPIClient{t: t, handler: server.Handler(), token: configScopeFixtureAdminToken}
		admin.call("POST", "/users", configScopeFixtureUser("alice"), http.StatusCreated, &alice)
	}
	aliceScope := store.WithConfigScope(ctx, alice.ID, false)
	token, err := db.CreateEnrollmentToken(aliceScope, core.EnrollmentTokenRequest{Name: "wss-resync-agent", TTLMinutes: 5, MaxUses: 1})
	if err != nil {
		t.Fatalf("CreateEnrollmentToken: %v", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	features := []string{core.AgentFeaturePortTraffic, core.AgentFeatureCompactTelemetry}
	enrolled, err := db.EnrollAgent(ctx, core.EnrollRequest{
		Name: "wss-resync-agent", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineXray}, Features: features, PublicKey: authn.EncodePublicKey(pub),
	}, token.Token)
	if err != nil {
		t.Fatalf("EnrollAgent: %v", err)
	}
	t0 := time.Now().UTC().Truncate(time.Second)
	pStart, pEnd, err := core.TrafficPeriodAt(t0, core.TrafficCycleMonthly, t0)
	if err != nil {
		t.Fatalf("TrafficPeriodAt: %v", err)
	}
	policy, err := db.CreatePortTrafficPolicy(aliceScope, core.PortTrafficPolicyRequest{
		AgentID: enrolled.ID, Name: "wss-resync-port", Engine: core.EngineXray, Port: 443,
		Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: pStart, LimitBytes: 10000,
	})
	if err != nil {
		t.Fatalf("CreatePortTrafficPolicy: %v", err)
	}
	savedCfg, err := db.SaveAgentConfig(aliceScope, core.Config{
		AgentID: enrolled.ID, Engine: core.EngineXray, Name: "wss-resync-listener",
		Content: `{"inbounds":[{"tag":"wss-resync","port":443,"protocol":"vless"}],"outbounds":[{"protocol":"freedom"}]}`,
	}, 0)
	if err != nil {
		t.Fatalf("SaveAgentConfig: %v", err)
	}
	pgConn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer pgConn.Close(ctx)
	if _, err := pgConn.Exec(ctx, `
		INSERT INTO agent_engine_ownership (agent_id,engine,owner_id,config_id,config_version,running,traffic_config_confirmed,updated_at)
		VALUES ($1,'xray',$2,$3,$4,true,true,now())`,
		enrolled.ID, alice.ID, savedCfg.ID, savedCfg.Version); err != nil {
		t.Fatalf("mark config applied: %v", err)
	}

	conn := dialPublicIPProbeWSS(t, ctx, httpServer.URL, enrolled.ID, priv)
	defer conn.Close(websocket.StatusNormalClosure, "test complete")
	frames := startWSSFrameReader(ctx, conn)
	hello := readWSSFrameType(t, frames, core.WireHello, 3*time.Second)
	if !hello.CompactTelemetry {
		t.Fatalf("expected WireHello.CompactTelemetry=true, got %+v", hello)
	}

	clock1 := &core.ClockSyncStatus{Status: "synchronized", CheckedAt: t0}
	bbr1 := &core.SystemBBRStatus{CollectedAt: t0, Available: true, CongestionControl: "bbr", DefaultQdisc: "fq"}
	iface1 := []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"10.0.0.1"}}}
	const testEpoch = "11111111111111111111111111111111"
	usage1 := []core.PortTrafficUsage{{
		PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration, CounterEpoch: testEpoch,
		CollectedAt: t0, PeriodStart: pStart, PeriodEnd: pEnd, EnforcementAvailable: true,
		ReceivedBytes: 100, UsedBytes: 100, LifetimeReceivedBytes: 100,
	}}
	if err := wsjson.Write(ctx, conn, core.WireMessage{
		Type: core.WireHeartbeat,
		Heartbeat: &core.HeartbeatRequest{
			Version: "resync-v1", Features: features,
			Metrics: &core.HostMetrics{
				CollectedAt: t0, CPUAvailable: true, CPUPercent: 10,
				MemoryAvailable: true, MemoryUsedBytes: 1024, MemoryTotalBytes: 8192,
				NetworkInterfaces: iface1, BBR: bbr1, ClockSync: clock1,
			},
			TrafficUsage: usage1,
		},
	}); err != nil {
		t.Fatalf("write initial heartbeat: %v", err)
	}

	// Exhaust telemetryBucket burst (10.0), then send a diff frame carrying 10.0.0.2/cubic/rx=250 that gets dropped!
	for i := 0; i < int(wsInboundTelemetryBurst); i++ {
		if err := wsjson.Write(ctx, conn, core.WireMessage{
			Type: core.WireMetrics,
			Metrics: &core.HostMetrics{
				CollectedAt:  t0.Add(time.Duration(i+1) * time.Millisecond),
				CPUAvailable: true, CPUPercent: float64(20 + i),
				MemoryAvailable: true, MemoryUsedBytes: 1024, MemoryTotalBytes: 8192,
			},
		}); err != nil {
			t.Fatalf("drain burst frame %d: %v", i, err)
		}
	}
	iface2 := []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"10.0.0.2"}}}
	bbr2 := &core.SystemBBRStatus{CollectedAt: t0.Add(2 * time.Second), Available: true, CongestionControl: "cubic", DefaultQdisc: "fq_codel"}
	clock2 := &core.ClockSyncStatus{Status: "unsynchronized", CheckedAt: t0.Add(2 * time.Second)}
	usage2 := []core.PortTrafficUsage{{
		PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration, CounterEpoch: testEpoch,
		CollectedAt: t0.Add(2 * time.Second), PeriodStart: pStart, PeriodEnd: pEnd, EnforcementAvailable: true,
		ReceivedBytes: 250, UsedBytes: 250, LifetimeReceivedBytes: 250,
	}}
	// This frame is dropped by telemetryBucket and must trigger WireTelemetryResync.
	if err := wsjson.Write(ctx, conn, core.WireMessage{
		Type: core.WireMetrics,
		Metrics: &core.HostMetrics{
			CollectedAt:  t0.Add(2 * time.Second),
			CPUAvailable: true, CPUPercent: 45,
			MemoryAvailable: true, MemoryUsedBytes: 2048, MemoryTotalBytes: 8192,
			NetworkInterfaces: iface2, BBR: bbr2, ClockSync: clock2,
		},
		TrafficUsage: usage2,
	}); err != nil {
		t.Fatalf("write rate-dropped diff frame: %v", err)
	}
	_ = readWSSFrameType(t, frames, core.WireTelemetryResync, 3*time.Second)

	// Wait for token refill (~450ms for >1 token), then send stale compact frame (nil fields)
	// followed by TelemetryFullSync frame — verify server converges completely to 10.0.0.2/cubic/rx=250.
	time.Sleep(500 * time.Millisecond)
	if err := wsjson.Write(ctx, conn, core.WireMessage{
		Type: core.WireMetrics,
		Metrics: &core.HostMetrics{
			CollectedAt:  t0.Add(3 * time.Second),
			CPUAvailable: true, CPUPercent: 46,
			MemoryAvailable: true, MemoryUsedBytes: 2048, MemoryTotalBytes: 8192,
		},
	}); err != nil {
		t.Fatalf("write stale in-flight compact frame: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := wsjson.Write(ctx, conn, core.WireMessage{
		Type: core.WireMetrics, TelemetryFullSync: true,
		Metrics: &core.HostMetrics{
			CollectedAt:  t0.Add(4 * time.Second),
			CPUAvailable: true, CPUPercent: 50,
			MemoryAvailable: true, MemoryUsedBytes: 4096, MemoryTotalBytes: 8192,
			NetworkInterfaces: iface2, BBR: bbr2, ClockSync: clock2,
		},
		TrafficUsage: usage2,
	}); err != nil {
		t.Fatalf("write TelemetryFullSync frame: %v", err)
	}

	// Interleave a heartbeat carrying newer interface/traffic (10.0.0.3, rx=320) followed by compact metrics.
	time.Sleep(500 * time.Millisecond)
	iface3 := []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"10.0.0.3"}}}
	usage3 := []core.PortTrafficUsage{{
		PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration, CounterEpoch: testEpoch,
		CollectedAt: t0.Add(5 * time.Second), PeriodStart: pStart, PeriodEnd: pEnd, EnforcementAvailable: true,
		ReceivedBytes: 320, UsedBytes: 320, LifetimeReceivedBytes: 320,
	}}
	if err := wsjson.Write(ctx, conn, core.WireMessage{
		Type: core.WireHeartbeat,
		Heartbeat: &core.HeartbeatRequest{
			Version: "resync-v1", Features: features,
			Metrics: &core.HostMetrics{
				CollectedAt: t0.Add(5 * time.Second), CPUAvailable: true, CPUPercent: 55,
				MemoryAvailable: true, MemoryUsedBytes: 4096, MemoryTotalBytes: 8192,
				NetworkInterfaces: iface3, BBR: bbr2, ClockSync: clock2,
			},
			TrafficUsage: usage3,
		},
	}); err != nil {
		t.Fatalf("write interleaved heartbeat: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := wsjson.Write(ctx, conn, core.WireMessage{
		Type: core.WireMetrics,
		Metrics: &core.HostMetrics{
			CollectedAt: t0.Add(6 * time.Second), CPUAvailable: true, CPUPercent: 60,
			MemoryAvailable: true, MemoryUsedBytes: 4096, MemoryTotalBytes: 8192,
		},
	}); err != nil {
		t.Fatalf("write trailing compact metrics: %v", err)
	}

	deadline := time.Now().Add(4 * time.Second)
	for {
		stored, getErr := db.GetAgent(aliceScope, enrolled.ID)
		policies, polErr := db.AgentPortTrafficPolicies(aliceScope, enrolled.ID)
		if getErr == nil && polErr == nil && len(policies) == 1 &&
			stored.Metrics.CPUPercent == 60 &&
			len(stored.Metrics.NetworkInterfaces) == 1 && stored.Metrics.NetworkInterfaces[0].Addresses[0] == "10.0.0.3" &&
			stored.Metrics.BBR != nil && stored.Metrics.BBR.CongestionControl == "cubic" &&
			stored.Metrics.ClockSync != nil && stored.Metrics.ClockSync.Status == "unsynchronized" &&
			policies[0].ReceivedBytes == 320 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("DB did not converge: agent=%+v policies=%+v (errs=%v/%v)", stored.Metrics, policies, getErr, polErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
