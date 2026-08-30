package komari

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetNodeReadsEnvelopeAndAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":[{"uuid":"node-1","name":"edge","billing_cycle":30,"traffic_limit":1073741824,"traffic_limit_type":"sum","effective_traffic_limit":2147483648,"effective_traffic_type":"sum","traffic_reset_day":1}]}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	node, err := client.GetNode(context.Background(), "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if node.BillingCycle != 30 || node.TrafficLimit != 1073741824 || node.EffectiveTrafficLimit != 2147483648 || !node.EffectiveTrafficLimitSet {
		t.Fatalf("unexpected node: %+v", node)
	}
}

func TestGetNodeAcceptsDirectArrayAndMissingNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"uuid":"node-1","billing_cycle":31,"traffic_limit":0}]`))
	}))
	defer server.Close()
	client, err := New(server.URL, "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if node, err := client.GetNode(context.Background(), "node-1"); err != nil || node.EffectiveTrafficLimitSet {
		t.Fatalf("missing effective field presence = %+v, %v", node, err)
	}
	if _, err := client.GetNode(context.Background(), "missing"); err == nil {
		t.Fatal("missing node unexpectedly succeeded")
	}
}

func TestNewRejectsUnsafeURL(t *testing.T) {
	for _, value := range []string{"/relative", "https://user@example.com", "https://example.com?x=1"} {
		if _, err := New(value, "", nil); err == nil {
			t.Fatalf("New(%q) unexpectedly succeeded", value)
		}
	}
	if _, err := New("https://example.com", "bad\nkey", nil); err == nil {
		t.Fatal("New accepted an API key containing a newline")
	}
	client, err := New("", "", nil)
	if err != nil || client != nil {
		t.Fatalf("empty URL = %#v, %v", client, err)
	}
}
