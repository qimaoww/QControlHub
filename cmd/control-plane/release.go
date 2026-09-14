package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/release"
)

const packagedReleaseManifestPath = "/usr/local/lib/qcontrolhub/release/release-manifest.json"

// An explicit path or key is fail-closed. Without either, a native/development
// build may omit the packaged manifest and keep the legacy unsigned behavior.
func loadReleaseManifest(explicitPath, packagedPath, pinnedKey string, binary []byte, version string) ([]byte, error) {
	explicitPath = strings.TrimSpace(explicitPath)
	pinnedKey = strings.TrimSpace(pinnedKey)
	manifestPath := explicitPath
	if manifestPath == "" {
		manifestPath = packagedPath
	}
	data, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) && explicitPath == "" && pinnedKey == "" {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read release manifest %q (required when a path or public key is configured): %w", manifestPath, err)
	}
	manifest, err := release.Parse(data)
	if err != nil {
		return nil, err
	}
	publicKey, err := release.DecodePublicKey(manifest.Signature.PublicKey)
	if pinnedKey != "" {
		publicKey, err = release.LoadPublicKey(pinnedKey)
	}
	if err != nil {
		return nil, fmt.Errorf("load release public key: %w", err)
	}
	// An embedded key checks package consistency only, not publisher identity.
	// Agents always verify with their independently provisioned pinned key.
	if err := manifest.Verify(publicKey); err != nil {
		return nil, err
	}
	if _, err := manifest.VerifyAgentBinary(release.Digest(binary), int64(len(binary)), version); err != nil {
		return nil, err
	}
	slog.Info("serving signed release manifest", "path", manifestPath,
		"release", manifest.Release, "artifacts", len(manifest.Artifacts), "publisher_key_pinned", pinnedKey != "")
	return data, nil
}
