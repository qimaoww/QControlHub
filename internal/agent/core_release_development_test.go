package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
