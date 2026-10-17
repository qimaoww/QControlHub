package agent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestHTTPChallengeExactHostTokenAndExpiry(t *testing.T) {
	service := &certificateChallengeService{routes: map[string]core.HTTPChallenge{"a.example.test/abcdefghijklmnop": {Value: "proof.a", ExpiresAt: time.Now().Add(time.Minute)}, "b.example.test/abcdefghijklmnop": {Value: "proof.b", ExpiresAt: time.Now().Add(-time.Second)}}}
	for _, test := range []struct {
		host, path string
		status     int
		body       string
	}{{"a.example.test", "/.well-known/acme-challenge/abcdefghijklmnop", 200, "proof.a"}, {"other.example.test", "/.well-known/acme-challenge/abcdefghijklmnop", 404, ""}, {"b.example.test", "/.well-known/acme-challenge/abcdefghijklmnop", 404, ""}, {"a.example.test", "/.well-known/acme-challenge/../../etc/passwd", 404, ""}, {"a.example.test", "/.well-known/acme-challenge/abcdefghijklmnop?x=1", 404, ""}} {
		r := httptest.NewRequest("GET", "http://"+test.host+test.path, nil)
		w := httptest.NewRecorder()
		service.ServeHTTP(w, r)
		if w.Code != test.status || (test.body != "" && w.Body.String() != test.body) {
			t.Fatalf("%s %s: %d %s", test.host, test.path, w.Code, w.Body.String())
		}
	}
	service.Close()
	w := httptest.NewRecorder()
	service.ServeHTTP(w, httptest.NewRequest("GET", "http://a.example.test/.well-known/acme-challenge/abcdefghijklmnop", nil))
	if w.Code != 404 {
		t.Fatal("cleanup leaked route")
	}
	if err := service.Replace(context.Background(), []core.HTTPChallenge{{OrderID: "o", Domain: "a.example.test", Token: strings.Repeat("/", 20), ExpiresAt: time.Now().Add(time.Minute)}}); err == nil {
		t.Fatal("accepted path traversal token")
	}
}
