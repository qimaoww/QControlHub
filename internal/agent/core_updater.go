package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	githubAPIBase             = "https://api.github.com"
	maxReleaseJSONBytes       = 2 << 20
	maxReleaseAssetSize       = 128 << 20
	defaultDownloadAttempts   = 3
	maxDownloadAttempts       = 5
	defaultDownloadTimeout    = 90 * time.Second
	defaultDownloadRetryDelay = 250 * time.Millisecond
)

type CoreUpdater struct {
	client                 *http.Client
	apiBase                string
	goarch                 string
	libc                   string
	trustedURL             func(*url.URL) bool
	downloadAttempts       int
	downloadAttemptTimeout time.Duration
	downloadRetryDelay     time.Duration
}

type retryableCoreDownloadError struct {
	err error
}

func (downloadError retryableCoreDownloadError) Error() string {
	return downloadError.err.Error()
}

func (downloadError retryableCoreDownloadError) Unwrap() error {
	return downloadError.err
}

type githubRelease struct {
	TagName    string               `json:"tag_name"`
	Draft      bool                 `json:"draft"`
	Prerelease bool                 `json:"prerelease"`
	Assets     []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}

type resolvedCoreRelease struct {
	Tag           string
	Asset         githubReleaseAsset
	FallbackAsset githubReleaseAsset
	Repository    string
}

func NewCoreUpdater() *CoreUpdater {
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			DisableCompression:    true,
			ResponseHeaderTimeout: 20 * time.Second,
		},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many release download redirects")
			}
			if !trustedCoreReleaseURL(request.URL) {
				return fmt.Errorf("release download redirected to untrusted URL %q", request.URL.Redacted())
			}
			return nil
		},
	}
	return &CoreUpdater{
		client: client, apiBase: githubAPIBase, goarch: runtime.GOARCH, libc: detectLinuxLibc(),
		trustedURL: trustedCoreReleaseURL, downloadAttempts: defaultDownloadAttempts,
		downloadAttemptTimeout: defaultDownloadTimeout, downloadRetryDelay: defaultDownloadRetryDelay,
	}
}

func (updater *CoreUpdater) Install(ctx context.Context, engine core.Engine, spec EngineSpec, selector, source string, manager *ServiceManager) (string, error) {
	if updater == nil {
		return "", errors.New("core updater is required")
	}
	selector, err := core.NormalizeCoreVersionSelector(selector)
	if err != nil {
		return "", err
	}
	source, err = core.NormalizeCoreSource(engine, selector, source)
	if err != nil {
		return "", err
	}
	if !engine.Valid() {
		return "", errors.New("unsupported core engine")
	}
	if updater.goarch != "amd64" && updater.goarch != "arm64" && !(engine == core.EngineXray && updater.goarch == "386") {
		return "", fmt.Errorf("%s release installer does not support agent architecture %s", engine, updater.goarch)
	}
	if err := validateCoreInstallDestination(spec.Binary); err != nil {
		return "", fmt.Errorf("unsafe core install destination: %w", err)
	}

	release, err := updater.resolveRelease(ctx, engine, selector, source)
	if err != nil {
		if label := coreSourceLabel(engine, selector, source); label != "" {
			return "", fmt.Errorf("resolve %s %s from %s: %w", engine, selector, label, err)
		}
		return "", err
	}
	directory := filepath.Dir(spec.Binary)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", fmt.Errorf("open core install directory: %w", err)
	}
	defer root.Close()
	selectedAsset := release.Asset
	tempName, xrayAssets, versionOutput, verificationFailed, err := updater.stageCoreCandidate(ctx, engine, root, directory, release, selectedAsset)
	fallbackReason := ""
	if err != nil && verificationFailed && release.FallbackAsset.Name != "" {
		primaryErr := err
		selectedAsset = release.FallbackAsset
		tempName, xrayAssets, versionOutput, _, err = updater.stageCoreCandidate(ctx, engine, root, directory, release, selectedAsset)
		if err != nil {
			return "", fmt.Errorf("default Mihomo binary failed version verification (%v); compatible fallback also failed: %w", primaryErr, err)
		}
		fallbackReason = "default Mihomo binary failed version verification; installed compatible CPU fallback"
	}
	if err != nil {
		return "", err
	}
	defer root.Remove(tempName)
	for _, tempAsset := range xrayAssets {
		defer root.Remove(tempAsset)
	}
	backup, err := replaceCoreBinary(root, filepath.Base(spec.Binary), tempName)
	if err != nil {
		return "", err
	}
	assetBackups := make(map[string]string, len(xrayAssets))
	for _, name := range xrayMigrationAssetNames {
		tempAsset, exists := xrayAssets[name]
		if !exists {
			continue
		}
		assetBackup, assetErr := replaceCoreAsset(root, name, tempAsset)
		if assetErr != nil {
			for installedName, installedBackup := range assetBackups {
				_, _ = rollbackCoreBinary(root, installedName, installedBackup)
			}
			_, _ = rollbackCoreBinary(root, filepath.Base(spec.Binary), backup)
			return "", fmt.Errorf("install Xray resource %s: %w", name, assetErr)
		}
		assetBackups[name] = assetBackup
	}

	restartOutput, restartErr := serviceCommandAndVerifyWithManager(ctx, manager, spec.Service, core.ActionRestart)
	output := fmt.Sprintf("installed %s release %s\nverified asset SHA-256: %s\nbinary: %s", engine, release.Tag, selectedAsset.Digest, spec.Binary)
	if fallbackReason != "" {
		output += "\nfallback: " + fallbackReason
	}
	if label := coreSourceLabel(engine, selector, source); label != "" {
		output += "\nsource: " + label
	}
	if versionOutput != "" {
		output += "\nversion: " + versionOutput
	}
	if restartOutput != "" {
		output += "\n" + restartOutput
	}
	if restartErr == nil {
		return output, nil
	}
	for name, assetBackup := range assetBackups {
		if _, assetRollbackErr := rollbackCoreBinary(root, name, assetBackup); assetRollbackErr != nil {
			return output, fmt.Errorf("core restart failed (%v); roll back Xray resource %s: %w", restartErr, name, assetRollbackErr)
		}
	}
	rollbackOutput, rollbackErr := rollbackCoreBinary(root, filepath.Base(spec.Binary), backup)
	if rollbackOutput != "" {
		output += "\n" + rollbackOutput
	}
	if rollbackErr != nil {
		return output, fmt.Errorf("core installed but service restart failed (%v); binary rollback also failed: %w", restartErr, rollbackErr)
	}
	if backup == "" {
		// A first-install service commonly uses automatic respawn. Once the new
		// binary is removed, its supervisor can otherwise keep retrying a
		// now-missing executable even though the installation was rolled back.
		stopOutput, stopErr := stopServiceAfterFirstInstallRollback(spec.Service, manager)
		if stopOutput != "" {
			output += "\nrollback stop: " + stopOutput
		}
		if stopErr != nil {
			return output, fmt.Errorf("new core restart failed (%v); binary was removed but the failed service could not be stopped: %w", restartErr, stopErr)
		}
	}
	if backup != "" {
		recoveryContext, recoveryCancel := context.WithTimeout(context.Background(), 30*time.Second)
		recoveryOutput, recoveryErr := serviceCommandAndVerifyWithManager(recoveryContext, manager, spec.Service, core.ActionRestart)
		recoveryCancel()
		if recoveryOutput != "" {
			output += "\nrollback restart: " + recoveryOutput
		}
		if recoveryErr != nil {
			return output, fmt.Errorf("new core restart failed (%v); previous binary restored but recovery failed: %w", restartErr, recoveryErr)
		}
	}
	return output, fmt.Errorf("new core restart failed and the binary change was rolled back: %w", restartErr)
}

func (updater *CoreUpdater) stageCoreCandidate(ctx context.Context, engine core.Engine, root *os.Root, directory string, release resolvedCoreRelease, asset githubReleaseAsset) (tempName string, xrayAssets map[string]string, versionOutput string, verificationFailed bool, err error) {
	downloadPath, err := updater.downloadAsset(ctx, asset)
	if err != nil {
		return "", nil, "", false, fmt.Errorf("download %s from %s: %w", asset.Name, release.Repository, err)
	}
	defer os.Remove(downloadPath)

	tempName, err = randomCoreTempName(root)
	if err != nil {
		return "", nil, "", false, err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = root.Remove(tempName)
		}
	}()
	temp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", nil, "", false, fmt.Errorf("create core install candidate: %w", err)
	}
	if err := extractCoreBinary(engine, asset.Name, downloadPath, temp); err != nil {
		temp.Close()
		return "", nil, "", false, err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return "", nil, "", false, err
	}
	if err := temp.Close(); err != nil {
		return "", nil, "", false, err
	}
	if engine == core.EngineXray {
		xrayAssets, err = stageXrayReleaseAssets(root, asset.Name, downloadPath)
		if err != nil {
			return "", nil, "", false, err
		}
		defer func() {
			if !succeeded {
				for _, name := range xrayAssets {
					_ = root.Remove(name)
				}
			}
		}()
	}
	versionOutput, err = verifyCoreCandidate(ctx, engine, filepath.Join(directory, tempName), release.Tag)
	if err != nil {
		return "", nil, versionOutput, true, err
	}
	succeeded = true
	return tempName, xrayAssets, versionOutput, false, nil
}

func stopServiceAfterFirstInstallRollback(service string, managers ...*ServiceManager) (string, error) {
	stopContext, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopCancel()
	manager := defaultSystemdServiceManager()
	if len(managers) > 0 && managers[0] != nil {
		manager = managers[0]
	}
	return serviceCommandAndVerifyWithManager(stopContext, manager, service, core.ActionStop)
}
