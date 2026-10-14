package githubfetch

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestConfiguredProxySuccessAndFailureKeepRouting(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "api.github.com" || r.Header.Get("Authorization") != "origin-credential" {
			t.Errorf("original request altered: host=%s", r.Host)
		}
		io.WriteString(w, "public metadata")
	}))
	defer origin.Close()
	for _, works := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failure"}[works], func(t *testing.T) {
			var connects atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connects.Add(1)
				if r.Method != "CONNECT" || r.Host != "api.github.com:443" || r.Header.Get("Proxy-Authorization") == "" {
					t.Errorf("proxy routing/auth changed: %v", r)
				}
				if !works {
					w.WriteHeader(502)
					return
				}
				upstream, err := net.Dial("tcp", origin.Listener.Addr().String())
				if err != nil {
					t.Error(err)
					return
				}
				client, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					upstream.Close()
					t.Error(err)
					return
				}
				defer client.Close()
				defer upstream.Close()
				io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
				go io.Copy(upstream, client)
				io.Copy(client, upstream)
			}))
			defer proxy.Close()
			proxyURL, _ := url.Parse(proxy.URL)
			proxyURL.User = url.UserPassword("proxy-user", "proxy-password")
			base := origin.Client().Transport.(*http.Transport).Clone()
			base.TLSClientConfig = base.TLSClientConfig.Clone()
			base.TLSClientConfig.ServerName = "example.com"
			base.Proxy = http.ProxyURL(proxyURL)
			defer base.CloseIdleConnections()
			client := &http.Client{Transport: &Transport{Base: base, Detect: func(context.Context) Mode { t.Error("detected despite explicit proxy"); return Unknown }}}
			req, _ := http.NewRequest("GET", "https://api.github.com/repos/o/r/releases/latest", nil)
			req.Header.Set("Authorization", "origin-credential")
			response, err := client.Do(req)
			if works {
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if string(body) != "public metadata" {
					t.Fatalf("body=%s", body)
				}
			} else if err == nil || !strings.Contains(err.Error(), "Bad Gateway") {
				t.Fatalf("proxy error=%v", err)
			}
			if connects.Load() != 1 {
				t.Fatalf("proxy repeated/fell back: connects=%d", connects.Load())
			}
		})
	}
}
