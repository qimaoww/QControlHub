package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestAgentTimeZoneReconcilesHeartbeatAddressChangesWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := store.Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	var lookups atomic.Int32
	server := New(dataStore, Config{
		AdminToken: strings.Repeat("z", 48),
		GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(request *http.Request) (*http.Response, error) {
			lookups.Add(1)
			ip := strings.TrimPrefix(request.URL.Path, "/")
			zone := map[string]string{"8.8.8.8": "Asia/Tokyo", "8.8.4.4": "Europe/London"}[ip]
			if request.URL.Host != "ipwho.is" || zone == "" {
				return nil, fmt.Errorf("unexpected fixture lookup: %s", request.URL)
			}
			body := fmt.Sprintf(`{"success":true,"ip":%q,"timezone":{"id":%q}}`, ip, zone)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})},
	})
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	agentID, privateKey := enrollWSSTestSourceAgent(t, ctx, httpServer.URL, databaseURL, dataStore, "timezone-heartbeat")
	admin := store.WithConfigScope(ctx, "", true)
	if _, _, err := dataStore.SetAgentTimeZone(admin, agentID, "auto", "", 0, false); err != nil {
		t.Fatal(err)
	}
	connection := dialPublicIPProbeWSS(t, ctx, httpServer.URL, agentID, privateKey)
	defer connection.Close(websocket.StatusNormalClosure, "test complete")
	heartbeat := func(ip string) {
		t.Helper()
		if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
			Features: []string{core.AgentFeatureProcessTimeZone},
			Metrics:  &core.HostMetrics{PublicIPv4: ip, PublicIPv4Source: core.PublicIPProbeSourceAgent},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	var revision int64
	for _, sample := range []struct{ ip, zone string }{{"8.8.8.8", "Asia/Tokyo"}, {"8.8.4.4", "Europe/London"}} {
		heartbeat(sample.ip)
		var message core.WireMessage
		if err := wsjson.Read(ctx, connection, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type != core.WireAgentTimeZone || message.AgentTimeZone == nil || message.AgentTimeZone.TimeZone != sample.zone || message.AgentTimeZone.Revision <= revision {
			t.Fatalf("heartbeat time zone command = %+v, want %s after revision %d", message, sample.zone, revision)
		}
		revision = message.AgentTimeZone.Revision
		if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireAgentTimeZoneResult, AgentTimeZoneResult: &core.AgentTimeZoneResult{
			Revision: revision, TimeZone: sample.zone,
		}}); err != nil {
			t.Fatal(err)
		}
		for {
			state, err := dataStore.GetAgentTimeZone(ctx, agentID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status == "applied" && state.Revision == revision {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("Agent time zone acknowledgement was not applied")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	// A normal task acts as an ordering sentinel after an unchanged heartbeat.
	// The next message must not repeat the already acknowledged zone command.
	task, err := dataStore.CreateTask(ctx, core.TaskRequest{AgentID: agentID, Action: core.ActionStatus, Engine: core.EngineMihomo})
	if err != nil {
		t.Fatal(err)
	}
	heartbeat("8.8.4.4")
	var message core.WireMessage
	if err := wsjson.Read(ctx, connection, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != core.WireTask || message.Task == nil || message.Task.ID != task.ID {
		t.Fatalf("unchanged heartbeat repeated a time zone command: %+v", message)
	}
	state, err := dataStore.GetAgentTimeZone(ctx, agentID)
	if err != nil || state.Status != "applied" || state.AppliedTimeZone != "Europe/London" || state.Revision != revision || lookups.Load() != 2 {
		t.Fatalf("final time zone=%+v, lookups=%d, error=%v", state, lookups.Load(), err)
	}
}
