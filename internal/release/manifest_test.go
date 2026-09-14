package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey, privateKey
}

func testManifest(t *testing.T) Manifest {
	t.Helper()
	return Manifest{
		ManifestVersion: ManifestVersion,
		Release:         "v1.2.3",
		SignedAt:        time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC),
		Artifacts: []Artifact{
			{
				Role:    RoleAgentBinary,
				Path:    "/api/v1/agent-binary",
				Version: "v1.2.3",
				SHA256:  Digest([]byte("agent-binary-contents")),
				Size:    int64(len("agent-binary-contents")),
			},
			{
				Role:    RoleInstallAsset,
				Path:    InstallAssetPrefix + "deploy/systemd/qagent.service",
				Version: "v1.2.3",
				SHA256:  Digest([]byte("unit-contents")),
				Size:    int64(len("unit-contents")),
			},
		},
	}
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	publicKey, privateKey := testKey(t)
	signed, err := testManifest(t).Sign(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Signature.Algorithm != SignatureAlgorithm {
		t.Fatalf("algorithm = %q", signed.Signature.Algorithm)
	}
	if err := signed.Verify(publicKey); err != nil {
		t.Fatalf("verify signed manifest: %v", err)
	}

	// Serializing and re-parsing must not disturb the signature: the payload is
	// derived from the structure, not from the file bytes.
	encoded, err := signed.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := reparsed.Verify(publicKey); err != nil {
		t.Fatalf("verify reparsed manifest: %v", err)
	}
}

// The control plane is the machine that serves the artifacts, so a manifest it
// could edit must not verify. Every field a tampering control plane would want
// to change is covered here.
func TestVerifyRejectsTampering(t *testing.T) {
	publicKey, privateKey := testKey(t)
	signed, err := testManifest(t).Sign(privateKey)
	if err != nil {
		t.Fatal(err)
	}

	clone := func() Manifest {
		copied := signed
		copied.Artifacts = append([]Artifact(nil), signed.Artifacts...)
		return copied
	}

	t.Run("artifact digest", func(t *testing.T) {
		manifest := clone()
		manifest.Artifacts[0].SHA256 = Digest([]byte("substituted-binary"))
		if err := manifest.Verify(publicKey); err == nil {
			t.Fatal("a rewritten artifact digest verified")
		}
	})
	t.Run("artifact version label", func(t *testing.T) {
		manifest := clone()
		manifest.Artifacts[0].Version = "v9.9.9"
		if err := manifest.Verify(publicKey); err == nil {
			t.Fatal("a rewritten version label verified")
		}
	})
	t.Run("artifact size", func(t *testing.T) {
		manifest := clone()
		manifest.Artifacts[0].Size++
		if err := manifest.Verify(publicKey); err == nil {
			t.Fatal("a rewritten artifact size verified")
		}
	})
	t.Run("added artifact", func(t *testing.T) {
		manifest := clone()
		manifest.Artifacts = append(manifest.Artifacts, Artifact{
			Role: RoleInstallAsset, Path: InstallAssetPrefix + "evil.sh",
			Version: "v1.2.3", SHA256: Digest([]byte("evil")), Size: 4,
		})
		if err := manifest.Verify(publicKey); err == nil {
			t.Fatal("an injected artifact verified")
		}
	})
	t.Run("release identifier", func(t *testing.T) {
		manifest := clone()
		manifest.Release = "v1.2.4"
		if err := manifest.Verify(publicKey); err == nil {
			t.Fatal("a rewritten release identifier verified")
		}
	})
	t.Run("signature bytes", func(t *testing.T) {
		manifest := clone()
		// Flip a real signature byte: replacing the trailing base64 padding
		// characters would be a no-op and make this assertion vacuous.
		raw, err := base64.RawURLEncoding.DecodeString(manifest.Signature.Value)
		if err != nil {
			t.Fatal(err)
		}
		raw[0] ^= 0xff
		manifest.Signature.Value = base64.RawURLEncoding.EncodeToString(raw)
		if err := manifest.Verify(publicKey); err == nil {
			t.Fatal("a corrupted signature verified")
		}
	})
	t.Run("empty signature", func(t *testing.T) {
		manifest := clone()
		manifest.Signature.Value = ""
		if err := manifest.Verify(publicKey); err == nil {
			t.Fatal("a manifest without a signature verified")
		}
	})
	t.Run("algorithm downgrade", func(t *testing.T) {
		manifest := clone()
		manifest.Signature.Algorithm = "none"
		if err := manifest.Verify(publicKey); err == nil {
			t.Fatal("an algorithm downgrade verified")
		}
	})
	t.Run("manifest format version", func(t *testing.T) {
		manifest := clone()
		manifest.ManifestVersion = ManifestVersion + 1
		if err := manifest.Verify(publicKey); err == nil {
			t.Fatal("an unknown manifest version verified")
		}
	})
}

// A manifest must not be able to introduce its own trust root: the embedded
// public key is informational and the pinned key still decides.
func TestVerifyUsesPinnedKeyNotEmbeddedKey(t *testing.T) {
	_, attackerKey := testKey(t)
	pinnedKey, _ := testKey(t)
	forged, err := testManifest(t).Sign(attackerKey)
	if err != nil {
		t.Fatal(err)
	}
	if forged.Signature.PublicKey == "" {
		t.Fatal("signed manifest did not record its public key")
	}
	if err := forged.Verify(pinnedKey); err == nil {
		t.Fatal("a manifest signed by a different key verified against the pinned key")
	}
}

func TestVerifyWithoutPinnedKey(t *testing.T) {
	_, privateKey := testKey(t)
	signed, err := testManifest(t).Sign(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := signed.Verify(nil); err != ErrNoPublicKey {
		t.Fatalf("verify without a pinned key = %v, want ErrNoPublicKey", err)
	}
}

func TestSignRejectsInvalidInput(t *testing.T) {
	_, privateKey := testKey(t)
	for name, mutate := range map[string]func(*Manifest){
		"missing release":  func(m *Manifest) { m.Release = "" },
		"no artifacts":     func(m *Manifest) { m.Artifacts = nil },
		"unknown role":     func(m *Manifest) { m.Artifacts[0].Role = "something-else" },
		"relative path":    func(m *Manifest) { m.Artifacts[0].Path = "api/v1/agent-binary" },
		"duplicate path":   func(m *Manifest) { m.Artifacts[1].Path = m.Artifacts[0].Path },
		"short digest":     func(m *Manifest) { m.Artifacts[0].SHA256 = "abc" },
		"uppercase digest": func(m *Manifest) { m.Artifacts[0].SHA256 = strings.ToUpper(m.Artifacts[0].SHA256) },
		"negative size":    func(m *Manifest) { m.Artifacts[0].Size = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			manifest := testManifest(t)
			mutate(&manifest)
			if _, err := manifest.Sign(privateKey); err == nil {
				t.Fatal("signing accepted an invalid manifest")
			}
		})
	}
}

func TestParseRejectsTrailingDocument(t *testing.T) {
	_, privateKey := testKey(t)
	signed, err := testManifest(t).Sign(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := signed.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(append(encoded, []byte("{}\n")...)); err == nil {
		t.Fatal("parse accepted a second JSON document")
	}
	if _, err := Parse([]byte(`{"manifest_version":1,"unexpected":true}`)); err == nil {
		t.Fatal("parse accepted an unknown field")
	}
}

func TestArtifactLookups(t *testing.T) {
	_, privateKey := testKey(t)
	signed, err := testManifest(t).Sign(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	binary, ok := signed.AgentBinary()
	if !ok || binary.Role != RoleAgentBinary {
		t.Fatalf("AgentBinary() = %+v, %v", binary, ok)
	}
	if _, ok := signed.ArtifactAt("/api/v1/agent-binary"); !ok {
		t.Fatal("ArtifactAt did not resolve the Agent binary")
	}
	// The installer works with relative asset names.
	unit, ok := signed.ArtifactByRelativePath("deploy/systemd/qagent.service")
	if !ok || unit.Role != RoleInstallAsset {
		t.Fatalf("ArtifactByRelativePath() = %+v, %v", unit, ok)
	}
	if _, ok := signed.ArtifactByRelativePath("deploy/systemd/unknown.service"); ok {
		t.Fatal("ArtifactByRelativePath resolved an undeclared asset")
	}
}

func TestLoadFromDisk(t *testing.T) {
	_, privateKey := testKey(t)
	signed, err := testManifest(t).Sign(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := signed.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "release-manifest.json")
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Release != signed.Release || len(loaded.Artifacts) != len(signed.Artifacts) {
		t.Fatalf("loaded manifest = %+v", loaded)
	}
}
