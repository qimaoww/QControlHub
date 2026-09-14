package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/release"
)

// upgradeReleaseFixture builds a signed manifest for the given binary contents
// and an Agent client pinned to the matching public key.
func upgradeReleaseFixture(t *testing.T, binary []byte, version string) (release.Manifest, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := release.Manifest{
		ManifestVersion: release.ManifestVersion,
		Release:         "v1.2.3",
		SignedAt:        time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC),
		Artifacts: []release.Artifact{{
			Role:    release.RoleAgentBinary,
			Path:    "/api/v1/agent-binary",
			Version: version,
			SHA256:  release.Digest(binary),
			Size:    int64(len(binary)),
		}},
	}
	signed, err := manifest.Sign(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signed, publicKey, privateKey
}

// releaseServer serves a manifest body, or 404 when body is nil.
func releaseServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != releaseManifestPath {
			http.NotFound(w, request)
			return
		}
		if body == nil {
			http.NotFound(w, request)
			return
		}
		_, _ = w.Write(body)
	}))
}

func upgradeReleaseClient(t *testing.T, serverURL, pinnedKey, controlPlaneVersion string) *Client {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ClientConfig{
		ServerURL: serverURL, StatePath: t.TempDir() + "/state.json",
		HeartbeatEvery: 30 * time.Second, MetricsEvery: 30 * time.Second,
		ReleasePublicKey: pinnedKey,
	}, testClientExecutor(t))
	if err != nil {
		t.Fatalf("new Client: %v", err)
	}
	// The manifest request is signed, so the client needs usable credentials.
	client.creds.AgentID = "agt_0123456789abcdef"
	client.creds.PrivateKey = authn.EncodePrivateKey(privateKey)
	// The panel version arrives in the authenticated agent policy message.
	client.controlPlaneVersion = controlPlaneVersion
	return client
}

// A node that never received a pinned key keeps working: verification is skipped
// rather than blocking every upgrade on an existing fleet.
func TestUpgradeReleaseSkipsWithoutPinnedKey(t *testing.T) {
	server := releaseServer(t, nil) // no manifest is served at all
	defer server.Close()
	client := upgradeReleaseClient(t, server.URL, "", "v1.2.3")

	version, err := client.verifyUpgradeRelease(context.Background(), server.Client(), []byte("whatever"), 0, client.controlPlaneVersion)
	if err != nil {
		t.Fatalf("upgrade without a pinned key failed: %v", err)
	}
	if version != "" {
		t.Fatalf("version = %q, want empty when verification is skipped", version)
	}
}

func TestUpgradeReleaseAcceptsSignedBinary(t *testing.T) {
	binary := []byte("agent-binary-payload")
	manifest, publicKey, _ := upgradeReleaseFixture(t, binary, "v1.2.3")
	body, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	server := releaseServer(t, body)
	defer server.Close()
	client := upgradeReleaseClient(t, server.URL, base64.RawURLEncoding.EncodeToString(publicKey), "v1.2.3")

	version, err := client.verifyUpgradeRelease(context.Background(), server.Client(), release.DigestBytes(binary), int64(len(binary)), client.controlPlaneVersion)
	if err != nil {
		t.Fatalf("signed release was rejected: %v", err)
	}
	// The version must come from the signed manifest, not from a header the
	// control plane chooses.
	if version != "v1.2.3" {
		t.Fatalf("version = %q, want v1.2.3 from the signed manifest", version)
	}
}

func TestUpgradeReleaseAcceptsInstallerPEMPath(t *testing.T) {
	binary := []byte("agent-binary-payload")
	manifest, publicKey, _ := upgradeReleaseFixture(t, binary, "v1.2.3")
	body, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "release-key.pub.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	server := releaseServer(t, body)
	defer server.Close()
	client := upgradeReleaseClient(t, server.URL, keyPath, "v1.2.3")
	version, err := client.verifyUpgradeRelease(context.Background(), server.Client(), release.DigestBytes(binary), int64(len(binary)), "v1.2.3")
	if err != nil || version != "v1.2.3" {
		t.Fatalf("installer-provisioned PEM key did not verify the upgrade: %q %v", version, err)
	}
}

func TestUpgradeReleaseDownloadVersionSource(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		name := "legacy-header"
		if pinned {
			name = "signed-manifest"
		}
		t.Run(name, func(t *testing.T) {
			binary := []byte("agent-binary-payload")
			manifest, publicKey, _ := upgradeReleaseFixture(t, binary, "v1.2.3")
			body, err := manifest.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/agent/v1/binary":
					w.Header().Set("X-QControlHub-Agent-SHA256", release.Digest(binary))
					w.Header().Set("X-QControlHub-Agent-Version", "header-version")
					_, _ = w.Write(binary)
				case releaseManifestPath:
					if !pinned {
						t.Error("legacy Agent unexpectedly required a manifest")
					}
					_, _ = w.Write(body)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			key, expected := "", "header-version"
			if pinned {
				key, expected = base64.RawURLEncoding.EncodeToString(publicKey), "v1.2.3"
			}
			client := upgradeReleaseClient(t, server.URL, key, "v1.2.3")
			_, version, _, err := client.downloadAgentBinaryOnce(context.Background(), server.Client(), t.TempDir())
			if err != nil || version != expected {
				t.Fatalf("wrong preflight version source: got %q, want %q, error %v", version, expected, err)
			}
		})
	}
}

// A control plane that serves a different binary than the one it signed cannot
// pass: this is the substitution the upgrade path exists to stop.
func TestUpgradeReleaseRejectsSubstitutedBinary(t *testing.T) {
	signedBinary := []byte("the-real-release-binary")
	manifest, publicKey, _ := upgradeReleaseFixture(t, signedBinary, "v1.2.3")
	body, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	server := releaseServer(t, body)
	defer server.Close()
	client := upgradeReleaseClient(t, server.URL, base64.RawURLEncoding.EncodeToString(publicKey), "v1.2.3")

	// The attacker-controlled control plane handed us a different payload.
	substituted := []byte("a-substituted-backdoor-binary")
	if _, err := client.verifyUpgradeRelease(context.Background(), server.Client(), release.DigestBytes(substituted), int64(len(substituted)), client.controlPlaneVersion); err == nil {
		t.Fatal("a substituted binary passed release verification")
	}
}

// A pinned key with no servable manifest must fail closed, otherwise removing
// the endpoint would silently disable verification.
func TestUpgradeReleaseFailsClosedWithoutManifest(t *testing.T) {
	server := releaseServer(t, nil)
	defer server.Close()
	_, publicKey, _ := upgradeReleaseFixture(t, []byte("binary"), "v1.2.3")
	client := upgradeReleaseClient(t, server.URL, base64.RawURLEncoding.EncodeToString(publicKey), "v1.2.3")

	if _, err := client.verifyUpgradeRelease(context.Background(), server.Client(), release.DigestBytes([]byte("binary")), 6, client.controlPlaneVersion); err == nil {
		t.Fatal("a pinned key with no manifest did not fail closed")
	} else if !strings.Contains(err.Error(), "QCH_RELEASE_PUBLIC_KEY") {
		t.Fatalf("error does not explain the misconfiguration: %v", err)
	}
}

// A manifest signed by a different key must not be accepted, even though it is a
// well-formed manifest describing the served binary.
func TestUpgradeReleaseRejectsManifestFromAnotherKey(t *testing.T) {
	binary := []byte("agent-binary-payload")
	manifest, _, _ := upgradeReleaseFixture(t, binary, "v1.2.3")
	body, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	server := releaseServer(t, body)
	defer server.Close()
	_, pinnedKey, _ := upgradeReleaseFixture(t, []byte("unrelated"), "v9.9.9")
	client := upgradeReleaseClient(t, server.URL, base64.RawURLEncoding.EncodeToString(pinnedKey), "v1.2.3")

	if _, err := client.verifyUpgradeRelease(context.Background(), server.Client(), release.DigestBytes(binary), int64(len(binary)), client.controlPlaneVersion); err == nil {
		t.Fatal("a manifest signed by an unpinned key was accepted")
	}
}

func TestUpgradeReleaseRejectsMalformedPinnedKey(t *testing.T) {
	server := releaseServer(t, []byte(`{}`))
	defer server.Close()
	client := upgradeReleaseClient(t, server.URL, "not-a-key", "v1.2.3")
	if _, err := client.verifyUpgradeRelease(context.Background(), server.Client(), release.DigestBytes([]byte("binary")), 6, client.controlPlaneVersion); err == nil {
		t.Fatal("a malformed pinned key was accepted")
	}
}

// The Agent must land on the build the panel considers current. A signed release
// for a different version is rejected even though its signature and digest are
// valid: this is the version binding, and it is what stops a panel from moving a
// node onto a build it does not actually run.
func TestUpgradeReleaseRequiresPanelVersionMatch(t *testing.T) {
	binary := []byte("agent-binary-payload")
	manifest, publicKey, _ := upgradeReleaseFixture(t, binary, "v1.2.3")
	body, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	server := releaseServer(t, body)
	defer server.Close()
	pinned := base64.RawURLEncoding.EncodeToString(publicKey)
	digest := []byte(release.DigestBytes(binary))

	t.Run("matching version", func(t *testing.T) {
		client := upgradeReleaseClient(t, server.URL, pinned, "v1.2.3")
		version, err := client.verifyUpgradeRelease(context.Background(), server.Client(), digest, int64(len(binary)), client.controlPlaneVersion)
		if err != nil {
			t.Fatalf("matching panel version was rejected: %v", err)
		}
		if version != "v1.2.3" {
			t.Fatalf("version = %q, want v1.2.3", version)
		}
	})
	t.Run("mismatched version", func(t *testing.T) {
		client := upgradeReleaseClient(t, server.URL, pinned, "v9.9.9")
		_, err := client.verifyUpgradeRelease(context.Background(), server.Client(), digest, int64(len(binary)), client.controlPlaneVersion)
		if err == nil {
			t.Fatal("a release for a different panel version was accepted")
		}
		if !strings.Contains(err.Error(), "does not match control plane version") {
			t.Fatalf("error does not describe the version mismatch: %v", err)
		}
	})
	t.Run("panel reports no version", func(t *testing.T) {
		client := upgradeReleaseClient(t, server.URL, pinned, "")
		if _, err := client.verifyUpgradeRelease(context.Background(), server.Client(), digest, int64(len(binary)), client.controlPlaneVersion); err == nil {
			t.Fatal("an upgrade proceeded without a panel version to bind to")
		}
	})
}
