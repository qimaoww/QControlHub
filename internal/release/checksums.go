package release

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The installer is a POSIX shell script that runs on a fresh node, so it cannot
// parse the JSON manifest without either a JSON tool or a copy of this package.
// It therefore verifies a second, deliberately boring artifact: a checksum list
// signed with the same release key.
//
// The list is the exact format `sha256sum -c` consumes, and the signature covers
// those raw bytes, so a shell can check it with stock `openssl pkeyutl -rawin`
// and nothing has to reproduce a canonical JSON encoding in shell.
//
// Use ChecksumList to build the signed bytes and VerifyChecksums to check them.
// ChecksumsFile and ChecksumsSignatureFile are the conventional file names.

const (
	// ChecksumsFile is the conventional name of the signed checksum list.
	ChecksumsFile = "SHA256SUMS"
	// ChecksumsSignatureFile is the conventional detached signature name.
	ChecksumsSignatureFile = "SHA256SUMS.sig"
)

// ChecksumsEntry is one verified line of a checksum list.
type ChecksumsEntry struct {
	Path   string
	SHA256 string
}

// ChecksumList renders artifacts as a `sha256sum -c` compatible list.
//
// Paths are emitted without a leading slash: the installer resolves them
// relative to its download root, and a leading slash would make `sha256sum -c`
// look outside that root.
func ChecksumList(artifacts []Artifact) string {
	lines := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		relative := strings.TrimPrefix(artifact.Path, InstallAssetPrefix)
		relative = strings.TrimPrefix(relative, "/")
		// Two spaces is the text-mode separator `sha256sum -c` expects.
		lines = append(lines, artifact.SHA256+"  "+relative)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// SignChecksums signs a checksum list with the release key and returns the list
// and its detached signature, both ready to publish beside the artifacts.
//
// The private key must stay on the release machine: a key that reaches the
// control plane would let whoever took the control plane over sign a
// replacement for every file the installer is about to check.
func SignChecksums(artifacts []Artifact, privateKey ed25519.PrivateKey) (list []byte, signature []byte, err error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, nil, errors.New("release: invalid Ed25519 private key")
	}
	if len(artifacts) == 0 {
		return nil, nil, errors.New("release: refusing to sign an empty artifact list")
	}
	for _, artifact := range artifacts {
		if err := ValidateDigest(artifact.SHA256); err != nil {
			return nil, nil, fmt.Errorf("release: artifact %q: %w", artifact.Path, err)
		}
	}
	list = []byte(ChecksumList(artifacts))
	signature = ed25519.Sign(privateKey, list)
	return list, signature, nil
}

// VerifyChecksums checks a detached signature over a checksum list. It is the
// exact operation the installer performs with `openssl pkeyutl -verify -rawin`.
func VerifyChecksums(list, signature []byte, publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return ErrNoPublicKey
	}
	if len(list) == 0 {
		return errors.New("release: checksum list is empty")
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("release: checksum signature must be %d bytes", ed25519.SignatureSize)
	}
	if !ed25519.Verify(publicKey, list, signature) {
		return errors.New("release: checksum list signature does not match the pinned release key")
	}
	return nil
}

// ParseChecksums reads a checksum list and rejects anything malformed, so a
// caller cannot treat a truncated list as a complete one.
func ParseChecksums(list []byte) ([]ChecksumsEntry, error) {
	text := strings.TrimSpace(string(list))
	if text == "" {
		return nil, errors.New("release: checksum list is empty")
	}
	entries := make([]ChecksumsEntry, 0, 16)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "  ", 2)
		if len(fields) != 2 || strings.TrimSpace(fields[1]) == "" {
			return nil, fmt.Errorf("release: malformed checksum line %q", line)
		}
		digest, path := fields[0], strings.TrimSpace(fields[1])
		if err := ValidateDigest(digest); err != nil {
			return nil, fmt.Errorf("release: checksum line %q: %w", line, err)
		}
		if strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
			return nil, fmt.Errorf("release: checksum path %q must stay relative to the download root", path)
		}
		if _, exists := seen[path]; exists {
			return nil, fmt.Errorf("release: checksum path %q is listed twice", path)
		}
		seen[path] = struct{}{}
		entries = append(entries, ChecksumsEntry{Path: path, SHA256: digest})
	}
	return entries, nil
}

// DecodeChecksumsSignature decodes a detached signature file produced by the
// release tooling. Raw bytes are accepted as well so an operator can publish the
// signature as either a text or a binary file.
func DecodeChecksumsSignature(content []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(content))
	if len(trimmed) == ed25519.SignatureSize {
		return []byte(trimmed), nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(decoded) == ed25519.SignatureSize {
		return decoded, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(trimmed); err == nil && len(decoded) == ed25519.SignatureSize {
		return decoded, nil
	}
	if len(content) == ed25519.SignatureSize {
		return content, nil
	}
	return nil, fmt.Errorf("release: checksum signature must be %d raw bytes or its base64 encoding", ed25519.SignatureSize)
}

// EncodeChecksumsSignature renders a detached signature as base64 text so the
// published file survives text transports.
func EncodeChecksumsSignature(signature []byte) string {
	return base64.StdEncoding.EncodeToString(signature)
}
