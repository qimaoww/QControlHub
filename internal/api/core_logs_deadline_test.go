package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCoreLogReadDeadline(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		request := httptest.NewRequest("GET", "/api/v1/core-logs", nil)
		var cancel context.CancelFunc
		if inherited {
			ctx, stop := context.WithTimeout(request.Context(), time.Second)
			cancel = stop
			request = request.WithContext(ctx)
		}
		called := false
		coreLogReadDeadline(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			called = true
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) > 12*time.Second {
				t.Fatal("unbounded log authentication/query")
			}
			if inherited && time.Until(deadline) > time.Second {
				t.Fatal("extended caller deadline")
			}
		})).ServeHTTP(httptest.NewRecorder(), request)
		if cancel != nil {
			cancel()
		}
		if !called {
			t.Fatal("handler not called")
		}
	}
}

func TestCoreLogReadDeadlineResponse(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	request := httptest.NewRequest("GET", "/api/v1/core-logs", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	writeCoreLogReadError(response, request, context.DeadlineExceeded)
	if response.Code != http.StatusGatewayTimeout || !strings.Contains(response.Body.String(), "读取超时，请重试。") {
		t.Fatalf("deadline response: %d %s", response.Code, response.Body.String())
	}
}

func TestReadCancellationRequiresCanceledRequestAndCause(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("database unavailable")} {
			request := httptest.NewRequest("GET", "/api/v1/core-logs", nil)
			ctx, cancel := context.WithCancel(request.Context())
			defer cancel()
			request = request.WithContext(ctx)
			if stopped {
				cancel()
			}
			response := httptest.NewRecorder()
			writeCoreLogReadError(response, request, cause)
			expected := 500
			if stopped && cause == context.Canceled {
				expected = 499
			}
			if response.Code != expected {
				t.Fatalf("stopped=%t cause=%v status=%d", stopped, cause, response.Code)
			}
		}
	}
	// A committed write cannot acquire the read-only cancellation meaning.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if writeCanceledRead(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/v1/tasks", nil).WithContext(ctx), context.Canceled) {
		t.Fatal("write outcome was suppressed")
	}
}
