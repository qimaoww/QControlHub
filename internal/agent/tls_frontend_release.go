package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Resolve the official latest redirect when that API route is unavailable. The
// redirect supplies only the tag; asset URLs, size and SHA-256 still come from
// the official tag API and pass the existing installer validation.
func (updater *CoreUpdater) caddyRelease(ctx context.Context) (githubRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	base := strings.TrimSuffix(updater.apiBase, "/") + "/repos/caddyserver/caddy/releases"
	var release githubRelease
	err := updater.getJSON(ctx, base+"/latest", &release)
	if err == nil {
		return release, nil
	}
	if ctx.Err() != nil || !assetRouteUnavailable(err) {
		return githubRelease{}, err
	}
	primaryErr := err
	recoverRelease := func() (githubRelease, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://github.com/caddyserver/caddy/releases/latest", nil)
		if err != nil {
			return githubRelease{}, err
		}
		request.Header.Set("User-Agent", "QControlHub-Agent")
		response, err := updater.client.Do(request)
		if err != nil {
			return githubRelease{}, err
		}
		defer response.Body.Close()
		resolved := response.Request.URL
		const prefix = "/caddyserver/caddy/releases/tag/"
		if response.StatusCode != http.StatusOK || resolved.Scheme != "https" || resolved.Host != "github.com" || resolved.User != nil || resolved.RawQuery != "" || resolved.Fragment != "" || !strings.HasPrefix(resolved.Path, prefix) {
			return githubRelease{}, fmt.Errorf("Caddy latest release redirect did not identify an official tag (HTTP %d)", response.StatusCode)
		}
		tag := strings.TrimPrefix(resolved.Path, prefix)
		if !strings.HasPrefix(tag, "v2.") || len(tag) > 80 || strings.ContainsAny(tag, "/\\\r\n\t ") {
			return githubRelease{}, fmt.Errorf("Caddy latest release redirect returned an invalid tag")
		}
		var recovered githubRelease
		if err := updater.getJSON(ctx, base+"/tags/"+url.PathEscape(tag), &recovered); err != nil {
			return githubRelease{}, err
		}
		if recovered.TagName != tag || recovered.Draft || recovered.Prerelease {
			return githubRelease{}, fmt.Errorf("Caddy tag metadata does not match the official stable release redirect")
		}
		return recovered, nil
	}
	slog.Warn("Caddy latest API route unavailable; resolving official release redirect", "error", primaryErr)
	release, err = recoverRelease()
	if err != nil {
		return githubRelease{}, fmt.Errorf("Caddy latest API unavailable (%v); official tag recovery failed: %w", primaryErr, err)
	}
	return release, nil
}
