package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (updater *CoreUpdater) downloadAsset(ctx context.Context, asset githubReleaseAsset) (string, error) {
	if err := validateReleaseAssetMetadata(asset); err != nil {
		return "", err
	}
	parsed, _ := url.Parse(asset.BrowserDownloadURL)
	if updater.trustedURL == nil || !updater.trustedURL(parsed) {
		return "", errors.New("refusing untrusted release download URL")
	}
	attempts := updater.downloadAttempts
	if attempts <= 0 {
		attempts = defaultDownloadAttempts
	}
	if attempts > maxDownloadAttempts {
		attempts = maxDownloadAttempts
	}
	attemptTimeout := updater.downloadAttemptTimeout
	if attemptTimeout <= 0 {
		attemptTimeout = defaultDownloadTimeout
	}
	retryDelay := updater.downloadRetryDelay
	if retryDelay < 0 {
		retryDelay = 0
	} else if retryDelay == 0 {
		retryDelay = defaultDownloadRetryDelay
	}

	var lastErr error
	performedAttempts := 0
	for attempt := 1; attempt <= attempts; attempt++ {
		performedAttempts = attempt
		attemptContext, cancel := context.WithTimeout(ctx, attemptTimeout)
		path, err := updater.downloadAssetOnce(attemptContext, parsed, asset)
		cancel()
		if err == nil {
			return path, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var retryable retryableCoreDownloadError
		if !errors.As(err, &retryable) || attempt == attempts {
			break
		}
		delay := retryDelay * time.Duration(1<<(attempt-1))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	if performedAttempts > 1 {
		return "", fmt.Errorf("release download failed after %d attempts: %w", performedAttempts, lastErr)
	}
	return "", lastErr
}

func (updater *CoreUpdater) downloadAssetOnce(ctx context.Context, parsed *url.URL, asset githubReleaseAsset) (_ string, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "QControlHub-Agent")
	response, err := updater.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", retryableCoreDownloadError{err: ctx.Err()}
		}
		return "", retryableCoreDownloadError{err: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		err := fmt.Errorf("release download returned HTTP %d", response.StatusCode)
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return "", retryableCoreDownloadError{err: err}
		}
		return "", err
	}
	temp, err := os.CreateTemp("", "qcontrolhub-release-*.asset")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer func() {
		temp.Close()
		if err != nil {
			os.Remove(tempPath)
		}
	}()
	if err = temp.Chmod(0o600); err != nil {
		return "", err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(response.Body, maxReleaseAssetSize+1))
	if copyErr != nil {
		err = retryableCoreDownloadError{err: copyErr}
		return "", err
	}
	if written != asset.Size || written > maxReleaseAssetSize {
		err = retryableCoreDownloadError{err: errors.New("downloaded release asset size does not match signed metadata")}
		return "", err
	}
	expected, _ := hex.DecodeString(strings.TrimPrefix(strings.ToLower(asset.Digest), "sha256:"))
	if subtle.ConstantTimeCompare(hasher.Sum(nil), expected) != 1 {
		err = errors.New("downloaded release asset SHA-256 does not match GitHub metadata")
		return "", err
	}
	if err = temp.Sync(); err != nil {
		return "", err
	}
	if err = temp.Close(); err != nil {
		return "", err
	}
	return tempPath, nil
}

func trustedCoreReleaseURL(value *url.URL) bool {
	if value == nil || value.Scheme != "https" || value.User != nil || value.Port() != "" {
		return false
	}
	host := strings.ToLower(value.Hostname())
	return host == "api.github.com" || host == "github.com" || host == "release-assets.githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

func validateCoreInstallDestination(destination string) error {
	if !filepath.IsAbs(destination) || destination == string(filepath.Separator) || filepath.Base(destination) == "." {
		return errors.New("core binary destination must be an absolute file path")
	}
	directory := filepath.Dir(destination)
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("core binary directory is symlinked or writable by group/others")
	}
	if err := validateOwner(info, "core binary directory"); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return validatePrivilegedExecutable(destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func randomCoreTempName(root *os.Root) (string, error) {
	for range 10 {
		suffix, err := randomSuffix(12)
		if err != nil {
			return "", err
		}
		name := ".qcontrolhub-core-" + suffix + ".tmp"
		if _, err := root.Lstat(name); errors.Is(err, os.ErrNotExist) {
			return name, nil
		}
	}
	return "", errors.New("could not allocate a unique core candidate name")
}
