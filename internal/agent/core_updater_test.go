package agent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/ulikunitz/xz"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

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

func TestDownloadAssetRequiresAndVerifiesGitHubDigest(t *testing.T) {
	t.Parallel()
	contents := bytes.Repeat([]byte{0x5a}, 1<<20)
	digest := sha256.Sum256(contents)
	asset := githubReleaseAsset{
		Name: "Xray-linux-64.zip", Size: int64(len(contents)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/XTLS/Xray-core/releases/download/v1/Xray-linux-64.zip",
	}
	updater := &CoreUpdater{
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(contents)), Header: make(http.Header), Request: request}, nil
		})},
		trustedURL: trustedCoreReleaseURL,
	}
	path, err := updater.downloadAsset(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, contents) {
		t.Fatal("downloaded asset content did not match")
	}

	asset.Digest = "sha256:" + strings.Repeat("0", 64)
	if _, err := updater.downloadAsset(context.Background(), asset); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestDownloadAssetRetriesTruncatedOfficialResponse(t *testing.T) {
	t.Parallel()
	contents := bytes.Repeat([]byte{0x6b}, 1<<20)
	digest := sha256.Sum256(contents)
	asset := githubReleaseAsset{
		Name: "Xray-linux-64.zip", Size: int64(len(contents)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/XTLS/Xray-core/releases/download/v1/Xray-linux-64.zip",
	}
	attempts := 0
	updater := &CoreUpdater{
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			body := io.Reader(bytes.NewReader(contents))
			if attempts == 1 {
				body = io.MultiReader(bytes.NewReader(contents[:len(contents)/2]), errorReader{err: io.ErrUnexpectedEOF})
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body), Header: make(http.Header), Request: request}, nil
		})},
		trustedURL: trustedCoreReleaseURL, downloadAttempts: 2,
		downloadAttemptTimeout: time.Second, downloadRetryDelay: time.Nanosecond,
	}
	path, err := updater.downloadAsset(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if attempts != 2 {
		t.Fatalf("download attempts = %d, want 2", attempts)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, contents) {
		t.Fatal("retried asset content did not match")
	}
}

func TestDownloadAssetDoesNotRetryPermanentHTTPFailure(t *testing.T) {
	t.Parallel()
	contents := bytes.Repeat([]byte{0x7c}, 1<<20)
	digest := sha256.Sum256(contents)
	asset := githubReleaseAsset{
		Name: "Xray-linux-64.zip", Size: int64(len(contents)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/XTLS/Xray-core/releases/download/v1/Xray-linux-64.zip",
	}
	attempts := 0
	updater := &CoreUpdater{
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header), Request: request}, nil
		})},
		trustedURL: trustedCoreReleaseURL, downloadAttempts: 3,
		downloadAttemptTimeout: time.Second, downloadRetryDelay: time.Nanosecond,
	}
	if _, err := updater.downloadAsset(context.Background(), asset); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("permanent download error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("download attempts = %d, want 1", attempts)
	}
}

func TestMihomoInstallFallsBackOnlyAfterDefaultVersionFailure(t *testing.T) {
	defaultContents := mihomoVersionFixture(t, false)
	compatibleContents := mihomoVersionFixture(t, true)
	asset := func(name string, contents []byte) githubReleaseAsset {
		digest := sha256.Sum256(contents)
		return githubReleaseAsset{
			Name: name, Size: int64(len(contents)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
			BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/" + name,
		}
	}
	defaultAsset := asset("mihomo-linux-amd64-v1.19.30.gz", defaultContents)
	compatibleAsset := asset("mihomo-linux-amd64-compatible-v1.19.30.gz", compatibleContents)
	releaseBody, err := json.Marshal(githubRelease{
		TagName: "v1.19.30", Assets: []githubReleaseAsset{compatibleAsset, defaultAsset},
	})
	if err != nil {
		t.Fatal(err)
	}
	downloads := map[string][]byte{defaultAsset.Name: defaultContents, compatibleAsset.Name: compatibleContents}
	requested := make([]string, 0, 2)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "api.github.com" {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(releaseBody)), Header: make(http.Header), Request: request}, nil
		}
		name := filepath.Base(request.URL.Path)
		contents, ok := downloads[name]
		if !ok {
			t.Fatalf("unexpected asset request %s", request.URL.String())
		}
		requested = append(requested, name)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(contents)), Header: make(http.Header), Request: request}, nil
	})}
	updater := &CoreUpdater{
		client: client, apiBase: githubAPIBase, goarch: "amd64", libc: "gnu", trustedURL: trustedCoreReleaseURL,
		downloadAttempts: 1, downloadAttemptTimeout: 10 * time.Second,
	}

	directory := t.TempDir()
	serviceHelper := filepath.Join(directory, "service-helper")
	if err := os.WriteFile(serviceHelper, []byte("#!/bin/sh\ncase \"$1\" in\nis-active) echo active ;;\nrestart) : ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	spec := EngineSpec{Binary: filepath.Join(directory, "mihomo"), Service: "qagent-mihomo.service"}
	manager := &ServiceManager{kind: ServiceManagerSystemd, executable: serviceHelper, enableExecutable: serviceHelper}
	output, err := updater.Install(context.Background(), core.EngineMihomo, spec, core.CoreVersionStable, "", manager)
	if err != nil {
		t.Fatalf("install with compatible fallback: %v\n%s", err, output)
	}
	if len(requested) != 2 || requested[0] != defaultAsset.Name || requested[1] != compatibleAsset.Name {
		t.Fatalf("Mihomo asset requests = %v, want default then compatible", requested)
	}
	if !strings.Contains(output, "fallback: default Mihomo binary failed version verification") || !strings.Contains(output, compatibleAsset.Digest) {
		t.Fatalf("fallback install output = %q", output)
	}
	version, err := run(context.Background(), spec.Binary, "-v")
	if err != nil || !strings.Contains(version, "v1.19.30") {
		t.Fatalf("installed compatible binary version = %q, %v", version, err)
	}
}

func mihomoVersionFixture(t *testing.T, succeeds bool) []byte {
	t.Helper()
	var script bytes.Buffer
	if succeeds {
		script.WriteString("#!/bin/sh\necho 'Mihomo Meta v1.19.30 linux amd64 compatible'\nexit 0\n")
	} else {
		script.WriteString("#!/bin/sh\necho 'unsupported CPU' >&2\nexit 1\n")
	}
	// Keep both the extracted executable and compressed release asset above the
	// production minimum without relying on a compiled fixture.
	script.WriteString(": <<'QCH_PADDING'\n")
	padding := make([]byte, 2<<20)
	if _, err := rand.Read(padding); err != nil {
		t.Fatal(err)
	}
	hex.NewEncoder(&script).Write(padding)
	script.WriteString("\nQCH_PADDING\n")
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(script.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}

func TestExtractCoreBinaryFormats(t *testing.T) {
	t.Parallel()
	contents := bytes.Repeat([]byte("binary"), (1<<20)/6+1)
	tests := []struct {
		name   string
		engine core.Engine
		asset  string
		write  func(*testing.T, string)
	}{
		{"Mihomo gzip", core.EngineMihomo, "mihomo.gz", func(t *testing.T, path string) {
			file, _ := os.Create(path)
			writer := gzip.NewWriter(file)
			_, _ = writer.Write(contents)
			_ = writer.Close()
			_ = file.Close()
		}},
		{"Xray zip", core.EngineXray, "Xray-linux-64.zip", func(t *testing.T, path string) {
			file, _ := os.Create(path)
			writer := zip.NewWriter(file)
			entry, _ := writer.Create("xray")
			_, _ = entry.Write(contents)
			_ = writer.Close()
			_ = file.Close()
		}},
		{"sing-box tar gzip", core.EngineSingBox, "sing-box.tar.gz", func(t *testing.T, path string) {
			file, _ := os.Create(path)
			compressed := gzip.NewWriter(file)
			writer := tar.NewWriter(compressed)
			_ = writer.WriteHeader(&tar.Header{Name: "sing-box-test/sing-box", Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg})
			_, _ = writer.Write(contents)
			_ = writer.Close()
			_ = compressed.Close()
			_ = file.Close()
		}},
		{"Shadowsocks Rust tar xz", core.EngineShadowsocksRust, "shadowsocks.tar.xz", func(t *testing.T, path string) {
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			compressed, err := xz.NewWriter(file)
			if err != nil {
				t.Fatal(err)
			}
			writer := tar.NewWriter(compressed)
			if err := writer.WriteHeader(&tar.Header{Name: "shadowsocks-v1.24.0/ssserver", Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(contents); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := compressed.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			archive := filepath.Join(directory, test.asset)
			test.write(t, archive)
			output, err := os.CreateTemp(directory, "output-")
			if err != nil {
				t.Fatal(err)
			}
			if err := extractCoreBinary(test.engine, test.asset, archive, output); err != nil {
				t.Fatal(err)
			}
			_ = output.Close()
			actual, _ := os.ReadFile(output.Name())
			if !bytes.Equal(actual, contents) {
				t.Fatal("extracted binary did not match")
			}
		})
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

func TestResolveDevelopmentResolvesOfficialAlphaBuild(t *testing.T) {
	t.Parallel()
	// Real MetaCubeX/mihomo releases/tags/Prerelease-Alpha metadata (verified
	// 2026-08-23): the official source publishes the full Alpha build set. The
	// default asset remains primary and compatible is retained as a fallback.
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.RequestURI() != "/repos/MetaCubeX/mihomo/releases?per_page=5&page=1" {
			t.Fatalf("unexpected development request %s", request.URL.String())
		}
		body := `[{"tag_name":"Prerelease-Alpha","draft":false,"prerelease":true,"assets":[
			{"name":"mihomo-linux-amd64-compatible-alpha-f295ba6.gz","browser_download_url":"https://github.com/MetaCubeX/mihomo/releases/download/Prerelease-Alpha/mihomo-linux-amd64-compatible-alpha-f295ba6.gz","digest":"sha256:0adc8e35ffcc18da0132af4c24ca236c51c58e2a97594828a9d7f8f4dc8c5a15","size":18939127},
			{"name":"mihomo-linux-amd64-v1-alpha-f295ba6.gz","browser_download_url":"https://github.com/MetaCubeX/mihomo/releases/download/Prerelease-Alpha/mihomo-linux-amd64-v1-alpha-f295ba6.gz","digest":"sha256:f7d3bf8241aa6ca06eea233af9b1b35b0908b11406b1e716e8acfc1bb94f3c14","size":18939120},
			{"name":"mihomo-linux-amd64-v1-go120-alpha-f295ba6.gz","browser_download_url":"https://github.com/MetaCubeX/mihomo/releases/download/Prerelease-Alpha/mihomo-linux-amd64-v1-go120-alpha-f295ba6.gz","digest":"sha256:c329e1454b268fa625919f4f095ae947b766f823e2129f560f008f188551fa15","size":21547315},
			{"name":"mihomo-linux-amd64-alpha-f295ba6.gz","browser_download_url":"https://github.com/MetaCubeX/mihomo/releases/download/Prerelease-Alpha/mihomo-linux-amd64-alpha-f295ba6.gz","digest":"sha256:1137dafbdc22131d118e8efa3998e9625033c0b88f4f2b8f3f4c9a4df361a0d7","size":18908798},
			{"name":"version.txt","browser_download_url":"https://github.com/MetaCubeX/mihomo/releases/download/Prerelease-Alpha/version.txt","digest":"sha256:3e7117c62df1d52bc50e920175e907010b226c437fc271b20cbe8a51fd244f4a","size":20}
		]}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})}
	updater := &CoreUpdater{client: client, apiBase: githubAPIBase, goarch: "amd64", trustedURL: trustedCoreReleaseURL}
	resolved, err := updater.resolveRelease(context.Background(), core.EngineMihomo, core.CoreVersionDevelopment, "")
	if err != nil {
		t.Fatalf("resolveRelease(development): %v", err)
	}
	if resolved.Tag != "Prerelease-Alpha" || resolved.Asset.Name != "mihomo-linux-amd64-alpha-f295ba6.gz" || resolved.FallbackAsset.Name != "mihomo-linux-amd64-compatible-alpha-f295ba6.gz" {
		t.Fatalf("resolveRelease(development) = primary %s/%s fallback %s", resolved.Tag, resolved.Asset.Name, resolved.FallbackAsset.Name)
	}
}

func mihomoReleaseArrayJSON(t *testing.T, releases ...githubRelease) string {
	t.Helper()
	encoded, err := json.Marshal(releases)
	if err != nil {
		t.Fatalf("marshal release fixtures: %v", err)
	}
	return string(encoded)
}

func TestResolveDevelopmentPicksLatestUsablePrerelease(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("e", 64)
	assetFor := func(tag, name string) githubReleaseAsset {
		return githubReleaseAsset{
			Name: name, Size: 2 << 20, Digest: digest,
			BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/" + tag + "/" + name,
		}
	}
	artifactAssets := func(tag string) []githubReleaseAsset {
		return []githubReleaseAsset{
			{Name: "toolchain.tar.gz", Size: 2 << 20, Digest: digest, BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/" + tag + "/toolchain.tar.gz"},
			{Name: "vendor.tar.gz", Size: 2 << 20, Digest: digest, BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/" + tag + "/vendor.tar.gz"},
		}
	}
	releases := func(tag string, assets ...githubReleaseAsset) githubRelease {
		return githubRelease{TagName: tag, Prerelease: true, Assets: assets}
	}
	tests := []struct {
		name      string
		responses map[string]string
		wantAsset string
		wantErr   string
	}{
		{
			name: "same page two usable picks newest",
			responses: map[string]string{
				"/repos/MetaCubeX/mihomo/releases?per_page=5&page=1": mihomoReleaseArrayJSON(t,
					releases("Prerelease-Current", assetFor("Prerelease-Current", "mihomo-linux-amd64-alpha-current.gz")),
					releases("Prerelease-Old", assetFor("Prerelease-Old", "mihomo-linux-amd64-alpha-old.gz")),
				),
			},
			wantAsset: "mihomo-linux-amd64-alpha-current.gz",
		},
		{
			name: "first unusable then usable on same page",
			responses: map[string]string{
				"/repos/MetaCubeX/mihomo/releases?per_page=5&page=1": mihomoReleaseArrayJSON(t,
					releases("Prerelease-Artifact", artifactAssets("Prerelease-Artifact")...),
					releases("Prerelease-Build", assetFor("Prerelease-Build", "mihomo-linux-amd64-alpha-build.gz")),
				),
			},
			wantAsset: "mihomo-linux-amd64-alpha-build.gz",
		},
		{
			name: "all unusable then next page usable",
			responses: map[string]string{
				"/repos/MetaCubeX/mihomo/releases?per_page=5&page=1": mihomoReleaseArrayJSON(t,
					releases("Prerelease-A1", artifactAssets("Prerelease-A1")...),
					releases("Prerelease-A2", artifactAssets("Prerelease-A2")...),
					releases("Prerelease-A3", artifactAssets("Prerelease-A3")...),
					releases("Prerelease-A4", artifactAssets("Prerelease-A4")...),
					releases("Prerelease-A5", artifactAssets("Prerelease-A5")...),
				),
				"/repos/MetaCubeX/mihomo/releases?per_page=5&page=2": mihomoReleaseArrayJSON(t,
					releases("Prerelease-Build", assetFor("Prerelease-Build", "mihomo-linux-amd64-alpha-build.gz")),
				),
			},
			wantAsset: "mihomo-linux-amd64-alpha-build.gz",
		},
		{
			name: "invalid digest fails closed without falling back",
			responses: map[string]string{
				"/repos/MetaCubeX/mihomo/releases?per_page=5&page=1": mihomoReleaseArrayJSON(t,
					githubRelease{TagName: "Prerelease-Broken", Prerelease: true, Assets: []githubReleaseAsset{
						{Name: "mihomo-linux-amd64-alpha-broken.gz", Size: 2 << 20, Digest: "sha256:bad", BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/Prerelease-Broken/mihomo-linux-amd64-alpha-broken.gz"},
					}},
					releases("Prerelease-Old", assetFor("Prerelease-Old", "mihomo-linux-amd64-alpha-old.gz")),
				),
			},
			wantErr: "SHA-256",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				uri := request.URL.RequestURI()
				body, ok := test.responses[uri]
				if !ok {
					body = `[]`
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
			})}
			updater := &CoreUpdater{client: client, apiBase: githubAPIBase, goarch: "amd64", trustedURL: trustedCoreReleaseURL}
			resolved, err := updater.resolveRelease(context.Background(), core.EngineMihomo, core.CoreVersionDevelopment, "")
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("resolveRelease(development) error = %v, want contains %q", err, test.wantErr)
				}
				return
			}
			if err != nil || resolved.Asset.Name != test.wantAsset {
				t.Fatalf("resolveRelease(development) asset = %q, %v; want %q", resolved.Asset.Name, err, test.wantAsset)
			}
		})
	}
}

func TestResolveDevelopmentResolvesMirrorAlphaBuild(t *testing.T) {
	t.Parallel()
	// Real vernesong/mihomo releases/tags/Prerelease-Alpha metadata (verified
	// 2026-08-23). The mirror (MetaCubeX/mihomo fork, Alpha branch) is an
	// explicit opt-in source for Mihomo development builds.
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.RequestURI() != "/repos/vernesong/mihomo/releases?per_page=5&page=1" {
			t.Fatalf("unexpected development request %s", request.URL.String())
		}
		body := `[{"tag_name":"Prerelease-Alpha","draft":false,"prerelease":true,"assets":[
			{"name":"mihomo-linux-amd64-compatible-alpha-smart-834a506.gz","browser_download_url":"https://github.com/vernesong/mihomo/releases/download/Prerelease-Alpha/mihomo-linux-amd64-compatible-alpha-smart-834a506.gz","digest":"sha256:8c351473514573d4d30dee69cbf993509ca7f5c0d9a018d73617b28eba5ea486","size":19211809},
			{"name":"mihomo-linux-amd64-v1-alpha-smart-834a506.gz","browser_download_url":"https://github.com/vernesong/mihomo/releases/download/Prerelease-Alpha/mihomo-linux-amd64-v1-alpha-smart-834a506.gz","digest":"sha256:513393c7c605b303b1148e2993bbdcb5997bb5ed3d62b3765109d297406a60a4","size":19211801},
			{"name":"mihomo-linux-amd64-v1-go120-alpha-smart-834a506.gz","browser_download_url":"https://github.com/vernesong/mihomo/releases/download/Prerelease-Alpha/mihomo-linux-amd64-v1-go120-alpha-smart-834a506.gz","digest":"sha256:9a49a184c180847fd6994304d609ed9ffa0bfa4cffa7b482b2cc71c689358e8a","size":21879116},
			{"name":"mihomo-linux-amd64-alpha-smart-834a506.gz","browser_download_url":"https://github.com/vernesong/mihomo/releases/download/Prerelease-Alpha/mihomo-linux-amd64-alpha-smart-834a506.gz","digest":"sha256:b0fff583c80045fd55c001d433e8e84fdcf3f35f8cc7ecc9413e3f0ce771a418","size":19182021},
			{"name":"version.txt","browser_download_url":"https://github.com/vernesong/mihomo/releases/download/Prerelease-Alpha/version.txt","digest":"sha256:3e7117c62df1d52bc50e920175e907010b226c437fc271b20cbe8a51fd244f4a","size":20}
		]}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})}
	updater := &CoreUpdater{client: client, apiBase: githubAPIBase, goarch: "amd64", trustedURL: trustedCoreReleaseURL}
	resolved, err := updater.resolveRelease(context.Background(), core.EngineMihomo, core.CoreVersionDevelopment, string(core.CoreSourceMirror))
	if err != nil {
		t.Fatalf("resolveRelease(mirror): %v", err)
	}
	if resolved.Tag != "Prerelease-Alpha" || resolved.Asset.Name != "mihomo-linux-amd64-alpha-smart-834a506.gz" || resolved.FallbackAsset.Name != "mihomo-linux-amd64-compatible-alpha-smart-834a506.gz" {
		t.Fatalf("resolveRelease(mirror) = primary %s/%s fallback %s", resolved.Tag, resolved.Asset.Name, resolved.FallbackAsset.Name)
	}
}

func TestResolveDevelopmentFailsClosedWhenSourceDropsBinaries(t *testing.T) {
	t.Parallel()
	// Defensive regression: if a chosen development source publishes only
	// toolchain/vendor/version artifacts, the channel must fail closed rather
	// than report a usable build or fall back to stable.
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `[{"tag_name":"Prerelease-Alpha","draft":false,"prerelease":true,"assets":[
			{"name":"toolchain.tar.gz","browser_download_url":"https://github.com/vernesong/mihomo/releases/download/Prerelease-Alpha/toolchain.tar.gz","digest":"sha256:29f377ae07dd51290ab15b50a56cad43cbbf909716ba4d25a7f47306ab02d408","size":67734784},
			{"name":"vendor.tar.gz","browser_download_url":"https://github.com/vernesong/mihomo/releases/download/Prerelease-Alpha/vendor.tar.gz","digest":"sha256:a57b6b7f6c16b7e61ba45e45c639bff5405249d30c46fff9b517a3359124c108","size":13867204},
			{"name":"version.txt","browser_download_url":"https://github.com/vernesong/mihomo/releases/download/Prerelease-Alpha/version.txt","digest":"sha256:85be1a68e6b63cee99d4aa2f7b325b633d540b6d591ea761d9a685a2b7a5685f","size":14}
		]}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})}
	updater := &CoreUpdater{client: client, apiBase: githubAPIBase, goarch: "amd64", trustedURL: trustedCoreReleaseURL}
	_, err := updater.resolveRelease(context.Background(), core.EngineMihomo, core.CoreVersionDevelopment, string(core.CoreSourceMirror))
	if err == nil {
		t.Fatal("resolveRelease(development) unexpectedly succeeded with no installable prerelease binary")
	}
	for _, expected := range []string{"Prerelease-Alpha", "mihomo-linux-amd64", "Linux amd64", "no installable", "development binary"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("resolveRelease(development) error %q is missing %q", err, expected)
		}
	}
	if strings.Contains(err.Error(), "official") {
		t.Fatalf("mirror source error must not be mislabeled as official: %q", err)
	}
}

func TestResolveDevelopmentNoPrereleaseDoesNotFallBack(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("d", 64)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `[{"tag_name":"v1.19.30","draft":false,"prerelease":false,"assets":[
			{"name":"mihomo-linux-amd64-v1.19.30.gz","browser_download_url":"https://github.com/vernesong/mihomo/releases/download/v1.19.30/mihomo-linux-amd64-v1.19.30.gz","digest":"` + digest + `","size":2097152}
		]}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})}
	updater := &CoreUpdater{client: client, apiBase: githubAPIBase, goarch: "amd64", trustedURL: trustedCoreReleaseURL}
	if _, err := updater.resolveRelease(context.Background(), core.EngineMihomo, core.CoreVersionDevelopment, string(core.CoreSourceMirror)); err == nil {
		t.Fatal("resolveRelease(development) fell back to stable when no prerelease exists")
	} else if !strings.Contains(err.Error(), "开发版 prerelease") {
		t.Fatalf("resolveRelease(development) error = %q, want no-prerelease message", err)
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
