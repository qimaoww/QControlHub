package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/githubfetch"
)

func TestCaddyReleaseOfficialRecovery(t *testing.T) {
	for _, scenario := range []string{"primary", "recover", "quota", "invalid-json", "untrusted-redirect", "other-repository", "missing-tag", "mismatched-tag", "prerelease", "draft", "tag-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			updater := NewCoreUpdater()
			var paths []string
			updater.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				paths = append(paths, r.Method+" "+r.URL.Host+r.URL.RequestURI())
				status, body := 200, `{"tag_name":"v2.11.9","assets":[{"name":"caddy.tar.gz","digest":"sha256:fixture"}]}`
				header := make(http.Header)
				switch r.URL.Path {
				case "/repos/caddyserver/caddy/releases/latest":
					if scenario != "primary" {
						status = 503
					}
					if scenario == "quota" {
						status = 403
						header.Set("X-RateLimit-Remaining", "0")
					}
					if scenario == "invalid-json" {
						status, body = 200, "invalid"
					}
				case "/caddyserver/caddy/releases/latest":
					status = 302
					location := "https://github.com/caddyserver/caddy/releases/tag/v2.11.9"
					if scenario == "untrusted-redirect" {
						location = "https://untrusted.example/releases/tag/v2.11.9"
					}
					if scenario == "other-repository" {
						location = "https://github.com/other/repository/releases/tag/v2.11.9"
					}
					if scenario == "missing-tag" {
						status = 200
					}
					header.Set("Location", location)
				case "/repos/caddyserver/caddy/releases/tags/v2.11.9":
					if scenario == "mismatched-tag" {
						body = `{"tag_name":"v2.11.8"}`
					}
					if scenario == "prerelease" {
						body = `{"tag_name":"v2.11.9","prerelease":true}`
					}
					if scenario == "draft" {
						body = `{"tag_name":"v2.11.9","draft":true}`
					}
					if scenario == "tag-unavailable" {
						status = 503
					}
				}
				if r.URL.Host == "untrusted.example" {
					t.Fatal("followed untrusted redirect")
				}
				if _, ok := r.Context().Deadline(); !ok {
					t.Fatal("request has no deadline")
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			release, err := updater.caddyRelease(context.Background())
			if scenario == "primary" || scenario == "recover" {
				if err != nil || release.TagName != "v2.11.9" || len(release.Assets) != 1 {
					t.Fatalf("release=%+v error=%v", release, err)
				}
			} else if err == nil {
				t.Fatal("invalid release accepted")
			}
			primary := "GET api.github.com/repos/caddyserver/caddy/releases/latest"
			want := []string{primary}
			if scenario != "primary" && scenario != "quota" && scenario != "invalid-json" {
				want = append(want, primary, primary, "HEAD github.com/caddyserver/caddy/releases/latest")
				if scenario != "untrusted-redirect" && scenario != "missing-tag" {
					path := "/caddyserver/caddy/releases/tag/v2.11.9"
					if scenario == "other-repository" {
						path = "/other/repository/releases/tag/v2.11.9"
					}
					want = append(want, "HEAD github.com"+path)
					if scenario != "other-repository" {
						tag := "GET api.github.com/repos/caddyserver/caddy/releases/tags/v2.11.9"
						want = append(want, tag)
						if scenario == "tag-unavailable" {
							want = append(want, tag, tag)
						}
					}
				}
			}
			if !reflect.DeepEqual(paths, want) {
				t.Fatalf("requests=%v want=%v", paths, want)
			}
		})
	}
}

func TestCaddyReleaseCancellationStopsRecovery(t *testing.T) {
	updater := NewCoreUpdater()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	updater.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		cancel()
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: http.NoBody, Request: r}, nil
	})
	_, err := updater.caddyRelease(ctx)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

func TestCaddyReleaseLiveRecovery(t *testing.T) {
	if os.Getenv("QCH_TEST_LIVE_CADDY_RECOVERY") != "1" {
		t.Skip("requires real official Caddy release downloads")
	}
	updater := NewCoreUpdater()
	transport := updater.client.Transport.(*githubfetch.Transport)
	base := transport.Base
	transport.Base = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.github.com" && r.URL.Path == "/repos/caddyserver/caddy/releases/latest" {
			return &http.Response{StatusCode: 503, Header: make(http.Header), Body: http.NoBody, Request: r}, nil
		}
		return base.RoundTrip(r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	release, err := updater.caddyRelease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := "caddy_" + strings.TrimPrefix(release.TagName, "v") + "_linux_" + updater.goarch + ".tar.gz"
	for _, asset := range release.Assets {
		if asset.Name == name {
			archive, err := updater.downloadAsset(ctx, asset)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(archive)
			t.Logf("recovered %s via official redirect/tag API and verified %s (%s)", release.TagName, name, asset.Digest)
			return
		}
	}
	t.Fatal("official archive missing")
}
