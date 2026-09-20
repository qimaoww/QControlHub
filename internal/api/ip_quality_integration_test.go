package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestIPQualityAPIAndWebSocketLifecycle(t *testing.T) {
	url := os.Getenv("QCH_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(context.Background())
	db, err := store.Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := New(db, Config{AdminToken: "quality-admin", OperatorTokens: []string{"quality-operator"}, ReadonlyTokens: []string{"quality-reader"}})
	handler := server.Handler()
	call := func(method, path, token string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(encoded))
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	day := time.Now().UTC().Format(time.DateOnly)
	call("GET", "/ip-quality?date="+day, "", nil, 401)
	empty := call("GET", "/ip-quality?date="+day, "quality-reader", nil, 200)
	if !strings.Contains(empty.Body.String(), `"records":[]`) || !strings.Contains(empty.Body.String(), `"schedules":[]`) {
		t.Fatalf("empty history did not return arrays: %s", empty.Body.String())
	}
	for _, path := range []string{"/ip-quality", "/ip-quality?date=2026-02-30", "/ip-quality?date=" + day + "&timezone=Invalid/Zone"} {
		call("GET", path, "quality-admin", nil, 400)
	}
	credential, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "quality API"})
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "quality-node", OS: "linux", Arch: "amd64",
		PublicKey: authn.EncodePublicKey(publicKey), Capabilities: []core.Engine{core.EngineMihomo}}, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]string{"agent_id": agent.ID}
	generic := core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality}
	call("POST", "/ip-quality", "quality-reader", input, 403)
	call("POST", "/ip-quality", "quality-operator", input, 403)
	call("POST", "/tasks", "quality-operator", generic, 403)
	call("POST", "/ip-quality", "quality-admin", input, 409)
	call("PUT", "/ip-quality/schedules/"+agent.ID, "quality-admin", map[string]bool{}, 400)
	call("PUT", "/ip-quality/schedules/"+agent.ID, "quality-operator", map[string]bool{"enabled": true}, 403)
	beat := core.HeartbeatRequest{Features: []string{core.AgentFeatureIPQuality}}
	if err := db.Heartbeat(ctx, agent.ID, beat); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []core.TaskRequest{
		{AgentID: agent.ID, Action: core.ActionIPQuality, Engine: core.EngineMihomo},
		{AgentID: agent.ID, Action: core.ActionIPQuality, CoreSource: "https://example.test/evil.sh"},
		{AgentID: agent.ID, Action: core.ActionIPQuality, TCPSettings: core.TCPSettings{"net.core.default_qdisc": "fq"}},
	} {
		call("POST", "/tasks", "quality-admin", bad, 400)
	}
	var task core.Task
	if err := json.Unmarshal(call("POST", "/ip-quality", "quality-admin", input, 201).Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	call("POST", "/ip-quality", "quality-admin", input, 200)
	call("PUT", "/ip-quality/schedules/"+agent.ID, "quality-admin", map[string]bool{"enabled": true}, 200)
	call("PUT", "/ip-quality/schedules/"+agent.ID, "quality-admin", map[string]bool{"enabled": false}, 200)

	// Exercise the real signed Agent transport, not just direct store calls.
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/agent/v1/connect"
	handshake, _ := http.NewRequestWithContext(ctx, http.MethodGet, wsURL, nil)
	if err := authn.SignRequest(handshake, nil, agent.ID, privateKey, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	connection, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: handshake.Header, Subprotocols: []string{"qcontrolhub.agent.v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "quality test complete")
	var message core.WireMessage
	if err := wsjson.Read(ctx, connection, &message); err != nil || message.Type != core.WireHello {
		t.Fatalf("hello = %+v %v", message, err)
	}
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &beat}); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Read(ctx, connection, &message); err != nil || message.Type != core.WireTask || message.Task.ID != task.ID {
		t.Fatalf("task = %+v %v", message, err)
	}
	report, err := core.ParseIPQualityReports([]byte(`{"Head":{"IP":"203.0.113.1"},"Info":{},"Type":{},"Score":{"IPQS":"null"},"Factor":{},"Media":{},"Mail":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	report.ReportsText = []string{"IP质量体检报告：203.0.113.1\nIP2Location：  3|低风险\n"}
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireResult, Result: &core.TaskResultEnvelope{
		TaskID: task.ID, Result: core.TaskResultRequest{LeaseID: message.Task.LeaseID, Success: true, IPQuality: &report},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Read(ctx, connection, &message); err != nil || message.Type != core.WireResultAck || message.TaskID != task.ID {
		t.Fatalf("ack = %+v %v", message, err)
	}
	var history core.IPQualityHistory
	historyBody := call("GET", "/ip-quality?date="+day, "quality-admin", nil, 200).Body.Bytes()
	if err := json.Unmarshal(historyBody, &history); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(historyBody), "reports_text") {
		t.Fatal("history response leaked the printed report text")
	}
	if len(history.Records) != 1 || history.Records[0].Result == nil || history.Records[0].Status != core.TaskSucceeded {
		t.Fatalf("wire result was not persisted: %+v", history)
	}
	if len(history.Records[0].Archives) != 1 {
		t.Fatal("report was not archived")
	}
	archivePath := "/ip-quality/" + task.ID + "/archives/4"
	for _, suffix := range []string{"", "?download=1"} {
		response := call("GET", archivePath+suffix, "quality-admin", nil, 200)
		if !strings.HasPrefix(response.Body.String(), "<svg") || !strings.Contains(response.Body.String(), "203.0.113.1") ||
			response.Header().Get("Content-Type") != "image/svg+xml" || response.Header().Get("Cache-Control") != "no-store" ||
			!strings.Contains(response.Header().Get("Content-Security-Policy"), "sandbox") {
			t.Fatal("archive is not a panel-rendered SVG or lost browser isolation")
		}
		if (suffix == "") != strings.HasPrefix(response.Header().Get("Content-Disposition"), "inline;") {
			t.Fatal("incorrect preview/download disposition")
		}
	}
	call("GET", archivePath, "", nil, 401)
	call("GET", archivePath, "quality-reader", nil, 404)
	call("GET", "/ip-quality/"+task.ID+"/archives/6", "quality-admin", nil, 404)
	call("GET", "/ip-quality/"+task.ID+"/archives/7", "quality-admin", nil, 400)
	reader := call("GET", "/ip-quality?date="+day, "quality-reader", nil, 200)
	if strings.Contains(reader.Body.String(), "203.0.113.1") {
		t.Fatal("read-only token inherited another principal's report")
	}

	// An otherwise valid result that JSONB cannot store must be ACKed as a
	// failed task, and the same authenticated connection must remain usable.
	bad := core.IPQualityResult{Reports: []json.RawMessage{
		json.RawMessage(`{"Head":{"IP":"203.0.113.1"},"Info":{"Organization":"provider\u0000value"},"Type":{},"Score":{},"Factor":{},"Media":{},"Mail":{}}`),
	}, ReportsText: []string{"IP质量体检报告：203.0.113.1\n"}}
	badXML := core.IPQualityResult{Reports: report.Reports,
		ReportsText: []string{"IP质量体检报告：203.0.113.1\n运营商：bad\ufffevalue"}}
	for _, test := range []struct {
		report core.IPQualityResult
		status core.TaskStatus
	}{{bad, core.TaskFailed}, {badXML, core.TaskFailed}, {report, core.TaskSucceeded}} {
		if err := json.Unmarshal(call("POST", "/ip-quality", "quality-admin", input, 201).Body.Bytes(), &task); err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Read(ctx, connection, &message); err != nil || message.Type != core.WireTask ||
			message.Task == nil || message.Task.ID != task.ID {
			t.Fatalf("follow-up task = %+v %v", message, err)
		}
		if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireResult, Result: &core.TaskResultEnvelope{
			TaskID: task.ID, Result: core.TaskResultRequest{LeaseID: message.Task.LeaseID, Success: true, IPQuality: &test.report},
		}}); err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Read(ctx, connection, &message); err != nil || message.Type != core.WireResultAck || message.TaskID != task.ID {
			t.Fatalf("unsavable/follow-up result was not acknowledged: %+v %v", message, err)
		}
		var history core.IPQualityHistory
		if err := json.Unmarshal(call("GET", "/ip-quality?date="+day, "quality-admin", nil, 200).Body.Bytes(), &history); err != nil {
			t.Fatal(err)
		}
		if len(history.Records) != 1 || history.Records[0].TaskID != task.ID || history.Records[0].Status != test.status ||
			(history.Records[0].Result != nil) != (test.status == core.TaskSucceeded) {
			t.Fatalf("follow-up history: %+v", history)
		}
		if test.status == core.TaskFailed {
			call("GET", "/ip-quality/"+task.ID+"/archives/4", "quality-admin", nil, 404)
		} else if err := validateRenderedSVG(call("GET", "/ip-quality/"+task.ID+"/archives/4", "quality-admin", nil, 200).Body.Bytes()); err != nil {
			t.Fatalf("successful task served invalid SVG: %v", err)
		}
	}
}
