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
	for _, rawURI := range []string{
		"vless://example-id@node.example:443?type=tcp#old-name",
		"trojan://secret@node.example:443?security=tls#old-name",
		"ss://encoded-identity@node.example:1443#old-name",
	} {
		got, err := renameSubStoreNode(rawURI, "东京 A · 专线")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(got, "#东京 A · 专线") || strings.Contains(got, "old-name") || strings.Contains(got, "%E4%B8%9C") {
			t.Fatalf("renamed URI = %q", got)
		}
	}
	for _, name := range []string{"", "bad\nname"} {
		if _, err := renameSubStoreNode("vless://example-id@node.example:443", name); err == nil {
			t.Errorf("invalid node name %q unexpectedly succeeded", name)
		}
	}
}

func TestSubStoreNodesForAddressMode(t *testing.T) {
	t.Parallel()
	profile := subStoreSyncProfile{
		URI: "vless://auto@node.example:443#node",
		Addresses: []subStoreSyncAddress{
			{Family: core.SubStoreAddressModeIPv4, Address: "198.51.100.10", URI: "vless://v4@198.51.100.10:443#node"},
			{Family: core.SubStoreAddressModeIPv6, Address: "2001:db8::10", URI: "vless://v6@[2001:db8::10]:443#node"},
		},
	}
	selection := core.SubStoreSyncSelection{CustomName: "Tokyo"}
	nodes, err := subStoreNodesForSelection(profile, selection)
	if err != nil || len(nodes) != 1 || nodes[0] != "vless://auto@node.example:443#Tokyo" {
		t.Fatalf("automatic Sub-Store node = %v, %v", nodes, err)
	}
	selection.AddressMode = core.SubStoreAddressModeBoth
	nodes, err = subStoreNodesForSelection(profile, selection)
	if err != nil || len(nodes) != 2 || !strings.HasSuffix(nodes[0], "#Tokyo") || !strings.HasSuffix(nodes[1], "#Tokyo v6") {
		t.Fatalf("dual-stack Sub-Store nodes = %v, %v", nodes, err)
	}
	selection.AddressMode = core.SubStoreAddressModeIPv6
	nodes, err = subStoreNodesForSelection(profile, selection)
	if err != nil || len(nodes) != 1 || !strings.HasSuffix(nodes[0], "#Tokyo v6") {
		t.Fatalf("IPv6 Sub-Store node = %v, %v", nodes, err)
	}
	if got := subStoreIPv6NodeName(strings.Repeat("x", 100)); len([]rune(got)) != 100 || !strings.HasSuffix(got, " v6") {
		t.Fatalf("IPv6 name truncation = %q", got)
	}
}

func TestSubStoreRemoteTargetHelpers(t *testing.T) {
	t.Parallel()
	if count := subStoreContentNodeCount("vless://one#One\r\n\r\nss://two#Two\n"); count != 2 {
		t.Fatalf("remote group node count = %d", count)
	}
	if _, err := validateSubStoreImportName("Existing group"); err != nil {
		t.Fatalf("valid remote group name: %v", err)
	}
	for _, name := range []string{"", "bad/group", "bad?group"} {
		if _, err := validateSubStoreImportName(name); err == nil {
			t.Fatalf("invalid remote group name %q accepted", name)
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
		case request.Method == http.MethodGet && request.URL.Path == "/qch-secret/api/subs":
			if stored == nil {
				writeSubStoreTestEnvelope(t, w, http.StatusOK, []any{}, "")
				return
			}
			writeSubStoreTestEnvelope(t, w, http.StatusOK, []any{stored}, "")
		case request.Method == http.MethodPost && request.URL.Path == "/qch-secret/api/subs":
			stored = decodeSubStoreTestPayload(t, request.Body)
			writeSubStoreTestEnvelope(t, w, http.StatusOK, stored, "")
		case request.Method == http.MethodPatch && (request.URL.Path == "/qch-secret/api/sub/QControlHub" || request.URL.Path == "/qch-secret/api/sub/QControlHub Renamed" || request.URL.Path == "/qch-secret/api/sub/Imported Group"):
			stored = decodeSubStoreTestPayload(t, request.Body)
			writeSubStoreTestEnvelope(t, w, http.StatusOK, stored, "")
		default:
			t.Errorf("unexpected Sub-Store request %s %s", request.Method, request.URL.Path)
			writeSubStoreTestEnvelope(t, w, http.StatusNotFound, nil, "unexpected")
		}
	}))
	defer remote.Close()

	server := &Server{subStoreHTTP: remote.Client()}
	settings := core.SubStoreSyncSettings{EndpointURL: remote.URL + "/qch-secret"}
	target := core.SubStoreSyncTarget{
		SubscriptionName: "QControlHub",
		IntegrationID:    "ssi_owner",
		SyncMode:         core.SubStoreSyncModeIncremental,
	}
	created, err := server.upsertSubStoreSubscription(context.Background(), settings, target, "vless://one#One")
	if err != nil || !created {
		t.Fatalf("create subscription = %t, %v", created, err)
	}
	if stored["source"] != "local" || stored["content"] != "vless://one#One" || stored["qcontrolhub_integration_id"] != "ssi_owner" {
		t.Fatalf("created payload = %#v", stored)
	}
	delete(stored, "qcontrolhub_managed_nodes")
	created, err = server.upsertSubStoreSubscription(context.Background(), settings, target, "vless://two#Two")
	if err != nil || created || stored["content"] != "vless://two#Two" {
		t.Fatalf("upgrade legacy owned subscription = %t, %#v, %v", created, stored, err)
	}
	stored["custom-option"] = "preserved"
	renamed, err := server.renameSubStoreSubscription(context.Background(), settings, target, "QControlHub Renamed")
	if err != nil || !renamed || stored["name"] != "QControlHub Renamed" || stored["content"] != "vless://two#Two" || stored["custom-option"] != "preserved" {
		t.Fatalf("rename subscription in place = %t, %#v, %v", renamed, stored, err)
	}
	target.SubscriptionName = "QControlHub Renamed"
	created, err = server.upsertSubStoreSubscription(context.Background(), settings, target, "vless://renamed#Renamed")
	if err != nil || created || stored["name"] != "QControlHub Renamed" || stored["content"] != "vless://renamed#Renamed" {
		t.Fatalf("rename subscription = %t, %#v, %v", created, stored, err)
	}
	target.SubscriptionName = "Imported Group"
	stored = map[string]any{
		"name":    "Imported Group",
		"content": "vless://manual@old.example:443#Manual\nvless://old@old.example:443#Managed",
	}
	created, err = server.upsertSubStoreSubscription(context.Background(), settings, target, "vless://new@new.example:443#Managed\nvless://added@new.example:443#Added")
	if err != nil || created || stored["content"] != "vless://manual@old.example:443#Manual\nvless://new@new.example:443#Managed\nvless://added@new.example:443#Added" || stored["qcontrolhub_integration_id"] != "ssi_owner" {
		t.Fatalf("claim and merge unowned subscription = %t, %#v, %v", created, stored, err)
	}
	target.SyncMode = core.SubStoreSyncModeManaged
	created, err = server.upsertSubStoreSubscription(context.Background(), settings, target, "vless://only@managed.example:443#Only")
	if err != nil || created || stored["content"] != "vless://only@managed.example:443#Only" {
		t.Fatalf("fully managed subscription = %t, %#v, %v", created, stored, err)
	}
	target.SubscriptionName = "QControlHub Renamed"
	stored = map[string]any{
		"name": "QControlHub Renamed", "content": "vless://foreign#Foreign",
		"qcontrolhub_integration_id": "another-control-plane",
	}
	if _, err := server.upsertSubStoreSubscription(context.Background(), settings, target, "vless://three#Three"); err == nil || !strings.Contains(err.Error(), "其他 QControlHub") {
		t.Fatalf("foreign subscription ownership error = %v", err)
	}
}

func TestMergeSubStoreContentByNodeName(t *testing.T) {
	t.Parallel()
	existing := strings.Join([]string{
		"vless://manual@old.example:443#Manual",
		"vless://old@old.example:443#Managed",
		"vless://removed@old.example:443#Removed",
	}, "\n")
	desired := strings.Join([]string{
		"vless://new@new.example:443#Managed",
		"vless://added@new.example:443#Added",
	}, "\n")
	merged, err := mergeSubStoreContentByName(existing, desired, []string{"Managed", "Removed"})
	want := strings.Join([]string{
		"vless://manual@old.example:443#Manual",
		"vless://new@new.example:443#Managed",
		"vless://added@new.example:443#Added",
	}, "\n")
	if err != nil || merged != want {
		t.Fatalf("incremental merge = %q, %v; want %q", merged, err, want)
	}
	if _, err := mergeSubStoreContentByName("", "vless://one@node:443#Duplicate\nvless://two@node:443#Duplicate", nil); err == nil || !strings.Contains(err.Error(), "重名") {
		t.Fatalf("duplicate desired names error = %v", err)
	}
	merged, err = mergeSubStoreContentByName(existing, "", []string{"Managed", "Removed"})
	if err != nil || merged != "vless://manual@old.example:443#Manual" {
		t.Fatalf("remove all managed nodes = %q, %v", merged, err)
	}
}

func TestUpsertSubStoreSubscriptionRejectsInvalidMode(t *testing.T) {
	t.Parallel()
	server := &Server{}
	_, err := server.upsertSubStoreSubscription(context.Background(), core.SubStoreSyncSettings{}, core.SubStoreSyncTarget{SyncMode: "unknown"}, "vless://one@node:443#One")
	if err == nil || !strings.Contains(err.Error(), "模式无效") {
		t.Fatalf("invalid sync mode error = %v", err)
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
