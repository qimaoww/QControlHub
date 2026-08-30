package komari

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetNodeReadsEnvelopeAndAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/nodes":
			_, _ = w.Write([]byte(`{"status":"success","data":[{"uuid":"node-1","name":"edge","billing_cycle":30,"traffic_limit":1073741824,"traffic_limit_type":"sum","effective_traffic_limit":2147483648,"effective_traffic_type":"sum","traffic_reset_day":27}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/rpc2":
			var request struct {
				Method string `json:"method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Method != "common:getNodesLatestStatus" {
				t.Fatalf("RPC method = %q", request.Method)
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"client":"node-1","net_total_up":268435456,"net_total_down":134217728}}`))
		default:
			http.NotFound(w, r)
		}
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
	if node.BillingCycle != 30 || node.TrafficLimit != 1073741824 || node.EffectiveTrafficLimit != 2147483648 || !node.EffectiveTrafficLimitSet || !node.EffectiveTrafficTypeSet || node.TrafficResetDay != 27 {
		t.Fatalf("unexpected node: %+v", node)
	}
	if !node.TrafficUsedSet || node.TrafficUsed != 402653184 || node.TrafficUp != 268435456 || node.TrafficDown != 134217728 {
		t.Fatalf("unexpected traffic usage: %+v", node)
	}
}

func TestGetNodeAcceptsDirectArrayAndMissingNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/rpc2" {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"not found"}}`))
			return
		}
		_, _ = w.Write([]byte(`[{"uuid":"node-1","billing_cycle":31,"traffic_limit":0}]`))
	}))
	defer server.Close()
	client, err := New(server.URL, "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if node, err := client.GetNode(context.Background(), "node-1"); err != nil || node.EffectiveTrafficLimitSet || node.TrafficUsedSet {
		t.Fatalf("missing effective field presence = %+v, %v", node, err)
	}
	if _, err := client.GetNode(context.Background(), "missing"); err == nil {
		t.Fatal("missing node unexpectedly succeeded")
	}
}

func TestTrafficUsedByType(t *testing.T) {
	tests := map[string]int64{
		"up":      9,
		"down":    4,
		"sum":     13,
		"min":     4,
		"max":     9,
		"unknown": 9,
	}
	for trafficType, want := range tests {
		if got := trafficUsed(trafficType, 9, 4); got != want {
			t.Errorf("trafficUsed(%q) = %d, want %d", trafficType, got, want)
		}
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
