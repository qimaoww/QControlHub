package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestNormalizeSubStoreEndpoint(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"http://substore:3001/qch-secret",
		"https://substore.example.com/backend/qch_secret-1",
	} {
		if _, err := normalizeSubStoreEndpoint(input); err != nil {
			t.Errorf("normalize valid endpoint %q: %v", input, err)
		}
	}
	for _, input := range []string{
		"", "http://substore:3001", "https://sub.store/secret", "ftp://substore/secret",
		"https://user:password@substore.example/secret", "https://substore.example/secret?token=x",
		"https://substore.example/%73ecret", "https://substore.example/bad token",
	} {
		if _, err := normalizeSubStoreEndpoint(input); err == nil {
			t.Errorf("normalize invalid endpoint %q unexpectedly succeeded", input)
		}
	}
	if hint := subStoreEndpointHint("https://substore.example/backend/qch-secret"); hint != "https://substore.example/••••••/••••••" {
		t.Fatalf("endpoint hint = %q", hint)
	}
}

func TestRenameSubStoreNode(t *testing.T) {
	t.Parallel()
	got, err := renameSubStoreNode("vless://example-id@node.example:443?type=tcp#old-name", "东京 A / 专线")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "#%E4%B8%9C%E4%BA%AC%20A%20/%20%E4%B8%93%E7%BA%BF") || strings.Contains(got, "old-name") {
		t.Fatalf("renamed URI = %q", got)
	}
	for _, name := range []string{"", "bad\nname"} {
		if _, err := renameSubStoreNode("vless://example-id@node.example:443", name); err == nil {
			t.Errorf("invalid node name %q unexpectedly succeeded", name)
		}
	}
}

func TestSubStoreSubscriptionCreateUpdateAndOwnership(t *testing.T) {
	t.Parallel()
	var stored map[string]any
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/qch-secret/api/utils/env":
			writeSubStoreTestEnvelope(t, w, http.StatusOK, map[string]any{"version": "test"}, "")
		case request.Method == http.MethodGet && request.URL.Path == "/qch-secret/api/sub/QControlHub":
			if stored == nil {
				writeSubStoreTestEnvelope(t, w, http.StatusNotFound, nil, "not found")
				return
			}
			writeSubStoreTestEnvelope(t, w, http.StatusOK, stored, "")
		case request.Method == http.MethodPost && request.URL.Path == "/qch-secret/api/subs":
			stored = decodeSubStoreTestPayload(t, request.Body)
			writeSubStoreTestEnvelope(t, w, http.StatusOK, stored, "")
		case request.Method == http.MethodPatch && request.URL.Path == "/qch-secret/api/sub/QControlHub":
			stored = decodeSubStoreTestPayload(t, request.Body)
			writeSubStoreTestEnvelope(t, w, http.StatusOK, stored, "")
		default:
			t.Errorf("unexpected Sub-Store request %s %s", request.Method, request.URL.Path)
			writeSubStoreTestEnvelope(t, w, http.StatusNotFound, nil, "unexpected")
		}
	}))
	defer remote.Close()

	server := &Server{subStoreHTTP: remote.Client()}
	settings := core.SubStoreSyncSettings{
		EndpointURL: remote.URL + "/qch-secret", SubscriptionName: "QControlHub", IntegrationID: "ssi_owner",
	}
	created, err := server.upsertSubStoreSubscription(context.Background(), settings, "vless://one#One")
	if err != nil || !created {
		t.Fatalf("create subscription = %t, %v", created, err)
	}
	if stored["source"] != "local" || stored["content"] != "vless://one#One" || stored["qcontrolhub_integration_id"] != "ssi_owner" {
		t.Fatalf("created payload = %#v", stored)
	}
	created, err = server.upsertSubStoreSubscription(context.Background(), settings, "vless://two#Two")
	if err != nil || created || stored["content"] != "vless://two#Two" {
		t.Fatalf("update subscription = %t, %#v, %v", created, stored, err)
	}
	stored["qcontrolhub_integration_id"] = "another-control-plane"
	if _, err := server.upsertSubStoreSubscription(context.Background(), settings, "vless://three#Three"); err == nil || !strings.Contains(err.Error(), "不是由当前 QControlHub") {
		t.Fatalf("foreign subscription ownership error = %v", err)
	}
}

func TestSubStoreRequestDoesNotLeakEndpointCredential(t *testing.T) {
	t.Parallel()
	server := &Server{subStoreHTTP: &http.Client{Transport: subStoreRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	})}}
	_, err := server.subStoreRequest(context.Background(), "https://substore.example/super-secret-path", http.MethodGet, "/api/utils/env", nil, nil)
	if err == nil || strings.Contains(err.Error(), "super-secret-path") || strings.Contains(err.Error(), "substore.example") {
		t.Fatalf("sanitized connection error = %v", err)
	}
}

type subStoreRoundTripFunc func(*http.Request) (*http.Response, error)

func (function subStoreRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func decodeSubStoreTestPayload(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func writeSubStoreTestEnvelope(t *testing.T, writer http.ResponseWriter, status int, data any, message string) {
	t.Helper()
	writer.WriteHeader(status)
	payload := map[string]any{"status": "success", "data": data}
	if status < 200 || status >= 300 {
		payload["status"] = "failed"
		payload["error"] = map[string]string{"message": message}
	}
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		t.Fatal(err)
	}
}
