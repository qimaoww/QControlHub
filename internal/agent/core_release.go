package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (updater *CoreUpdater) resolveRelease(ctx context.Context, engine core.Engine, selector, source string) (resolvedCoreRelease, error) {
	repository, err := officialCoreRepository(engine, selector, source)
	if err != nil {
		return resolvedCoreRelease{}, err
	}
	base := strings.TrimSuffix(updater.apiBase, "/") + "/repos/" + repository + "/releases"
	var release githubRelease
	switch selector {
	case core.CoreVersionStable:
		if err := updater.getJSON(ctx, base+"/latest", &release); err != nil {
			return resolvedCoreRelease{}, fmt.Errorf("resolve latest stable %s release: %w", engine, err)
		}
		if release.Draft || release.Prerelease {
			return resolvedCoreRelease{}, errors.New("latest endpoint returned a non-stable release")
		}
	case core.CoreVersionDevelopment:
		found := false
		var inspected []string
		for page := 1; page <= 10 && !found; page++ {
			var releases []githubRelease
			endpoint := fmt.Sprintf("%s?per_page=5&page=%d", base, page)
			if err := updater.getJSON(ctx, endpoint, &releases); err != nil {
				return resolvedCoreRelease{}, fmt.Errorf("resolve latest development %s release: %w", engine, err)
			}
			for _, candidate := range releases {
				if candidate.Draft || !candidate.Prerelease {
					continue
				}
				inspected = append(inspected, candidate.TagName)
				if _, err := selectCoreReleaseAsset(engine, updater.goarch, candidate); err != nil {
					var missing missingCoreReleaseAssetError
					if errors.As(err, &missing) {
						// A prerelease without a compatible binary (for example
						// Mihomo's toolchain-only Alpha release) is not a usable
						// development build; keep searching so a real prerelease
						// is selected instead of failing on the first match.
						continue
					}
					return resolvedCoreRelease{}, err
				}
				release = candidate
				found = true
				// Stop at the first usable candidate: GitHub returns releases
				// newest-to-oldest, so continuing would let a later (older)
				// candidate overwrite the selected release.
				break
			}
			if len(releases) < 5 {
				break
			}
		}
		if !found {
			if len(inspected) > 0 {
				if engine == core.EngineMihomo {
					return resolvedCoreRelease{}, fmt.Errorf("%s upstream publishes no installable Linux %s development binary (inspected prereleases: %s; expected asset naming: mihomo-linux-%s-<version>.gz or mihomo-linux-%s-<variant>-<version>.gz); the development channel is unavailable until the selected source publishes a build or a different verifiable source is configured", engine, updater.goarch, strings.Join(inspected, ", "), updater.goarch, updater.goarch)
				}
				return resolvedCoreRelease{}, fmt.Errorf("%s upstream publishes no installable Linux %s development binary (inspected prereleases: %s); the development channel is unavailable", engine, updater.goarch, strings.Join(inspected, ", "))
			}
			return resolvedCoreRelease{}, errors.New("所选来源当前没有可用的开发版 prerelease")
		}
	default:
		tag := "v" + selector
		if err := updater.getJSON(ctx, base+"/tags/"+url.PathEscape(tag), &release); err != nil {
			return resolvedCoreRelease{}, fmt.Errorf("resolve exact %s release %s: %w", engine, tag, err)
		}
		if release.Draft || strings.TrimPrefix(release.TagName, "v") != selector {
			return resolvedCoreRelease{}, errors.New("所选来源返回的版本与请求不一致")
		}
	}
	if strings.TrimSpace(release.TagName) == "" || len(release.TagName) > 80 {
		return resolvedCoreRelease{}, errors.New("release has an invalid tag")
	}
	asset, err := selectCoreReleaseAsset(engine, updater.goarch, release, updater.libc)
	if err != nil {
		return resolvedCoreRelease{}, err
	}
	fallback, err := selectMihomoCompatibleFallback(engine, updater.goarch, release, asset)
	if err != nil {
		return resolvedCoreRelease{}, err
	}
	return resolvedCoreRelease{Tag: release.TagName, Asset: asset, FallbackAsset: fallback, Repository: repository}, nil
}

// officialCoreRepository returns the GitHub repository that publishes releases
// for the requested engine, version channel, and explicit source. Mihomo
// development accepts an explicit source: the official MetaCubeX repository
// (default and recommended, also used when source is omitted) or the public
// vernesong/mihomo Alpha mirror (third-party, explicit opt-in). Any other engine
// or stable/custom install must not carry a source, so an inapplicable value is
// rejected fail-closed instead of silently switching repositories.
func officialCoreRepository(engine core.Engine, selector, source string) (string, error) {
	if engine != core.EngineMihomo && source != "" {
		return "", errors.New("core source is not applicable to this engine")
	}
	switch engine {
	case core.EngineMihomo:
		if source != "" && selector != core.CoreVersionDevelopment {
			return "", errors.New("core source is only applicable to Mihomo development installs")
		}
		switch source {
		case string(core.CoreSourceMirror):
			return "vernesong/mihomo", nil
		case "", string(core.CoreSourceOfficial):
			return "MetaCubeX/mihomo", nil
		default:
			return "", fmt.Errorf("unsupported Mihomo core source %q", source)
		}
	case core.EngineXray:
		return "XTLS/Xray-core", nil
	case core.EngineSingBox:
		return "SagerNet/sing-box", nil
	case core.EngineShadowsocksRust:
		return "shadowsocks/shadowsocks-rust", nil
	default:
		return "", errors.New("unsupported core repository")
	}
}

// coreSourceLabel returns a user-facing, allowlisted description of a Mihomo
// development source. It is empty for any engine/channel where no user-selectable
// source exists, so callers can omit source attribution rather than mislabel an
// unrelated repository as official.
func coreSourceLabel(engine core.Engine, selector, source string) string {
	if engine != core.EngineMihomo || selector != core.CoreVersionDevelopment {
		return ""
	}
	if source == string(core.CoreSourceMirror) {
		return "vernesong/mihomo (third-party mirror)"
	}
	return "MetaCubeX/mihomo (official)"
}

type missingCoreReleaseAssetError struct {
	engine core.Engine
	arch   string
	asset  string
}

func (err missingCoreReleaseAssetError) Error() string {
	if err.asset == "" {
		return fmt.Sprintf("%s release has no supported Linux %s asset", err.engine, err.arch)
	}
	return fmt.Sprintf("%s release does not contain expected asset %s", err.engine, err.asset)
}

// matchMihomoLinuxAsset returns the single default Linux binary for the
// requested Mihomo architecture. Official amd64 releases currently publish an
// unqualified performance build and a separate compatible build. The default
// remains primary; compatible is selected separately as a verification-only
// fallback. A non-nil error is returned for ambiguous or invalid releases and
// must be treated as fail-closed.
func matchMihomoLinuxAsset(arch string, release githubRelease) (githubReleaseAsset, bool, error) {
	prefix := "mihomo-linux-" + arch + "-"
	var compatible, generic githubReleaseAsset
	for _, asset := range release.Assets {
		if !strings.HasPrefix(asset.Name, prefix) || !strings.HasSuffix(asset.Name, ".gz") {
			continue
		}
		variant := strings.TrimSuffix(strings.TrimPrefix(asset.Name, prefix), ".gz")
		if arch == "amd64" && strings.HasPrefix(variant, "compatible-") {
			if err := validateReleaseAssetMetadata(asset); err != nil {
				return githubReleaseAsset{}, false, err
			}
			if compatible.Name != "" {
				return githubReleaseAsset{}, false, errors.New("Mihomo release contains multiple compatible Linux assets")
			}
			compatible = asset
			continue
		}
		if strings.HasPrefix(variant, "v1-") || strings.HasPrefix(variant, "v2-") || strings.HasPrefix(variant, "v3-") {
			continue
		}
		if strings.Contains(variant, "go1") {
			continue
		}
		if err := validateReleaseAssetMetadata(asset); err != nil {
			return githubReleaseAsset{}, false, err
		}
		if generic.Name != "" {
			return githubReleaseAsset{}, false, errors.New("Mihomo release contains multiple generic Linux assets")
		}
		generic = asset
	}
	if generic.Name != "" {
		return generic, true, nil
	}
	if compatible.Name == "" {
		return githubReleaseAsset{}, false, nil
	}
	return compatible, true, nil
}

func selectMihomoCompatibleFallback(engine core.Engine, arch string, release githubRelease, primary githubReleaseAsset) (githubReleaseAsset, error) {
	if engine != core.EngineMihomo || arch != "amd64" || strings.Contains(primary.Name, "-compatible-") {
		return githubReleaseAsset{}, nil
	}
	prefix := "mihomo-linux-amd64-compatible-"
	var fallback githubReleaseAsset
	for _, asset := range release.Assets {
		if !strings.HasPrefix(asset.Name, prefix) || !strings.HasSuffix(asset.Name, ".gz") {
			continue
		}
		if err := validateReleaseAssetMetadata(asset); err != nil {
			return githubReleaseAsset{}, err
		}
		if fallback.Name != "" {
			return githubReleaseAsset{}, errors.New("Mihomo release contains multiple compatible Linux assets")
		}
		fallback = asset
	}
	return fallback, nil
}

func selectCoreReleaseAsset(engine core.Engine, arch string, release githubRelease, libcValues ...string) (githubReleaseAsset, error) {
	libc := "gnu"
	if len(libcValues) > 0 && strings.TrimSpace(libcValues[0]) != "" {
		libc = strings.ToLower(strings.TrimSpace(libcValues[0]))
	}
	if engine == core.EngineMihomo {
		asset, found, err := matchMihomoLinuxAsset(arch, release)
		if err != nil {
			return githubReleaseAsset{}, err
		}
		if !found {
			return githubReleaseAsset{}, missingCoreReleaseAssetError{engine: engine, arch: arch}
		}
		return asset, nil
	}
	wanted := ""
	switch engine {
	case core.EngineXray:
		switch arch {
		case "amd64":
			wanted = "Xray-linux-64.zip"
		case "arm64":
			wanted = "Xray-linux-arm64-v8a.zip"
		case "386":
			wanted = "Xray-linux-32.zip"
		}
	case core.EngineSingBox:
		version := strings.TrimPrefix(release.TagName, "v")
		wanted = "sing-box-" + version + "-linux-" + arch + ".tar.gz"
	case core.EngineShadowsocksRust:
		targets := map[string]map[string]string{
			"gnu":  {"amd64": "x86_64-unknown-linux-gnu", "arm64": "aarch64-unknown-linux-gnu"},
			"musl": {"amd64": "x86_64-unknown-linux-musl", "arm64": "aarch64-unknown-linux-musl"},
		}
		target := targets[libc][arch]
		if target != "" {
			version := strings.TrimPrefix(release.TagName, "v")
			wanted = "shadowsocks-v" + version + "." + target + ".tar.xz"
		}
	}
	if wanted == "" {
		return githubReleaseAsset{}, missingCoreReleaseAssetError{engine: engine, arch: arch}
	}
	for _, asset := range release.Assets {
		if asset.Name == wanted {
			if err := validateReleaseAssetMetadata(asset); err != nil {
				return githubReleaseAsset{}, err
			}
			return asset, nil
		}
	}
	return githubReleaseAsset{}, missingCoreReleaseAssetError{engine: engine, arch: arch, asset: wanted}
}

func detectLinuxLibc() string {
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		return "musl"
	}
	return "gnu"
}

func validateReleaseAssetMetadata(asset githubReleaseAsset) error {
	if asset.Size < 1<<20 || asset.Size > maxReleaseAssetSize {
		return errors.New("release asset size is outside the accepted range")
	}
	digest := strings.TrimPrefix(strings.ToLower(asset.Digest), "sha256:")
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size || !strings.HasPrefix(strings.ToLower(asset.Digest), "sha256:") {
		return errors.New("release asset is missing a valid GitHub SHA-256 digest")
	}
	parsed, err := url.Parse(asset.BrowserDownloadURL)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !trustedCoreReleaseURL(parsed) {
		return errors.New("release asset has an untrusted download URL")
	}
	return nil
}

func (updater *CoreUpdater) getJSON(ctx context.Context, endpoint string, output any) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || updater.trustedURL == nil || !updater.trustedURL(parsed) {
		return errors.New("refusing untrusted release API URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "QControlHub-Agent")
	response, err := updater.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxReleaseJSONBytes+1))
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("release API returned multiple JSON values")
		}
		return err
	}
	return nil
}
