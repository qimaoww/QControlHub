package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type reconnectTransport func(*http.Request) (*http.Response, error)

func (transport reconnectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// Exercise the real session/Run loops without starting host services or nftables.
func reconnectTestClient(t *testing.T) *Client {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{
		config: ClientConfig{
			StatePath: filepath.Join(t.TempDir(), "agent-state.json"),
			// Accelerate queue saturation; production uses 1-30 second intervals.
			HeartbeatEvery: time.Millisecond, MetricsEvery: time.Hour,
		},
		creds: credentials{
			AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(privateKey), Server: "reconnect.test",
		},
		serverHost: "reconnect.test", websocketURL: "ws://reconnect.test/agent/v1/connect",
		executor: &Executor{}, metrics: NewMetricsCollector(), logs: &CoreLogCollector{}, http: &http.Client{},
	}
}

func TestRunWebSocketBoundsHandshake(t *testing.T) {
	client := reconnectTestClient(t)
	client.http.Transport = reconnectTransport(func(request *http.Request) (*http.Response, error) {
		deadline, ok := request.Context().Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
			t.Errorf("handshake deadline = %v, present=%v; want bounded to 30s", deadline, ok)
		}
		return nil, context.DeadlineExceeded
	})
	if err := client.runWebSocket(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handshake timeout error = %v", err)
	}
}

func TestRunWebSocketCancelsStalledHandshake(t *testing.T) {
	client := reconnectTestClient(t)
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done() // Accept TCP but never send response headers.
	}))
	defer server.Close()
	client.websocketURL = "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- client.runWebSocket(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handshake never started")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled handshake error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stalled handshake ignored process cancellation")
	}
}

func TestRunWebSocketHandshakeCancellationKeepsSessionAlive(t *testing.T) {
	client := reconnectTestClient(t)
	heartbeats := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(w, request, &websocket.AcceptOptions{Subprotocols: []string{"qcontrolhub.agent.v1"}})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for i := 0; ; i++ {
			var message core.WireMessage
			if err := wsjson.Read(request.Context(), connection, &message); err != nil {
				return
			}
			if i == 2 {
				close(heartbeats)
			}
		}
	}))
	defer server.Close()
	client.websocketURL = "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- client.runWebSocket(ctx) }()
	select {
	case <-heartbeats:
	case err := <-finished:
		t.Fatalf("handshake cancellation terminated the established session: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("established session did not keep sending heartbeats")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("session ignored process cancellation")
	}
}

// Independently fail reads or writes after the producer has filled its queue.
type blockedWebSocketBody struct {
	readFailure, writeFailure, closed, writeStarted chan struct{}
	closeOnce, writeOnce                            sync.Once
}

func newBlockedWebSocketBody(t *testing.T) *blockedWebSocketBody {
	body := &blockedWebSocketBody{
		readFailure: make(chan struct{}), writeFailure: make(chan struct{}),
		closed: make(chan struct{}), writeStarted: make(chan struct{}),
	}
	t.Cleanup(func() { _ = body.Close() })
	return body
}

func (body *blockedWebSocketBody) Read([]byte) (int, error) {
	select {
	case <-body.readFailure:
		return 0, errors.New("simulated disconnected reader")
	case <-body.closed:
		return 0, io.ErrClosedPipe
	}
}

func (body *blockedWebSocketBody) Write([]byte) (int, error) {
	body.writeOnce.Do(func() { close(body.writeStarted) })
	select {
	case <-body.writeFailure:
		return 0, errors.New("simulated disconnected writer")
	case <-body.closed:
		return 0, io.ErrClosedPipe
	}
}

func (body *blockedWebSocketBody) Close() error {
	body.closeOnce.Do(func() { close(body.closed) })
	return nil
}

func (body *blockedWebSocketBody) response(request *http.Request) *http.Response {
	digest := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return &http.Response{
		StatusCode: http.StatusSwitchingProtocols,
		Header: http.Header{
			"Upgrade": {"websocket"}, "Connection": {"Upgrade"},
			"Sec-Websocket-Accept":   {base64.StdEncoding.EncodeToString(digest[:])},
			"Sec-Websocket-Protocol": {"qcontrolhub.agent.v1"},
		},
		Body: body,
	}
}

func waitForBlockedWireProducer(t *testing.T, producer string, finished <-chan error) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		// Wait for the actual blocked enqueue rather than guessing when the
		// scheduler and /proc collection have produced 16 messages.
		stack := make([]byte, 1<<20)
		stack = stack[:runtime.Stack(stack, true)]
		for _, goroutine := range strings.Split(string(stack), "\n\n") {
			if strings.Contains(goroutine, "[select]") && strings.Contains(goroutine, "(*Client)."+producer) && strings.Contains(goroutine, "(*Client).runWebSocket") {
				return
			}
		}
		select {
		case err := <-finished:
			t.Fatalf("session returned before queue saturation: %v", err)
		case <-deadline.C:
			t.Fatalf("did not observe %s blocked on the full outgoing queue", producer)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestRunWebSocketReleasesSaturatedQueue(t *testing.T) {
	for _, producer := range []string{"queueHeartbeat", "queueMetrics"} {
		for _, failure := range []string{"read", "write", "cancel"} {
			t.Run(producer+"/"+failure, func(t *testing.T) {
				client := reconnectTestClient(t)
				if producer == "queueMetrics" {
					client.config.HeartbeatEvery, client.config.MetricsEvery = time.Hour, time.Millisecond
					_, _ = client.metrics.Collect(context.Background())
					client.metrics.previous.cpuAt = time.Now().Add(-time.Second)
				}
				body := newBlockedWebSocketBody(t)
				client.http.Transport = reconnectTransport(func(request *http.Request) (*http.Response, error) {
					return body.response(request), nil
				})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				finished := make(chan error, 1)
				go func() { finished <- client.runWebSocket(ctx) }()
				waitForBlockedWireProducer(t, producer, finished)
				switch failure {
				case "read":
					close(body.readFailure)
				case "write":
					close(body.writeFailure)
				case "cancel":
					cancel()
				}
				select {
				case err := <-finished:
					if failure != "cancel" && (err == nil || errors.Is(err, context.Canceled)) {
						t.Fatalf("lost the transport failure behind generic cancellation: %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("session stayed blocked after its transport failed")
				}
				if failure != "cancel" && ctx.Err() != nil {
					t.Fatal("session failure canceled the process/in-flight-task context")
				}
			})
		}
	}
}

func TestRunReconnectsAfterSaturatedSession(t *testing.T) {
	client := reconnectTestClient(t)
	if err := saveCredentials(client.config.StatePath, client.creds); err != nil {
		t.Fatal(err)
	}
	first, second := newBlockedWebSocketBody(t), newBlockedWebSocketBody(t)
	var attempts atomic.Int32
	client.http.Transport = reconnectTransport(func(request *http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			return first.response(request), nil
		}
		return second.response(request), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- client.Run(ctx) }()
	waitForBlockedWireProducer(t, "queueHeartbeat", finished)
	close(first.writeFailure)
	select {
	case <-second.writeStarted:
	case err := <-finished:
		t.Fatalf("Run stopped instead of reconnecting: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not send a heartbeat on a new session")
	}
	if ctx.Err() != nil || attempts.Load() != 2 {
		t.Fatalf("reconnect canceled process or made unexpected attempts: %v, %d", ctx.Err(), attempts.Load())
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Run cancellation = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reconnected Run did not stop")
	}
}

func TestRunRetriesTransientHandshakeFailures(t *testing.T) {
	client := reconnectTestClient(t)
	if err := saveCredentials(client.config.StatePath, client.creds); err != nil {
		t.Fatal(err)
	}
	body := newBlockedWebSocketBody(t)
	var attempts atomic.Int32
	client.http.Transport = reconnectTransport(func(request *http.Request) (*http.Response, error) {
		switch attempts.Add(1) {
		case 1:
			return nil, context.DeadlineExceeded
		case 2:
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable", Body: io.NopCloser(strings.NewReader("temporarily unavailable"))}, nil
		default:
			return body.response(request), nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- client.Run(ctx) }()
	select {
	case <-body.writeStarted:
	case err := <-finished:
		t.Fatalf("Run stopped on transient handshake errors: %v", err)
	case <-time.After(7 * time.Second):
		t.Fatal("Run did not recover after repeated handshake failures")
	}
	if attempts.Load() != 3 || ctx.Err() != nil {
		t.Fatalf("handshake recovery: attempts=%d, context=%v", attempts.Load(), ctx.Err())
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Run cancellation = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recovered Run did not stop")
	}
}

func TestRunRecoversAfterHandshakeTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises the real 30-second handshake deadline")
	}
	client := reconnectTestClient(t)
	if err := saveCredentials(client.config.StatePath, client.creds); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	recovered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if attempts.Add(1) == 1 {
			<-request.Context().Done() // No upgrade response until the Agent times out.
			return
		}
		connection, err := websocket.Accept(w, request, &websocket.AcceptOptions{Subprotocols: []string{"qcontrolhub.agent.v1"}})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		var message core.WireMessage
		if err := wsjson.Read(request.Context(), connection, &message); err != nil || message.Type != core.WireHeartbeat {
			return
		}
		close(recovered)
		for wsjson.Read(request.Context(), connection, &message) == nil {
		}
	}))
	defer server.Close()
	client.websocketURL = "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- client.Run(ctx) }()
	select {
	case <-recovered:
	case err := <-finished:
		t.Fatalf("Run stopped after the stalled handshake: %v", err)
	case <-time.After(40 * time.Second):
		t.Fatal("Agent did not reconnect after its handshake deadline")
	}
	if attempts.Load() != 2 || ctx.Err() != nil {
		t.Fatalf("handshake recovery: attempts=%d, context=%v", attempts.Load(), ctx.Err())
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Run cancellation = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recovered Run did not stop")
	}
}
