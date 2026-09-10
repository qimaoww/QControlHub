package api

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func gzipRequestBody(t *testing.T, body io.Reader) []byte {
	t.Helper()
	reader, err := gzip.NewReader(body)
	if err != nil {
		t.Fatalf("decode gzip response: %v", err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip response: %v", err)
	}
	return decoded
}

func serveCompressed(t *testing.T, handler http.HandlerFunc, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	middleware := (&Server{}).middleware(handler)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/probe", nil)
	if acceptEncoding != "" {
		request.Header.Set("Accept-Encoding", acceptEncoding)
	}
	recorder := httptest.NewRecorder()
	middleware.ServeHTTP(recorder, request)
	return recorder
}

func TestResponseCompressionEncodesLargeJSON(t *testing.T) {
	payload := strings.Repeat(`{"message":"repeated kernel log line"},`, 400)
	recorder := serveCompressed(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, payload)
	}, "gzip, deflate, br")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	if encoding := recorder.Header().Get("Content-Encoding"); encoding != "gzip" {
		t.Fatalf("Content-Encoding=%q, want gzip", encoding)
	}
	if !strings.Contains(recorder.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary=%q must include Accept-Encoding", recorder.Header().Get("Vary"))
	}
	if length := recorder.Header().Get("Content-Length"); length != "" {
		t.Fatalf("Content-Length=%q must not be sent with a compressed body", length)
	}
	if decoded := string(gzipRequestBody(t, recorder.Body)); decoded != payload {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(decoded), len(payload))
	}
	if recorder.Body.Len() >= len(payload)/4 {
		t.Fatalf("compressed body %d bytes is not smaller than %d", recorder.Body.Len(), len(payload)/4)
	}
}

func TestResponseCompressionLeavesSmallBodiesFramed(t *testing.T) {
	recorder := serveCompressed(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Length", "16")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}, "gzip")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	if encoding := recorder.Header().Get("Content-Encoding"); encoding != "" {
		t.Fatalf("Content-Encoding=%q must stay empty below the threshold", encoding)
	}
	if length := recorder.Header().Get("Content-Length"); length != "16" {
		t.Fatalf("Content-Length=%q, want the handler's original value", length)
	}
	if recorder.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("body=%q", recorder.Body.String())
	}
}

func TestResponseCompressionKeepsEmptyAndErrorResponsesReadable(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		status     int
		body       string
		wantStatus int
	}{
		{"empty body", http.StatusNoContent, "", http.StatusNoContent},
		{"small error", http.StatusForbidden, `{"error":"forbidden"}`, http.StatusForbidden},
		{"large error", http.StatusInternalServerError, strings.Repeat("x", 4096), http.StatusInternalServerError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := serveCompressed(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = io.WriteString(w, testCase.body)
			}, "gzip")
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status=%d, want %d", recorder.Code, testCase.wantStatus)
			}
			body := recorder.Body.Bytes()
			if recorder.Header().Get("Content-Encoding") == "gzip" {
				body = gzipRequestBody(t, bytes.NewReader(body))
			}
			if string(body) != testCase.body {
				t.Fatalf("body=%q, want %q", body, testCase.body)
			}
		})
	}
}

func TestResponseCompressionHonoursEncodingNegotiation(t *testing.T) {
	payload := strings.Repeat("kernel log line ", 200)
	handler := func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	}
	for _, testCase := range []struct {
		name           string
		acceptEncoding string
		wantEncoding   string
	}{
		{"no header", "", ""},
		{"identity only", "identity", ""},
		{"gzip accepted", "gzip", "gzip"},
		{"gzip refused by quality", "gzip;q=0", ""},
		{"gzip with parameters", "br;q=0.5, gzip;q=0.8", "gzip"},
		{"wildcard does not imply gzip", "*", ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := serveCompressed(t, handler, testCase.acceptEncoding)
			if encoding := recorder.Header().Get("Content-Encoding"); encoding != testCase.wantEncoding {
				t.Fatalf("Content-Encoding=%q, want %q", encoding, testCase.wantEncoding)
			}
			body := recorder.Body.Bytes()
			if testCase.wantEncoding == "gzip" {
				body = gzipRequestBody(t, bytes.NewReader(body))
			}
			if string(body) != payload {
				t.Fatalf("payload was altered: %d bytes", len(body))
			}
		})
	}
}

// The agent binary endpoint already gzip encodes its own body. Compressing it a
// second time would inflate the payload, so the wrapper must pass it through.
func TestResponseCompressionSkipsPreEncodedBodies(t *testing.T) {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, _ = io.WriteString(&buffer, "")
	_, _ = writer.Write([]byte(strings.Repeat("agent binary payload ", 200)))
	_ = writer.Close()
	preEncoded := buffer.Bytes()

	recorder := serveCompressed(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(preEncoded)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(preEncoded)
	}, "gzip")
	if recording := recorder.Header().Get("Content-Encoding"); recording != "gzip" {
		t.Fatalf("Content-Encoding=%q", recording)
	}
	if got := recorder.Body.Len(); got != len(preEncoded) {
		t.Fatalf("body=%d bytes, want the untouched %d", got, len(preEncoded))
	}
}

func TestRequestAcceptsGzip(t *testing.T) {
	for _, testCase := range []struct {
		header string
		want   bool
	}{
		{"", false},
		{"gzip", true},
		{"GZIP", true},
		{"deflate, gzip;q=1.0, *;q=0.1", true},
		{"gzip;q=0", false},
		{"gzip;q=0.0, br", false},
		{"br, zstd", false},
		{"*", false},
		{"x-gzip", false},
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		if testCase.header != "" {
			request.Header.Set("Accept-Encoding", testCase.header)
		}
		if got := requestAcceptsGzip(request); got != testCase.want {
			t.Fatalf("requestAcceptsGzip(%q)=%v, want %v", testCase.header, got, testCase.want)
		}
	}
}

// A hijacked connection (agent websocket upgrade) must reach the underlying
// writer; buffering it would break the upgrade handshake.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (recorder *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	recorder.hijacked = true
	return nil, nil, errors.New("probe hijack")
}

func TestResponseCompressionKeepsUpgradesUnwrapped(t *testing.T) {
	recorder := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	middleware := (&Server{}).middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(*compressedResponseWriter); !ok {
			t.Fatalf("middleware did not wrap the response writer")
		}
		if _, _, err := http.NewResponseController(w).Hijack(); err == nil {
			t.Fatalf("probe hijack must report its own error")
		}
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	request := httptest.NewRequest(http.MethodGet, "/agent/v1/connect", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	middleware.ServeHTTP(recorder, request)
	if !recorder.hijacked {
		t.Fatalf("hijack did not reach the underlying writer through the wrapper")
	}
	if recorder.Code != http.StatusSwitchingProtocols {
		t.Fatalf("status=%d", recorder.Code)
	}
	if encoding := recorder.Header().Get("Content-Encoding"); encoding != "" {
		t.Fatalf("upgrade response must not be compressed, got %q", encoding)
	}
}

// A response that already declares an upgrade must never be buffered, because
// the caller owns the connection after the handshake.
func TestResponseCompressionSkipsUpgradeResponses(t *testing.T) {
	recorder := serveCompressed(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		w.WriteHeader(http.StatusSwitchingProtocols)
		_, _ = io.WriteString(w, strings.Repeat("x", 4096))
	}, "gzip")
	if recorder.Code != http.StatusSwitchingProtocols {
		t.Fatalf("status=%d", recorder.Code)
	}
	if encoding := recorder.Header().Get("Content-Encoding"); encoding != "" {
		t.Fatalf("upgrade response must not be compressed, got %q", encoding)
	}
	if recorder.Body.Len() != 4096 {
		t.Fatalf("upgrade body=%d bytes, want 4096", recorder.Body.Len())
	}
}
