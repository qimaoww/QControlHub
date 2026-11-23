package agent

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMihomoSnellMinimumVersion(t *testing.T) {
	for _, version := range []struct {
		value string
		valid bool
	}{
		{"1.19.33", false}, {"1.19.34", true}, {"1.20.0", true}, {"2.0.0", true}, {"1.19.34-alpha", false}, {"unknown", false},
	} {
		t.Run(version.value, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "mihomo")
			writeExecutable(t, binary, "#!/bin/sh\nprintf 'Mihomo Meta v"+version.value+" linux amd64\\n'\n")
			for _, source := range []string{
				"listeners: [{type: snell, version: 6, mode: unshaped}]",
				"proxies: [{type: snell, version: 6, mode: default}]",
				"proxies: [{type: snell, version: '6', mode: default}]",
			} {
				if err := validateMihomoSnellCapabilities(context.Background(), binary, source); (err == nil) != version.valid {
					t.Fatalf("version %s: %v", version.value, err)
				}
			}
			if err := validateMihomoSnellCapabilities(context.Background(), binary, "listeners: [{type: snell, version: 5}]"); err != nil {
				t.Fatal("v5 regression", err)
			}
		})
	}
}

func TestMihomoSnellGateIgnoresUnrelatedVersionFields(t *testing.T) {
	for _, source := range []string{
		"proxies: [{name: direct-extra, type: direct, version: custom-metadata}]\nrules: ['MATCH,DIRECT']",
		"listeners: [{name: socks, type: socks, port: 21080, version: custom-metadata}]",
	} {
		if err := validateMihomoSnellCapabilities(context.Background(), "/does-not-exist", source); err != nil {
			t.Fatalf("unrelated version field triggered Snell validation: %v", err)
		}
	}
}
