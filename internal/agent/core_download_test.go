package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/githubfetch"
)

func TestDownloadAssetRequiresAndVerifiesGitHubDigest(t *testing.T) {
	t.Parallel()
	contents := bytes.Repeat([]byte{0x5a}, 1<<20)
	digest := sha256.Sum256(contents)
	asset := githubReleaseAsset{
		Name: "Xray-linux-64.zip", Size: int64(len(contents)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/XTLS/Xray-core/releases/download/v1/Xray-linux-64.zip",
	}
	updater := &CoreUpdater{
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(contents)), Header: make(http.Header), Request: request}, nil
		})},
		trustedURL: trustedCoreReleaseURL,
	}
	path, err := updater.downloadAsset(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, contents) {
		t.Fatal("downloaded asset content did not match")
	}

	asset.Digest = "sha256:" + strings.Repeat("0", 64)
	if _, err := updater.downloadAsset(context.Background(), asset); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestDownloadAssetRetriesTruncatedOfficialResponse(t *testing.T) {
	t.Parallel()
	contents := bytes.Repeat([]byte{0x6b}, 1<<20)
	digest := sha256.Sum256(contents)
	asset := githubReleaseAsset{
		Name: "Xray-linux-64.zip", Size: int64(len(contents)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/XTLS/Xray-core/releases/download/v1/Xray-linux-64.zip",
	}
	attempts := 0
	updater := &CoreUpdater{
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			body := io.Reader(bytes.NewReader(contents))
			if attempts == 1 {
				body = io.MultiReader(bytes.NewReader(contents[:len(contents)/2]), errorReader{err: io.ErrUnexpectedEOF})
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body), Header: make(http.Header), Request: request}, nil
		})},
		trustedURL: trustedCoreReleaseURL, downloadAttempts: 2,
		downloadAttemptTimeout: time.Second, downloadRetryDelay: time.Nanosecond,
	}
	path, err := updater.downloadAsset(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if attempts != 2 {
		t.Fatalf("download attempts = %d, want 2", attempts)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, contents) {
		t.Fatal("retried asset content did not match")
	}
}

func TestDownloadAssetDoesNotRetryPermanentHTTPFailure(t *testing.T) {
	t.Parallel()
	contents := bytes.Repeat([]byte{0x7c}, 1<<20)
	digest := sha256.Sum256(contents)
	asset := githubReleaseAsset{
		Name: "Xray-linux-64.zip", Size: int64(len(contents)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/XTLS/Xray-core/releases/download/v1/Xray-linux-64.zip",
	}
	attempts := 0
	updater := &CoreUpdater{
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header), Request: request}, nil
		})},
		trustedURL: trustedCoreReleaseURL, downloadAttempts: 3,
		downloadAttemptTimeout: time.Second, downloadRetryDelay: time.Nanosecond,
	}
	if _, err := updater.downloadAsset(context.Background(), asset); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("permanent download error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("download attempts = %d, want 1", attempts)
	}
}

func TestDownloadTotalBudgetIncludesRetryBackoff(t *testing.T) {
	attempts := 0
	updater := NewCoreUpdater()
	updater.downloadAttemptTimeout = 100 * time.Millisecond
	updater.downloadRetryDelay = 75 * time.Millisecond
	updater.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: http.NoBody}, nil
	})}
	asset := githubReleaseAsset{Name: "fixture.gz", Size: 1 << 20, Digest: "sha256:" + strings.Repeat("0", 64), BrowserDownloadURL: "https://github.com/o/r/releases/download/v1/fixture.gz"}
	_, err := updater.downloadAsset(context.Background(), asset)
	if !errors.Is(err, context.DeadlineExceeded) || attempts > 2 {
		t.Fatalf("retry reset total budget: attempts=%d err=%v", attempts, err)
	}
}

func TestDownloadOfficialAssetAPIFallback(t *testing.T) {
	t.Parallel()
	contents := bytes.Repeat([]byte{0x4a}, 1<<20)
	digest := sha256.Sum256(contents)
	for _, scenario := range []string{"gateway", "network", "corrupt-fallback", "permanent", "invalid-digest", "tls", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			asset := githubReleaseAsset{ID: 123, Name: "fixture.gz", Size: int64(len(contents)), Digest: "sha256:" + hex.EncodeToString(digest[:]), BrowserDownloadURL: "https://github.com/owner/repo/releases/download/v1/fixture.gz"}
			var routes []string
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			updater := &CoreUpdater{trustedURL: trustedCoreReleaseURL, downloadAttempts: 1, client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				routes = append(routes, request.URL.String())
				status := http.StatusOK
				if request.URL.Host == "github.com" {
					switch scenario {
					case "gateway", "corrupt-fallback":
						status = http.StatusGatewayTimeout
					case "network", "tls":
						category := "network"
						if scenario == "tls" {
							category = "validation"
						}
						return nil, &githubfetch.Failure{Direct: errors.New("fixture transport failure"), Category: category}
					case "permanent":
						status = http.StatusNotFound
					case "invalid-digest":
						return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", len(contents))))}, nil
					case "cancel":
						cancel()
						return nil, context.Canceled
					}
				}
				if request.Header.Get("Accept") != "application/octet-stream" || request.Header.Get("Authorization") != "" {
					t.Fatal("asset route must request public binary content without credentials")
				}
				value := contents
				if scenario == "corrupt-fallback" {
					value = bytes.Repeat([]byte{'x'}, len(contents))
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(value))}, nil
			})}}
			path, err := updater.downloadAsset(ctx, asset)
			if path != "" {
				defer os.Remove(path)
			}
			wantSuccess := scenario == "gateway" || scenario == "network"
			if (err == nil) != wantSuccess {
				t.Fatalf("download result: path=%q err=%v", path, err)
			}
			wantFallback := wantSuccess || scenario == "corrupt-fallback"
			if (len(routes) == 2) != wantFallback || len(routes) > 2 {
				t.Fatalf("unexpected routes: %v", routes)
			}
			if wantFallback && routes[1] != "https://api.github.com/repos/owner/repo/releases/assets/123" {
				t.Fatalf("unexpected asset API: %v", routes)
			}
			if scenario == "corrupt-fallback" && !strings.Contains(err.Error(), "SHA-256") {
				t.Fatalf("fallback bypassed digest validation: %v", err)
			}
		})
	}
}
