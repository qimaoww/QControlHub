package cnip

import (
	"context"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"testing"
)

func TestFetchRejectsPrivateDestinations(t *testing.T) {
	for _, url := range []string{"https://127.0.0.1/list", "https://[::1]/list", "https://169.254.169.254/latest", "http://example.com/cn.txt", "https://name:pass@example.com/cn.txt", "https://example.com/cn.txt?secret=x"} {
		if _, err := Fetch(context.Background(), core.CNIPSource{URL: url, Format: "txt"}); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
}

func TestDualStackAutoSources(t *testing.T) {
	source := core.CNIPSource{URL: "https://example.com/cn.mmdb", IPv6URL: "https://example.com/cn.mmdb"}
	calls := 0
	got, err := fetchFamilies(context.Background(), source, func(_ context.Context, s core.CNIPSource) ([]string, error) {
		calls++
		if s.Format != "auto" {
			t.Fatal("format not automatic")
		}
		return []string{"1.0.1.0/24", "2400:3200::/32"}, nil
	})
	if err != nil || len(got) != 2 || calls != 1 {
		t.Fatalf("dual stack: %v %v %d", got, err, calls)
	}
	source.IPv6URL = ""
	_, err = fetchFamilies(context.Background(), source, func(_ context.Context, s core.CNIPSource) ([]string, error) {
		if s.URL == DefaultIPv6URL {
			return []string{"2400:3200::/32"}, nil
		}
		return []string{"1.0.1.0/24"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	source.IPv6URL = source.URL
	if _, err := fetchFamilies(context.Background(), source, func(context.Context, core.CNIPSource) ([]string, error) { return []string{"1.0.1.0/24"}, nil }); err == nil {
		t.Fatal("missing IPv6 silently accepted")
	}
}
