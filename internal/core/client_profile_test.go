package core

import (
	"strings"
	"testing"
)

func TestClientProfileIdentityAndSSRustNames(t *testing.T) {
	seen := map[string]bool{}
	for _, engine := range []Engine{EngineShadowsocksRust, EngineXray} {
		for _, listen := range []string{"::", "127.0.0.1"} {
			for _, port := range []int{20001, 20002} {
				label := ClientProfileNameLabel(engine, listen, port)
				if seen[label] || !strings.HasPrefix(label, ClientProfileNameLabelPrefix) {
					t.Fatal("endpoints share name storage")
				}
				seen[label] = true
			}
		}
	}
	for _, name := range []string{"香港 · ATT", "ss-rust-1", strings.Repeat("名", 64)} {
		if !ValidSSRustTag(name) {
			t.Fatalf("valid name rejected: %q", name)
		}
	}
	for _, name := range []string{"", " ", " leading", "trailing ", "bad\x00name", "bad\nname", "bad\u0085name", strings.Repeat("名", 65), "\xff"} {
		if ValidSSRustTag(name) {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
}
