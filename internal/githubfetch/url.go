// Package githubfetch provides a public GitHub download channel for IPv6 hosts.
// Callers retain responsibility for validating the original source and content.
package githubfetch

import (
	"errors"
	"net/url"
	"strings"
)

const Prefix = "https://v6.gh-proxy.org/"

func IsGitHub(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	h := strings.ToLower(u.Host)
	return h == "github.com" || h == "api.github.com" || h == "raw.githubusercontent.com" || strings.HasSuffix(h, ".githubusercontent.com")
}

// Original removes exactly one trusted channel prefix. Nested prefixes and
// arbitrary proxy targets are rejected, never treated as trusted GitHub URLs.
func Original(raw string) (string, error) {
	original := strings.TrimPrefix(raw, Prefix)
	u, err := url.Parse(original)
	if err != nil || !IsGitHub(u) || strings.ContainsAny(original, " \t\r\n\\") {
		return "", errors.New("GitHub channel requires an original public HTTPS GitHub URL")
	}
	return original, nil
}

// Accelerated preserves the original path, escaping and query byte for byte.
func Accelerated(raw string) (string, error) {
	original, err := Original(raw)
	if err != nil {
		return "", err
	}
	return Prefix + original, nil
}
