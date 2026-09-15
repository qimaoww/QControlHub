package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
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
