//go:build linux

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func deliverCoreLogTransitionAcrossReconnect(t *testing.T, client *Client, collector *CoreLogCollector, token string) int {
	t.Helper()
	var connections atomic.Int32
	var messageCount atomic.Int32
	batchIDs := make(chan string, 2)
	firstClosed := make(chan struct{})
	ackSent := make(chan struct{})
	releaseSecond := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
			Subprotocols: []string{"qcontrolhub.agent.v1"},
		})
		if err != nil {
			return
		}
		defer connection.Close(websocket.StatusNormalClosure, "test complete")
		number := connections.Add(1)
		readContext, cancel := context.WithTimeout(request.Context(), 10*time.Second)
		defer cancel()
		for {
			var message core.WireMessage
			if err := wsjson.Read(readContext, connection, &message); err != nil {
				return
			}
			if message.Type != core.WireCoreLogs || message.CoreLogs == nil {
				continue
			}
			matches := 0
			for _, entry := range message.CoreLogs.Entries {
				if entry.Message == token {
					matches++
				}
			}
			if matches != 1 {
				t.Errorf("WSS transition batch token matches = %d, batch=%+v", matches, message.CoreLogs)
			}
			messageCount.Add(int32(matches))
			batchIDs <- message.CoreLogs.ID
			if number == 1 {
				_ = connection.Close(websocket.StatusGoingAway, "simulate reconnect before ACK")
				close(firstClosed)
				return
			}
			if err := wsjson.Write(readContext, connection, core.WireMessage{
				Type: core.WireCoreLogsAck, BatchID: message.CoreLogs.ID,
			}); err != nil {
				t.Errorf("write WSS core log ACK: %v", err)
				return
			}
			close(ackSent)
			<-releaseSecond
			return
		}
	}))
	defer server.Close()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "wss-state.json")
	client.creds = credentials{AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(privateKey)}
	client.http = server.Client()
	client.websocketURL = "ws" + strings.TrimPrefix(server.URL, "http")
	client.config.Version = "transition-test"
	client.config.HeartbeatEvery = 30 * time.Second
	client.config.MetricsEvery = 30 * time.Second
	client.metrics = NewMetricsCollector()
	client.traffic = NewTrafficManager(statePath)

	firstContext, firstCancel := context.WithTimeout(context.Background(), 10*time.Second)
	firstErr := client.runWebSocket(firstContext)
	firstCancel()
	if firstErr == nil {
		t.Fatal("first WSS session did not disconnect before ACK")
	}
	select {
	case <-firstClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("first WSS session did not deliver transition batch")
	}

	secondContext, secondCancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() { secondDone <- client.runWebSocket(secondContext) }()
	select {
	case <-ackSent:
	case <-time.After(10 * time.Second):
		secondCancel()
		close(releaseSecond)
		t.Fatal("reconnected WSS session did not acknowledge transition batch")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		collector.mu.Lock()
		acknowledged := collector.pending == nil
		collector.mu.Unlock()
		if acknowledged {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	collector.mu.Lock()
	acknowledged := collector.pending == nil
	collector.mu.Unlock()
	if !acknowledged {
		secondCancel()
		close(releaseSecond)
		t.Fatal("reconnected WSS ACK did not clear the pending transition batch")
	}
	secondCancel()
	close(releaseSecond)
	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnected WSS session leaked after cancellation")
	}
	firstID, secondID := <-batchIDs, <-batchIDs
	if firstID == "" || secondID != firstID {
		t.Fatalf("WSS reconnect batch IDs = %q and %q", firstID, secondID)
	}
	if messageCount.Load() != 2 {
		t.Fatalf("transition token wire deliveries = %d, want one retry of one logical batch", messageCount.Load())
	}
	return 1
}

func writeSingBoxTransitionConfig(t *testing.T, executor *Executor, content string) {
	t.Helper()
	if err := os.WriteFile(executor.Specs[core.EngineSingBox].ConfigPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendSingBoxTransitionLine(t *testing.T, path, token string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitForCoreLogSourceStatus(t *testing.T, collector *CoreLogCollector, engine core.Engine, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if collector.Status()[engine].Status == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("core log source status = %+v, want %q", collector.Status()[engine], want)
}

func collectCoreLogTransitionToken(t *testing.T, collector *CoreLogCollector, token string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	quietUntil := time.Time{}
	count := 0
	for time.Now().Before(deadline) {
		batch := collector.NextBatch()
		if batch == nil {
			if !quietUntil.IsZero() && time.Now().After(quietUntil) {
				return count
			}
			time.Sleep(25 * time.Millisecond)
			continue
		}
		retry := collector.NextBatch()
		if retry == nil || retry.ID != batch.ID {
			t.Fatalf("pending batch was not stable across reconnect: first=%+v retry=%+v", batch, retry)
		}
		for _, entry := range batch.Entries {
			if entry.Message == token {
				count++
				quietUntil = time.Now().Add(1200 * time.Millisecond)
			}
		}
		if !collector.Acknowledge(batch.ID) {
			t.Fatalf("failed to acknowledge transition batch %q", batch.ID)
		}
	}
	return count
}
