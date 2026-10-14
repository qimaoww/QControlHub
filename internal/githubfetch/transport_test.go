package githubfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestChannelRedirectsAndCredentials(t *testing.T) {
	for _, target := range []string{
		"https://release-assets.githubusercontent.com/file?sig=a%2Fb&x=2",
		Prefix + "https://raw.githubusercontent.com/org/repo/ref/f%2File?q=a%2Fb",
		"/https://github.com/org/repo/releases/download/tag/f.zip?x=1",
		"https://untrusted.example/file", "http://github.com/org/repo/file",
		Prefix + "https://untrusted.example/file", Prefix + Prefix + "https://github.com/o/r",
	} {
		t.Run(target, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: &Transport{Detect: func(context.Context) Mode { return IPv6Only }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if !strings.HasPrefix(r.URL.String(), Prefix+"https://") {
					t.Fatalf("unaccelerated request %s", r.URL)
				}
				for _, h := range []string{"Authorization", "Cookie", "X-QControlHub-Enrollment", "Referer", "X-Secret"} {
					if r.Header.Get(h) != "" {
						t.Fatalf("forwarded %s", h)
					}
				}
				if calls == 1 {
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": {target}}, Body: http.NoBody, Request: r}, nil
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("resource")), Request: r}, nil
			})}, CheckRedirect: func(r *http.Request, via []*http.Request) error {
				if !IsGitHub(r.URL) {
					t.Fatalf("caller saw proxy URL instead of original: %s", r.URL)
				}
				return nil
			}}
			r, _ := http.NewRequest("GET", "https://github.com/org/repo/releases/download/tag/f.zip", nil)
			for _, h := range []string{"Authorization", "Cookie", "X-QControlHub-Enrollment", "Referer", "X-Secret"} {
				r.Header.Set(h, "private-value")
			}
			response, err := client.Do(r)
			wantSuccess := !strings.Contains(target, "untrusted") && !strings.HasPrefix(target, "http:") && !strings.HasPrefix(target, Prefix+Prefix)
			if (err == nil) != wantSuccess {
				t.Fatalf("success=%v error=%v", wantSuccess, err)
			}
			if response != nil {
				response.Body.Close()
			}
			if !wantSuccess && calls != 1 {
				t.Fatalf("rejected redirect performed %d requests", calls)
			}
		})
	}
}

func TestDirectNetworksAndControlPlaneStayUnchanged(t *testing.T) {
	for _, mode := range []Mode{IPv4, DualStack, NAT64, Unknown, IPv6Only} {
		for _, raw := range []string{"https://control.example/api/v1/agent-binary", "https://api.github.com/repos/o/r/releases?per_page=5&page=2"} {
			t.Run(fmt.Sprint(mode, raw), func(t *testing.T) {
				transport := &Transport{Detect: func(context.Context) Mode { return mode }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					want := raw
					if mode == IPv6Only && strings.Contains(raw, "api.github.com") {
						want = Prefix + raw
					}
					if r.URL.String() != want {
						t.Fatalf("got %s want %s", r.URL, want)
					}
					if strings.Contains(raw, "control.example") && r.Header.Get("X-QControlHub-Enrollment") != "private-value" {
						t.Fatal("control-plane credentials changed")
					}
					return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
				})}
				r, _ := http.NewRequest("GET", raw, nil)
				r.Header.Set("X-QControlHub-Enrollment", "private-value")
				resp, err := transport.RoundTrip(r)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
			})
		}
	}
}

func TestCallerRedirectPolicyAndHopLimitPreserved(t *testing.T) {
	client := &http.Client{Transport: &Transport{Detect: func(context.Context) Mode { return IPv6Only }, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://github.com/o/r/again"}}, Body: http.NoBody, Request: r}, nil
	})}, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("redirect limit")
		}
		return nil
	}}
	if _, err := client.Get("https://github.com/o/r"); err == nil || !strings.Contains(err.Error(), "redirect limit") {
		t.Fatalf("error=%v", err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Get("https://github.com/o/r")
	if err != nil || resp.StatusCode != 302 {
		t.Fatalf("policy lost: %v", err)
	}
	resp.Body.Close()
}

func TestExplicitEgressProxySkipsDetection(t *testing.T) {
	base := &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return nil, fmt.Errorf("configured proxy error") }}
	tt := &Transport{Base: base, Detect: func(context.Context) Mode { t.Fatal("unexpected detection"); return Unknown }}
	r, _ := http.NewRequest("GET", "https://api.github.com/repos/o/r/releases", nil)
	if _, err := tt.RoundTrip(r); err == nil || err.Error() != "configured proxy error" {
		t.Fatalf("err=%v", err)
	}
}
