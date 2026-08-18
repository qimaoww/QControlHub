package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSendSignsPayloadWithHMACWhenSecretConfigured(t *testing.T) {
	t.Parallel()
	const secret = "test-webhook-secret"
	var receivedSignature string
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		receivedSignature = request.Header.Get("X-QControlHub-Signature")
		receivedBody, _ = io.ReadAll(request.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := New(secret, slog.New(slog.NewTextHandler(io.Discard, nil)))
	event := TaskFailedEvent(core.Task{
		ID: "tsk_0123456789abcdef", AgentID: "agt_0123456789abcdef",
		Action: core.ActionDeploy, Engine: core.EngineMihomo,
	}, "edge-01", "validation failed")
	if err := client.Send(context.Background(), server.URL, event); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(receivedBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if receivedSignature != want {
		t.Fatalf("signature = %q, want %q", receivedSignature, want)
	}
	var decoded Event
	if err := json.Unmarshal(receivedBody, &decoded); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if decoded.Type != TypeTaskFailed || decoded.TaskID != event.TaskID || decoded.Engine != "mihomo" || decoded.Agent != "edge-01" || !strings.Contains(decoded.Message, "失败") {
		t.Fatalf("decoded event = %+v", decoded)
	}
}

func TestSendSkipsSignatureWithoutSecret(t *testing.T) {
	t.Parallel()
	var receivedSignature string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		receivedSignature = request.Header.Get("X-QControlHub-Signature")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := New("", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := client.Send(context.Background(), server.URL, AgentOfflineEvent(core.Agent{ID: "agt_0123456789abcdef", Name: "edge-01"})); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if receivedSignature != "" {
		t.Fatalf("unexpected signature header %q without a secret", receivedSignature)
	}
}

func TestSendEmptyURLIsNoOp(t *testing.T) {
	t.Parallel()
	client := New("secret", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := client.Send(context.Background(), "", AgentOfflineEvent(core.Agent{})); err != nil {
		t.Fatalf("Send() with empty URL error = %v", err)
	}
}

func TestSendReportsNonSuccessResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer server.Close()

	client := New("", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := client.Send(context.Background(), server.URL, AgentOfflineEvent(core.Agent{ID: "agt_0123456789abcdef", Name: "edge-01"}))
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("Send() error = %v, want 502", err)
	}
}

func TestEventsCarryExpectedFields(t *testing.T) {
	t.Parallel()
	agent := core.Agent{ID: "agt_0123456789abcdef", Name: "edge-01"}
	offline := AgentOfflineEvent(agent)
	if offline.Type != TypeAgentOffline || offline.AgentID != agent.ID || offline.Message == "" {
		t.Fatalf("offline event = %+v", offline)
	}
	online := AgentOnlineEvent(agent)
	if online.Type != TypeAgentOnline || online.AgentID != agent.ID || online.Message == "" {
		t.Fatalf("online event = %+v", online)
	}
	task := core.Task{
		ID: "tsk_0123456789abcdef", AgentID: agent.ID, Action: core.ActionInstall,
		Engine: core.EngineShadowsocksRust,
	}
	failed := TaskFailedEvent(task, agent.Name, strings.Repeat("x", 5000))
	if failed.Type != TypeTaskFailed || failed.Action != "install" || failed.Engine != "ss-rust" || len(failed.Error) > 2100 {
		t.Fatalf("failed event = %+v (error truncated to %d bytes)", failed, len(failed.Error))
	}
}

func TestSendHonorsContextCancellation(t *testing.T) {
	t.Parallel()
	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-blocked
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	defer close(blocked)

	client := New("", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := client.Send(ctx, server.URL, AgentOfflineEvent(core.Agent{ID: "agt_0123456789abcdef"})); err == nil {
		t.Fatal("Send() succeeded despite canceled context")
	}
}
