package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type ipQualityRoundTripFunc func(*http.Request) (*http.Response, error)

func (f ipQualityRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const archiveFixture = `<svg xmlns="http://www.w3.org/2000/svg" width="900" height="500"><style>text{fill:black}</style><text x="10" y="30">IPQuality fixture</text></svg>`

func TestIPQualityArchiveDownloadLimits(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"valid", archiveFixture, 200, false},
		{"not-found", archiveFixture, 404, true},
		{"redirect", archiveFixture, 302, true},
		{"html", "<html>error page</html>", 200, true},
		{"truncated", `<svg xmlns="http://www.w3.org/2000/svg">`, 200, true},
		{"two-roots", archiveFixture + archiveFixture, 200, true},
		{"trailing-garbage", archiveFixture + "broken", 200, true},
		{"oversized", strings.Repeat("x", core.MaxIPQualityArchiveBytes+1), 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newIPQualityArchiveClient()
			client.Transport = ipQualityRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "Report.Check.Place" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Fatal("incorrect download destination or leaked credentials")
				}
				return &http.Response{StatusCode: test.status, Header: http.Header{"Location": []string{"http://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader(test.body)), Request: r}, nil
			})
			archive, err := downloadIPQualityArchive(context.Background(), client, "https://Report.Check.Place/IP/fixture.svg", 4)
			if (err != nil) != test.wantError {
				t.Fatalf("download: %v", err)
			}
			if err == nil && (string(archive.Content) != test.body || archive.Size != len(test.body) || len(archive.SHA256) != 64 || archive.DownloadedAt.IsZero()) {
				t.Fatalf("incomplete archive: %+v", archive)
			}
		})
	}
	client := newIPQualityArchiveClient()
	client.Transport = ipQualityRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("requested an untrusted URL")
		return nil, errors.New("unreachable")
	})
	if _, err := downloadIPQualityArchive(context.Background(), client, "http://127.0.0.1/a.svg", 4); err == nil {
		t.Fatal("accepted unsafe URL")
	}
	client.Transport = ipQualityRoundTripFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := downloadIPQualityArchive(ctx, client, "https://Report.Check.Place/IP/fixture.svg", 4); err == nil {
		t.Fatal("ignored cancellation")
	}
}
