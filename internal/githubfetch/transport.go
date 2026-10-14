package githubfetch

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
)

type Transport struct {
	Base   http.RoundTripper
	Detect func(context.Context) Mode
}

// Wrap preserves the client's original redirect policy, dialer (including
// address restrictions), deadlines and TLS settings. Only public GitHub GETs
// are eligible; control-plane URLs never enter this channel.
func Wrap(client *http.Client) *http.Client {
	copy := *client
	copy.Transport = &Transport{Base: client.Transport, Detect: localDetector.Detect}
	return &copy
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if !IsGitHub(req.URL) || (req.Method != http.MethodGet && req.Method != http.MethodHead) {
		return base.RoundTrip(req)
	}
	// A configured egress proxy already supplies connectivity and may carry
	// its own authentication. Keep its routing behavior unchanged.
	if transport, ok := base.(*http.Transport); ok && transport.Proxy != nil {
		proxy, err := transport.Proxy(req)
		if err != nil {
			return nil, err
		}
		if proxy != nil {
			return base.RoundTrip(req)
		}
	}
	detect := t.Detect
	if detect == nil {
		detect = localDetector.Detect
	}
	if detect(req.Context()) != IPv6Only {
		return base.RoundTrip(req)
	}
	accelerated, err := Accelerated(req.URL.String())
	if err != nil {
		return nil, err
	}
	wire := req.Clone(req.Context())
	wire.URL, _ = url.Parse(accelerated)
	wire.Host = ""
	// Never forward enrollment/admin credentials, cookies, custom secrets or
	// a Referer to the third party. The channel fetches public resources only.
	wire.Header = make(http.Header)
	for _, name := range []string{"Accept", "User-Agent", "Range", "If-None-Match", "If-Modified-Since"} {
		for _, value := range req.Header.Values(name) {
			wire.Header.Add(name, value)
		}
	}
	response, err := base.RoundTrip(wire)
	if err != nil {
		slog.Warn("GitHub accelerated request failed", "stage", "request", "host", req.URL.Hostname())
		return nil, fmt.Errorf("IPv6 GitHub channel request failed; check DNS/HTTPS access to v6.gh-proxy.org: %w", err)
	}
	response.Request = req
	if location := response.Header.Get("Location"); location != "" && response.StatusCode >= 300 && response.StatusCode < 400 {
		next, parseErr := wire.URL.Parse(location)
		if parseErr == nil {
			var original string
			original, parseErr = Original(next.String())
			if parseErr == nil {
				response.Header = response.Header.Clone()
				response.Header.Set("Location", original)
			}
		}
		if parseErr != nil {
			response.Body.Close()
			return nil, fmt.Errorf("IPv6 GitHub channel rejected redirect outside public GitHub sources")
		}
	}
	if response.StatusCode >= 400 {
		slog.Warn("GitHub accelerated response failed", "stage", "response", "status", response.StatusCode, "host", req.URL.Hostname())
	}
	return response, nil
}
