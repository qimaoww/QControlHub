package webui

import (
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDisplayEngineVersionLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		eng  core.Engine
		raw  string
		want string
	}{
		{"mihomo stable", core.EngineMihomo, "Mihomo Meta v1.19.29 linux amd64 with go1.26.5", "Mihomo 内核 v1.19.29"},
		{"mihomo dev alpha", core.EngineMihomo, "Mihomo Meta alpha-99ce79c linux amd64 with go1.26.5", "Mihomo 内核 alpha-99ce79c"},
		{"xray", core.EngineXray, "Xray 26.3.27 (Xray, Penetrates Everything.) d2758a0", "Xray 内核 v26.3.27"},
		{"sing-box", core.EngineSingBox, "sing-box version 1.13.16", "sing-box 内核 v1.13.16"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := displayEngineVersion(tc.eng, tc.raw); got != tc.want {
				t.Fatalf("displayEngineVersion(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestEngineVersionExtractsShortReleaseToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		eng  core.Engine
		raw  string
		want string
	}{
		{
			name: "mihomo banner",
			eng:  core.EngineMihomo,
			raw:  "Mihomo Meta v1.19.29 linux amd64 with go1.26.5 Sat Jul 18 12:20:03 UTC 2026",
			want: "v1.19.29",
		},
		{
			name: "xray banner",
			eng:  core.EngineXray,
			raw:  "Xray 26.3.27 (Xray, Penetrates Everything.) d2758a0 (go1.26.1 linux/amd64)",
			want: "v26.3.27",
		},
		{
			name: "sing-box banner",
			eng:  core.EngineSingBox,
			raw:  "sing-box version 1.13.16",
			want: "v1.13.16",
		},
		{
			name: "already v-prefixed short version",
			eng:  core.EngineMihomo,
			raw:  "v1.19.29",
			want: "v1.19.29",
		},
		{
			name: "bare short version gets v prefix",
			eng:  core.EngineXray,
			raw:  "26.7.28",
			want: "v26.7.28",
		},
		{
			name: "empty",
			eng:  core.EngineXray,
			raw:  "",
			want: "",
		},
		{
			name: "unknown marker",
			eng:  core.EngineMihomo,
			raw:  "unknown",
			want: "unknown",
		},
		{
			name: "mihomo dev alpha banner",
			eng:  core.EngineMihomo,
			raw:  "Mihomo Meta alpha-99ce79c linux amd64 with go1.26.5 Wed Aug  5 23:20:13 UTC 2026",
			want: "alpha-99ce79c",
		},
		{
			name: "mihomo dev beta banner",
			eng:  core.EngineMihomo,
			raw:  "Mihomo Meta beta-1234567 linux amd64 with go1.26.5",
			want: "beta-1234567",
		},
		{
			name: "versioned prerelease keeps its suffix",
			eng:  core.EngineMihomo,
			raw:  "Mihomo Meta v1.20.0-alpha-abc123 linux amd64",
			want: "v1.20.0-alpha-abc123",
		},
		{
			name: "no version token falls back to raw",
			eng:  core.EngineSingBox,
			raw:  "未检测到二进制",
			want: "未检测到二进制",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := engineVersion(tc.eng, tc.raw); got != tc.want {
				t.Fatalf("engineVersion(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
