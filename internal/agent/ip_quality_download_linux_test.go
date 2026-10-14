//go:build linux

package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/githubfetch"
)

func TestIPQualityDownloadValidation(t *testing.T) {
	for _, scenario := range []string{"ok", "empty", "html", "invalid-utf8", "oversized", "truncated", "http404", "http503-recovery", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			attempts := 0
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				attempts++
				content, status, size := "#!/bin/bash\nexit 0\n", 200, int64(-1)
				switch scenario {
				case "empty":
					content = ""
				case "html":
					content = "<html>Bad gateway</html>"
				case "invalid-utf8":
					content = string([]byte{0xff})
				case "oversized":
					content = strings.Repeat("x", ipQualityDownloadLimit+1)
				case "truncated":
					size = 100
				case "http404":
					status = 404
				case "http503-recovery":
					if attempts == 1 {
						status = 503
					}
				case "canceled":
					cancel()
					return nil, context.Canceled
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(content)), ContentLength: size, Header: make(http.Header), Request: request}, nil
			})}
			content, err := fetchIPQualityContent(ctx, client, ipQualityScriptURL)
			if scenario == "ok" || scenario == "http503-recovery" {
				if err != nil || !strings.HasPrefix(string(content), "#!/bin/bash") {
					t.Fatalf("%q %v", content, err)
				}
			} else if err == nil {
				t.Fatalf("accepted %s", scenario)
			}
			if scenario == "http404" && attempts != 1 {
				t.Fatal("retried permanent failure")
			}
			if scenario == "http503-recovery" && attempts != 2 {
				t.Fatal("did not recover")
			}
			if scenario == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestIPQualityResourceIPv6PolicyAndSources(t *testing.T) {
	for _, source := range []string{"https://github.com/xykt/IPQuality/raw/main/ref/iso3166.json", "https://testingcf.jsdelivr.net/gh/xykt/IPQuality@main/ref/iso3166.json", "https://raw.githubusercontent.com/xykt/IPQuality/main/ref/iso3166.json"} {
		original, err := ipQualityReferenceSource(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []githubfetch.Mode{githubfetch.IPv4, githubfetch.DualStack, githubfetch.IPv6Only, githubfetch.NAT64} {
			wireURL := ""
			client := &http.Client{Transport: &githubfetch.Transport{Detect: func(context.Context) githubfetch.Mode { return mode }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				wireURL = r.URL.String()
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"alpha-2":"XX"}]`)), ContentLength: -1, Header: make(http.Header), Request: r}, nil
			})}}
			if _, err := fetchIPQualityContent(context.Background(), client, original); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(wireURL, githubfetch.Prefix) != (mode == githubfetch.IPv6Only) {
				t.Fatalf("mode %s: %s", mode, wireURL)
			}
		}
	}
	for _, bad := range []string{"http://raw.githubusercontent.com/xykt/IPQuality/main/ref/a", "https://raw.githubusercontent.com/other/repo/main/ref/a", "https://user:secret@raw.githubusercontent.com/xykt/IPQuality/main/ref/a", "https://raw.githubusercontent.com/xykt/IPQuality/main/ref/../ip.sh", "https://raw.githubusercontent.com/xykt/IPQuality/main/ref/a?token=secret"} {
		if _, err := ipQualityReferenceSource(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestIPQualityDownloadRejectsUntrustedRedirect(t *testing.T) {
	client := ipQualityHTTPClient()
	for _, source := range []string{"http://IP.Check.Place", "https://untrusted.test/script", "https://github.com/other/repo/raw/main/a", "https://raw.githubusercontent.com/xykt/IPQuality/main/ip.sh?secret=x"} {
		request, _ := http.NewRequest(http.MethodGet, source, nil)
		if err := client.CheckRedirect(request, []*http.Request{{}}); err == nil {
			t.Fatalf("accepted %s", source)
		}
	}
}
