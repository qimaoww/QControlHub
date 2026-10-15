package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/githubfetch"
)

func TestUnknownDNSFailureReleaseMetadataAndVerifiedAssetsAllChannels(t *testing.T) {
	contents := bytes.Repeat([]byte{0x41}, 1<<20)
	digest := sha256.Sum256(contents)
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		for _, selector := range []string{core.CoreVersionStable, core.CoreVersionDevelopment, "1.2.3"} {
			sources := []string{""}
			if engine == core.EngineMihomo && selector == core.CoreVersionDevelopment {
				sources = append(sources, string(core.CoreSourceMirror))
			}
			for _, source := range sources {
				t.Run(string(engine)+"/"+selector+"/"+source, func(t *testing.T) {
					repository, err := officialCoreRepository(engine, selector, source)
					if err != nil {
						t.Fatal(err)
					}
					names := map[core.Engine]string{core.EngineMihomo: "mihomo-linux-amd64-v1.2.3.gz", core.EngineXray: "Xray-linux-64.zip", core.EngineSingBox: "sing-box-1.2.3-linux-amd64.tar.gz", core.EngineShadowsocksRust: "shadowsocks-v1.2.3.x86_64-unknown-linux-gnu.tar.xz"}
					asset := githubReleaseAsset{Name: names[engine], Size: int64(len(contents)), Digest: "sha256:" + hex.EncodeToString(digest[:]), BrowserDownloadURL: "https://github.com/" + repository + "/releases/download/v1.2.3/" + names[engine]}
					release := githubRelease{TagName: "v1.2.3", Prerelease: selector == core.CoreVersionDevelopment, Assets: []githubReleaseAsset{asset}}
					var payload any = release
					if selector == core.CoreVersionDevelopment {
						payload = []githubRelease{release}
					}
					metadata, _ := json.Marshal(payload)
					requests := 0
					client := &http.Client{Transport: &githubfetch.Transport{Detect: func(context.Context) githubfetch.Mode { return githubfetch.Unknown }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						requests++
						if !strings.HasPrefix(r.URL.String(), githubfetch.Prefix) {
							return nil, &net.DNSError{Name: r.URL.Hostname(), Err: "i/o timeout", IsTimeout: true}
						}
						raw, err := githubfetch.Original(r.URL.String())
						if err != nil || !strings.HasPrefix(r.URL.String(), githubfetch.Prefix) {
							t.Fatalf("not accelerated: %s %v", r.URL, err)
						}
						body := contents
						if strings.Contains(raw, "api.github.com") {
							body = metadata
							if selector == core.CoreVersionDevelopment && !strings.HasSuffix(raw, "?per_page=5&page=1") {
								t.Fatalf("lost pagination: %s", raw)
							}
						}
						return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header), Request: r}, nil
					})}}
					updater := NewCoreUpdater()
					updater.client = client
					updater.goarch = "amd64"
					updater.libc = "gnu"
					resolved, err := updater.resolveRelease(context.Background(), engine, selector, source)
					if err != nil {
						t.Fatal(err)
					}
					path, err := updater.downloadAsset(context.Background(), resolved.Asset)
					if err != nil {
						t.Fatal(err)
					}
					defer os.Remove(path)
					if requests != 3 {
						t.Fatalf("requests=%d want failed direct API, accelerated API and asset", requests)
					}
				})
			}
		}
	}
}

func TestIPv6FailedDownloadCleansTemporaryFiles(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	contents := bytes.Repeat([]byte{0x41}, 1<<20)
	asset := githubReleaseAsset{Name: "asset.zip", Size: int64(len(contents)), Digest: "sha256:" + strings.Repeat("0", 64), BrowserDownloadURL: "https://github.com/o/r/releases/download/v1/f.zip"}
	updater := NewCoreUpdater()
	updater.client = &http.Client{Transport: &githubfetch.Transport{Detect: func(context.Context) githubfetch.Mode { return githubfetch.IPv6Only }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(contents)), Header: make(http.Header), Request: r}, nil
	})}}
	if _, err := updater.downloadAsset(context.Background(), asset); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err=%v", err)
	}
	paths, err := filepath.Glob(filepath.Join(os.TempDir(), "qcontrolhub-release-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("temporary files: %v %v", paths, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := updater.downloadAsset(ctx, asset); err != context.Canceled {
		t.Fatalf("canceled download=%v", err)
	}
}

func TestMetadataRetriesAndRejectsOversizedBodies(t *testing.T) {
	updater := NewCoreUpdater()
	attempts := 0
	updater.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		status := 200
		body := `{"ok":true}`
		if attempts == 1 {
			status = 503
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	var output map[string]any
	if err := updater.getJSON(context.Background(), githubAPIBase+"/repos/o/r/releases/latest", &output); err != nil || attempts != 2 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
	updater.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"padding":"` + strings.Repeat("a", maxReleaseJSONBytes) + `"}`)), Header: make(http.Header)}, nil
	})}
	if err := updater.getJSON(context.Background(), githubAPIBase+"/repos/o/r/releases/latest", &output); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized metadata=%v", err)
	}
}

func TestMetadataQuotaFailureReportsResetWithoutRetry(t *testing.T) {
	updater := NewCoreUpdater()
	attempts := 0
	updater.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 403, Body: http.NoBody, Header: http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1791963634"}}}, nil
	})}
	var output any
	err := updater.getJSON(context.Background(), githubAPIBase+"/repos/o/r/releases/latest", &output)
	if err == nil || !strings.Contains(err.Error(), "rate limit exhausted") || !strings.Contains(err.Error(), "2026-10-14T07:40:34Z") || attempts != 1 {
		t.Fatalf("attempts=%d error=%v", attempts, err)
	}
}

func TestNetworkFailureDoesNotRetryOrChangeInstalledCore(t *testing.T) {
	for _, stage := range []string{"metadata", "asset"} {
		t.Run(stage, func(t *testing.T) {
			directory := t.TempDir()
			binary := filepath.Join(directory, "mihomo")
			previous := []byte("#!/bin/sh\necho existing-core\n")
			if err := os.WriteFile(binary, previous, 0700); err != nil {
				t.Fatal(err)
			}
			asset := githubReleaseAsset{Name: "mihomo-linux-amd64-v1.2.3.gz", Size: 1 << 20, Digest: "sha256:" + strings.Repeat("0", 64), BrowserDownloadURL: "https://github.com/o/r/releases/download/v1.2.3/file.gz"}
			metadata, _ := json.Marshal(githubRelease{TagName: "v1.2.3", Assets: []githubReleaseAsset{asset}})
			calls := 0
			updater := NewCoreUpdater()
			updater.client = &http.Client{Transport: &githubfetch.Transport{Detect: func(context.Context) githubfetch.Mode { return githubfetch.Unknown }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if stage == "asset" && strings.Contains(r.URL.String(), "api.github.com") {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(metadata))}, nil
				}
				return nil, &net.DNSError{Name: r.URL.Hostname(), Err: "i/o timeout", IsTimeout: true}
			})}}
			// No service manager is needed before a candidate is downloaded and
			// verified; any accidental service access fails this regression.
			_, err := updater.Install(context.Background(), core.EngineMihomo, EngineSpec{Binary: binary}, core.CoreVersionStable, "", nil)
			if !githubfetch.Terminal(err) {
				t.Fatalf("network error=%v", err)
			}
			expected := 2
			if stage == "asset" {
				expected = 3
			}
			if calls != expected {
				t.Fatalf("repeated exhausted routes: calls=%d", calls)
			}
			got, readErr := os.ReadFile(binary)
			if readErr != nil || !bytes.Equal(got, previous) {
				t.Fatalf("existing core changed: %v", readErr)
			}
			files, _ := os.ReadDir(directory)
			if len(files) != 1 {
				t.Fatalf("staging left behind: %v", files)
			}
		})
	}
}
