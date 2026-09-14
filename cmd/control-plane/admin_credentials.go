//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

func adminTokenDigestFromEnv() ([32]byte, error) {
	return parseAdminTokenCredential(
		strings.TrimSpace(os.Getenv("QCH_ADMIN_TOKEN")),
		strings.TrimSpace(os.Getenv("QCH_ADMIN_TOKEN_SHA256")),
	)
}

func parseAdminTokenCredential(rawToken, encodedDigest string) ([32]byte, error) {
	var digest [32]byte
	if rawToken == "" && encodedDigest == "" {
		return digest, errors.New("QCH_ADMIN_TOKEN_SHA256 or legacy QCH_ADMIN_TOKEN is required")
	}
	if rawToken != "" && len([]byte(rawToken)) < 32 {
		return digest, errors.New("QCH_ADMIN_TOKEN must contain at least 32 bytes")
	}
	if encodedDigest != "" {
		decoded, err := hex.DecodeString(encodedDigest)
		if err != nil || len(decoded) != len(digest) {
			return digest, errors.New("QCH_ADMIN_TOKEN_SHA256 must be exactly 64 hexadecimal characters")
		}
		copy(digest[:], decoded)
	}
	if rawToken != "" {
		rawDigest := sha256.Sum256([]byte(rawToken))
		if encodedDigest != "" && rawDigest != digest {
			return digest, errors.New("QCH_ADMIN_TOKEN and QCH_ADMIN_TOKEN_SHA256 do not match")
		}
		digest = rawDigest
	}
	return digest, nil
}

func secretFromEnvOrFile(valueKey, fileKey string) (string, error) {
	value := strings.TrimSpace(os.Getenv(valueKey))
	path := strings.TrimSpace(os.Getenv(fileKey))
	if value != "" && path != "" {
		return "", fmt.Errorf("%s and %s cannot be configured together", valueKey, fileKey)
	}
	if path == "" {
		return value, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileKey, err)
	}
	return strings.TrimSpace(string(contents)), nil
}
