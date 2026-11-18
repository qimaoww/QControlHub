package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestProxyResultTransitions(t *testing.T) {
	at := time.Now().UTC()
	row := core.ProxyStatus{Installed: "v2.11.7", Running: "v2.11.7", Outcome: "恢复失败"}
	recordProxyResult(&row, true, "v2.11.9", nil, at)
	if row.UpdateState != "recovery_failed" || row.Outcome != "恢复失败" || row.Latest != "v2.11.9" {
		t.Fatalf("read erased legacy recovery state: %+v", row)
	}
	recordProxyResult(&row, false, "", issueCaddyModules, at.Add(time.Second))
	recordProxyResult(&row, true, "", errors.New("private.example"), at.Add(2*time.Second))
	if row.UpdateState != "blocked" || row.UpdateError != issueCaddyModules.Error() || row.CheckedAt != at || row.CheckError == "" || strings.Contains(row.CheckError, "private.example") {
		t.Fatalf("check overwrote verified data or disclosed diagnostics: %+v", row)
	}
	recordProxyResult(&row, true, "v2.11.9", nil, at.Add(3*time.Second))
	if row.CheckError != "" || row.UpdateState != "blocked" || row.Outcome != "更新失败" {
		t.Fatalf("check cleared an independent attempt: %+v", row)
	}
	recordProxyResult(&row, false, "v2.11.9", fmt.Errorf("%w: private.example", issueRolledBack), at)
	if row.UpdateState != "rolled_back" || row.UpdateError != issueRolledBack.Error() || row.Running != "v2.11.7" {
		t.Fatalf("rollback changed running identity: %+v", row)
	}
	row.UpdateStartedAt = at
	reconcileProxyAttempt(&row, at.Add(time.Minute))
	if row.UpdateState != "interrupted" || !row.UpdateStartedAt.IsZero() || row.Latest != "v2.11.9" || row.Running != "v2.11.7" {
		t.Fatalf("abandoned switch did not reconcile: %+v", row)
	}
	recordProxyResult(&row, false, "v2.11.9", nil, at)
	if row.UpdateError != "" || row.UpdateState != "succeeded" || row.Outcome != "" {
		t.Fatalf("successful retry left errors: %+v", row)
	}
}

func TestCaddyHistoricalProvenance(t *testing.T) {
	data := bytes.Repeat([]byte("fixture"), 160000)
	var archive bytes.Buffer
	compressed, err := gzip.NewWriterLevel(&archive, gzip.NoCompression)
	if err != nil {
		t.Fatal(err)
	}
	w := tar.NewWriter(compressed)
	if err := w.WriteHeader(&tar.Header{Name: "caddy", Mode: 0755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"identical", "different binary", "bad archive", "other tag", "unavailable", "other arch", "prerelease"} {
		t.Run(scenario, func(t *testing.T) {
			updater := NewCoreUpdater()
			updater.goarch = "amd64"
			version := "v2.11.7"
			name := "caddy_2.11.7_linux_amd64.tar.gz"
			url := "https://github.com/caddyserver/caddy/releases/download/" + version + "/" + name
			asset := githubReleaseAsset{Name: name, BrowserDownloadURL: url, Size: int64(archive.Len()), Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(archive.Bytes()))}
			digest := fmt.Sprintf("%x", sha256.Sum256(data))
			want := issueHistoricalRelease
			switch scenario {
			case "identical":
				want = ""
			case "different binary":
				digest = strings.Repeat("a", 64)
				want = issueHistoricalMismatch
			case "bad archive":
				asset.Digest = "sha256:" + strings.Repeat("0", 64)
			case "other arch":
				updater.goarch = "arm64"
			}
			release := githubRelease{TagName: version, Assets: []githubReleaseAsset{asset}, Prerelease: scenario == "prerelease"}
			if scenario == "other tag" {
				release.TagName = "v2.11.8"
			}
			updater.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body []byte
				status := 200
				switch r.URL.String() {
				case "https://api.github.com/repos/caddyserver/caddy/releases/tags/" + version:
					body, _ = json.Marshal(release)
					if scenario == "unavailable" {
						status = 404
					}
				case url:
					body = archive.Bytes()
				default:
					t.Fatalf("unexpected historical release request %s", r.URL)
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header), Request: r}, nil
			})
			err := verifyHistoricalCaddy(t.Context(), updater, version, digest)
			if want == "" && err != nil || want != "" && !errors.Is(err, want) {
				t.Fatalf("want %q, got %v", want, err)
			}
		})
	}
}

func TestCaddyModuleProtection(t *testing.T) {
	if err := validateCaddyModuleList("", nil); err != nil {
		t.Fatal(err)
	}
	if err := validateCaddyModuleList("dns.providers.fixture example.test/module", nil); err != issueCaddyModules {
		t.Fatal(err)
	}
	if err := validateCaddyModuleList("", errors.New("permission denied")); err != issueModuleInspection {
		t.Fatal(err)
	}
}
