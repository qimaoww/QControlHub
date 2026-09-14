package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/release"
)

func TestLoadReleaseManifest(t *testing.T) {
	binary := []byte("packaged Agent")
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := (release.Manifest{
		Release: "v1.2.3", SignedAt: time.Now().UTC(),
		Artifacts: []release.Artifact{{
			Role: release.RoleAgentBinary, Path: release.AgentBinaryPath, Version: "v1.2.3",
			SHA256: release.Digest(binary), Size: int64(len(binary)),
		}},
	}).Sign(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	packagedPath := filepath.Join(t.TempDir(), "release-manifest.json")
	if err := os.WriteFile(packagedPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	pinned := base64.RawURLEncoding.EncodeToString(publicKey)
	missingPath := filepath.Join(t.TempDir(), "missing-manifest.json")

	for _, test := range []struct {
		name, explicit, packaged, key, version, wantError string
		binary                                            []byte
		wantAbsent                                        bool
	}{
		{name: "automatic packaged manifest", packaged: packagedPath, binary: binary, version: "v1.2.3"},
		{name: "explicit manifest and pin", explicit: packagedPath, packaged: missingPath, key: pinned, binary: binary, version: "v1.2.3"},
		{name: "unsigned development build", packaged: missingPath, binary: binary, version: "dev", wantAbsent: true},
		{name: "explicit missing manifest", explicit: missingPath, packaged: packagedPath, binary: binary, version: "v1.2.3", wantError: "read release manifest"},
		{name: "pin without manifest", packaged: missingPath, key: pinned, binary: binary, version: "v1.2.3", wantError: "read release manifest"},
		{name: "mismatched version", packaged: packagedPath, binary: binary, version: "v9.9.9", wantError: "does not match control plane version"},
		{name: "different packaged bytes", packaged: packagedPath, binary: []byte("different build"), version: "v1.2.3", wantError: "not the signed release artifact"},
		{name: "no packaged Agent", packaged: packagedPath, version: "v1.2.3", wantError: "not the signed release artifact"},
		{name: "wrong pinned key", packaged: packagedPath, key: base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize)), binary: binary, version: "v1.2.3", wantError: "does not match the pinned release key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := loadReleaseManifest(test.explicit, test.packaged, test.key, test.binary, test.version)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.wantAbsent {
				if len(got) != 0 {
					t.Fatal("unsigned build unexpectedly serves a manifest")
				}
			} else if !bytes.Equal(got, encoded) {
				t.Fatal("manifest response differs from the packaged signed bytes")
			}
		})
	}
}
