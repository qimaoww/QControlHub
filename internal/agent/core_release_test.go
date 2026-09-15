package agent

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSelectCoreReleaseAssetUsesGenericOfficialBuilds(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("0", 64)
	asset := func(repository, name string) githubReleaseAsset {
		return githubReleaseAsset{
			Name: name, Size: 2 << 20, Digest: digest,
			BrowserDownloadURL: "https://github.com/" + repository + "/releases/download/test/" + name,
		}
	}
	tests := []struct {
		name    string
		engine  core.Engine
		arch    string
		libc    string
		release githubRelease
		want    string
	}{
		{
			name: "Mihomo stable keeps default CPU build primary", engine: core.EngineMihomo, arch: "amd64",
			release: githubRelease{TagName: "v1.19.29", Assets: []githubReleaseAsset{
				asset("MetaCubeX/mihomo", "mihomo-linux-amd64-compatible-v1.19.29.gz"),
				asset("MetaCubeX/mihomo", "mihomo-linux-amd64-v1-v1.19.29.gz"),
				asset("MetaCubeX/mihomo", "mihomo-linux-amd64-v1.19.29.gz"),
			}},
			want: "mihomo-linux-amd64-v1.19.29.gz",
		},
		{
			name: "Mihomo development", engine: core.EngineMihomo, arch: "arm64",
			release: githubRelease{TagName: "Prerelease-Alpha", Prerelease: true, Assets: []githubReleaseAsset{
				asset("MetaCubeX/mihomo", "mihomo-linux-arm64-alpha-deadbeef.gz"),
			}},
			want: "mihomo-linux-arm64-alpha-deadbeef.gz",
		},
		{
			name: "Xray amd64", engine: core.EngineXray, arch: "amd64",
			release: githubRelease{TagName: "v26.3.27", Assets: []githubReleaseAsset{
				asset("XTLS/Xray-core", "Xray-linux-64.zip"),
			}},
			want: "Xray-linux-64.zip",
		},
		{
			name: "sing-box beta", engine: core.EngineSingBox, arch: "arm64",
			release: githubRelease{TagName: "v1.14.0-beta.3", Prerelease: true, Assets: []githubReleaseAsset{
				asset("SagerNet/sing-box", "sing-box-1.14.0-beta.3-linux-arm64.tar.gz"),
			}},
			want: "sing-box-1.14.0-beta.3-linux-arm64.tar.gz",
		},
		{
			name: "Shadowsocks Rust amd64", engine: core.EngineShadowsocksRust, arch: "amd64",
			release: githubRelease{TagName: "v1.24.0", Assets: []githubReleaseAsset{
				asset("shadowsocks/shadowsocks-rust", "shadowsocks-v1.24.0.x86_64-unknown-linux-gnu.tar.xz"),
			}},
			want: "shadowsocks-v1.24.0.x86_64-unknown-linux-gnu.tar.xz",
		},
		{
			name: "Shadowsocks Rust Alpine musl", engine: core.EngineShadowsocksRust, arch: "arm64", libc: "musl",
			release: githubRelease{TagName: "v1.24.0", Assets: []githubReleaseAsset{
				asset("shadowsocks/shadowsocks-rust", "shadowsocks-v1.24.0.aarch64-unknown-linux-musl.tar.xz"),
			}},
			want: "shadowsocks-v1.24.0.aarch64-unknown-linux-musl.tar.xz",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := selectCoreReleaseAsset(test.engine, test.arch, test.release, test.libc)
			if err != nil || got.Name != test.want {
				t.Fatalf("selectCoreReleaseAsset() = %q, %v; want %q", got.Name, err, test.want)
			}
		})
	}
}

func TestResolveReleaseChannelsAndExactVersion(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("a", 64)
	assetJSON := `{"name":"Xray-linux-64.zip","browser_download_url":"https://github.com/XTLS/Xray-core/releases/download/test/Xray-linux-64.zip","digest":"` + digest + `","size":2097152}`
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch request.URL.RequestURI() {
		case "/repos/XTLS/Xray-core/releases/latest":
			body = `{"tag_name":"v26.3.27","draft":false,"prerelease":false,"assets":[` + assetJSON + `]}`
		case "/repos/XTLS/Xray-core/releases?per_page=5&page=1":
			body = `[{"tag_name":"v26.7.28","draft":false,"prerelease":true,"assets":[` + assetJSON + `]}]`
		case "/repos/XTLS/Xray-core/releases/tags/v25.6.8":
			body = `{"tag_name":"v25.6.8","draft":false,"prerelease":false,"assets":[` + assetJSON + `]}`
		default:
			t.Fatalf("unexpected release API request %s", request.URL.String())
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})}
	updater := &CoreUpdater{client: client, apiBase: githubAPIBase, goarch: "amd64", trustedURL: trustedCoreReleaseURL}
	for selector, want := range map[string]string{
		core.CoreVersionStable: "v26.3.27", core.CoreVersionDevelopment: "v26.7.28", "25.6.8": "v25.6.8",
	} {
		resolved, err := updater.resolveRelease(context.Background(), core.EngineXray, selector, "")
		if err != nil || resolved.Tag != want {
			t.Errorf("resolveRelease(%q) = %q, %v; want %q", selector, resolved.Tag, err, want)
		}
	}
}

func TestOfficialCoreReleaseMetadataLive(t *testing.T) {
	if os.Getenv("QCH_LIVE_RELEASE_TEST") != "1" {
		t.Skip("QCH_LIVE_RELEASE_TEST is not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	updater := NewCoreUpdater()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		for _, channel := range []string{core.CoreVersionStable, core.CoreVersionDevelopment} {
			release, err := updater.resolveRelease(ctx, engine, channel, "")
			if err != nil {
				t.Errorf("resolveRelease(%s, %s): %v", engine, channel, err)
				continue
			}
			if release.Tag == "" || release.Asset.Name == "" || release.Asset.Digest == "" {
				t.Errorf("resolveRelease(%s, %s) returned incomplete metadata: %+v", engine, channel, release)
			}
		}
	}
	mirrorRelease, err := updater.resolveRelease(ctx, core.EngineMihomo, core.CoreVersionDevelopment, string(core.CoreSourceMirror))
	if err != nil {
		t.Errorf("resolveRelease(mihomo, development, mirror): %v", err)
	} else if mirrorRelease.Repository != "vernesong/mihomo" || mirrorRelease.Asset.Name == "" || mirrorRelease.Asset.Digest == "" {
		t.Errorf("mirror development release returned incomplete metadata: %+v", mirrorRelease)
	}
}

// mihomoFixtureAsset returns a structurally valid GitHub release asset used only
// as a fixed table fixture. Naming follows MetaCubeX/mihomo's Alpha build
// workflow (.github/workflows/build.yml on the Alpha branch): the default
// goamd64 build is emitted as "mihomo-linux-<arch>-<version>.gz" while CPU and
// Go-toolchain variants use "<arch>-<variant>-<version>.gz".
func mihomoFixtureAsset(name string) githubReleaseAsset {
	return githubReleaseAsset{
		Name:               name,
		Size:               2 << 20,
		Digest:             "sha256:" + strings.Repeat("0", 64),
		BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/test/" + name,
	}
}

func TestSelectMihomoLinuxAssetMatchesRealNaming(t *testing.T) {
	t.Parallel()
	// Fixture metadata sources (all verified 2026-08-23):
	//   - stable tag v1.19.30 from MetaCubeX/mihomo releases/tags/v1.19.30
	//   - official development tag Prerelease-Alpha (alpha-f295ba6) published by
	//     MetaCubeX/mihomo
	//   - mirror development tag Prerelease-Alpha (alpha-smart-834a506) published
	//     by the vernesong/mihomo mirror (MetaCubeX fork, Alpha branch)
	tests := []struct {
		name    string
		arch    string
		release githubRelease
		want    string
		wantErr string
	}{
		{
			name: "stable amd64 keeps default official asset primary",
			arch: "amd64",
			release: githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{
				mihomoFixtureAsset("mihomo-linux-amd64-compatible-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1-go120-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1-go123-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v2-go120-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v2-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v3-go123-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v3-v1.19.30.gz"),
			}},
			want: "mihomo-linux-amd64-v1.19.30.gz",
		},
		{
			name: "development amd64 keeps default alpha asset primary",
			arch: "amd64",
			// Asset names are the real vernesong/mihomo Prerelease-Alpha publish
			// set; the matcher keeps the default build primary while excluding
			// explicit v1/v2/v3 and Go-toolchain variants.
			release: githubRelease{TagName: "Prerelease-Alpha", Prerelease: true, Assets: []githubReleaseAsset{
				mihomoFixtureAsset("mihomo-linux-amd64-compatible-alpha-smart-834a506.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1-alpha-smart-834a506.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v2-alpha-smart-834a506.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v3-alpha-smart-834a506.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1-go120-alpha-smart-834a506.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-alpha-smart-834a506.gz"),
			}},
			want: "mihomo-linux-amd64-alpha-smart-834a506.gz",
		},
		{
			name: "official development amd64 keeps default alpha asset primary",
			arch: "amd64",
			release: githubRelease{TagName: "Prerelease-Alpha", Prerelease: true, Assets: []githubReleaseAsset{
				mihomoFixtureAsset("mihomo-linux-amd64-compatible-alpha-f295ba6.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1-alpha-f295ba6.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v2-alpha-f295ba6.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v3-alpha-f295ba6.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1-go120-alpha-f295ba6.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-alpha-f295ba6.gz"),
			}},
			want: "mihomo-linux-amd64-alpha-f295ba6.gz",
		},
		{
			name: "stable arm64 picks default official asset",
			arch: "arm64",
			release: githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{
				mihomoFixtureAsset("mihomo-linux-arm64-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-arm64-v1.19.30.deb"),
			}},
			want: "mihomo-linux-arm64-v1.19.30.gz",
		},
		{
			name: "wrong platform and packaging excluded",
			arch: "amd64",
			release: githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{
				mihomoFixtureAsset("mihomo-linux-386-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-386-softfloat-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-armv7-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-mips-softfloat-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-loong64-abi1-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-windows-amd64-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-darwin-amd64-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-android-amd64-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1.19.30.deb"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1.19.30.pkg.tar.zst"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1-go123-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v2-v1.19.30.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v3-v1.19.30.gz"),
			}},
			wantErr: "no supported Linux amd64 asset",
		},
		{
			name: "multiple generic assets rejected as ambiguous",
			arch: "amd64",
			release: githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{
				mihomoFixtureAsset("mihomo-linux-amd64-first.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-second.gz"),
			}},
			wantErr: "multiple generic Linux assets",
		},
		{
			name: "multiple compatible assets rejected as ambiguous",
			arch: "amd64",
			release: githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{
				mihomoFixtureAsset("mihomo-linux-amd64-compatible-first.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-compatible-second.gz"),
				mihomoFixtureAsset("mihomo-linux-amd64-v1.19.30.gz"),
			}},
			wantErr: "multiple compatible Linux assets",
		},
		{
			name: "missing sha256 digest rejected fail closed",
			arch: "amd64",
			release: githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{
				{Name: "mihomo-linux-amd64-v1.19.30.gz", Size: 2 << 20, Digest: "", BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-linux-amd64-v1.19.30.gz"},
			}},
			wantErr: "missing a valid GitHub SHA-256 digest",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := selectCoreReleaseAsset(core.EngineMihomo, test.arch, test.release)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("selectCoreReleaseAsset() error = %v, want contains %q", err, test.wantErr)
				}
				return
			}
			if err != nil || got.Name != test.want {
				t.Fatalf("selectCoreReleaseAsset() = %q, %v; want %q", got.Name, err, test.want)
			}
		})
	}
}

func TestOfficialCoreRepositorySelectsSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		engine   core.Engine
		selector string
		source   string
		want     string
		wantErr  bool
	}{
		{name: "mihomo development official default", engine: core.EngineMihomo, selector: core.CoreVersionDevelopment, source: "", want: "MetaCubeX/mihomo"},
		{name: "mihomo development explicit official", engine: core.EngineMihomo, selector: core.CoreVersionDevelopment, source: string(core.CoreSourceOfficial), want: "MetaCubeX/mihomo"},
		{name: "mihomo development mirror opt-in", engine: core.EngineMihomo, selector: core.CoreVersionDevelopment, source: string(core.CoreSourceMirror), want: "vernesong/mihomo"},
		{name: "mihomo stable official default", engine: core.EngineMihomo, selector: core.CoreVersionStable, source: "", want: "MetaCubeX/mihomo"},
		{name: "mihomo stable mirror rejected", engine: core.EngineMihomo, selector: core.CoreVersionStable, source: string(core.CoreSourceMirror), wantErr: true},
		{name: "mihomo unknown source rejected", engine: core.EngineMihomo, selector: core.CoreVersionDevelopment, source: "private", wantErr: true},
		{name: "xray development no source", engine: core.EngineXray, selector: core.CoreVersionDevelopment, source: "", want: "XTLS/Xray-core"},
		{name: "xray development mirror rejected", engine: core.EngineXray, selector: core.CoreVersionDevelopment, source: string(core.CoreSourceMirror), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := officialCoreRepository(test.engine, test.selector, test.source)
			if test.wantErr {
				if err == nil {
					t.Fatalf("officialCoreRepository() = %q, nil; want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("officialCoreRepository() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestCoreSourceLabelIsSourceAware(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		engine   core.Engine
		selector string
		source   string
		want     string
	}{
		{name: "mirror labeled third party", engine: core.EngineMihomo, selector: core.CoreVersionDevelopment, source: string(core.CoreSourceMirror), want: "vernesong/mihomo (third-party mirror)"},
		{name: "official default labeled official", engine: core.EngineMihomo, selector: core.CoreVersionDevelopment, source: "", want: "MetaCubeX/mihomo (official)"},
		{name: "explicit official labeled official", engine: core.EngineMihomo, selector: core.CoreVersionDevelopment, source: string(core.CoreSourceOfficial), want: "MetaCubeX/mihomo (official)"},
		{name: "stable has no label", engine: core.EngineMihomo, selector: core.CoreVersionStable, source: string(core.CoreSourceOfficial), want: ""},
		{name: "non-mihomo has no label", engine: core.EngineXray, selector: core.CoreVersionDevelopment, source: string(core.CoreSourceMirror), want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := coreSourceLabel(test.engine, test.selector, test.source); got != test.want {
				t.Fatalf("coreSourceLabel() = %q, want %q", got, test.want)
			}
		})
	}
}
