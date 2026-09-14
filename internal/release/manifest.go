// Package release signs and verifies the artifacts a control plane serves to
// freshly enrolling Agents and to already deployed ones.
//
// The control plane is the machine that hands out the Agent binary and the
// installer scripts, so it cannot be its own trust anchor: a checksum delivered
// beside the file proves only that the transfer was intact, and an attacker who
// controls the response can rewrite both. Integrity therefore rests on a
// detached Ed25519 signature made with a key that never reaches the control
// plane. The control plane loads the public key and a manifest that was signed
// elsewhere (release tooling, or CI with a protected environment), verifies it
// once at startup, and can then serve artifacts whose digests are covered by a
// signature it is unable to forge.
package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	// SignatureAlgorithm is the only algorithm this package accepts. It is part
	// of the signed payload, so a manifest cannot be replayed under a weaker
	// scheme.
	SignatureAlgorithm = "ed25519"
	// ManifestVersion versions the signed payload itself rather than the release
	// it describes, so the format can change without breaking older verifiers.
	ManifestVersion = 1
)

// Roles identify what an artifact is and where the client may install it from.
const (
	RoleAgentBinary  = "agent-binary"
	RoleInstallAsset = "install-asset"
)

// KnownRoles lists the role values a manifest may declare.
var KnownRoles = []string{RoleAgentBinary, RoleInstallAsset}

// AgentBinaryKey is the manifest key of the Agent executable. Clients look it up
// by this constant rather than by scanning the artifact list, so a manifest that
// omits it is rejected instead of silently verifying nothing.
const AgentBinaryKey = RoleAgentBinary

// Artifact is one signed downloadable file.
type Artifact struct {
	// Role is one of KnownRoles.
	Role string `json:"role"`
	// Path is the request path the artifact is served at, for example
	// "/api/v1/agent-binary" or "/install-assets/deploy/systemd/qagent.service".
	Path string `json:"path"`
	// Version is the artifact's own version label. Binding it into the signed
	// payload is what keeps a served version string from being edited in
	// transit: a client reads the label only after the signature covering it
	// verified, so the control plane cannot claim a version the signed content
	// does not have.
	Version string `json:"version"`
	// SHA256 is the lowercase hex digest of the raw file bytes.
	SHA256 string `json:"sha256"`
	// Size is the exact byte length, which lets a client reject a truncated or
	// padded body before hashing it.
	Size int64 `json:"size"`
}

// Manifest is the signed description of one release's downloadable artifacts.
type Manifest struct {
	ManifestVersion int `json:"manifest_version"`
	// Release is the release identifier the operator chose (usually a tag or
	// commit). Informational: trust comes from the signature, not this string.
	Release   string     `json:"release"`
	SignedAt  time.Time  `json:"signed_at"`
	Artifacts []Artifact `json:"artifacts"`

	Signature struct {
		Algorithm string `json:"algorithm"`
		// PublicKey is the base64 (raw URL) Ed25519 public key that made the
		// signature. Carrying it here lets an operator diagnose a key mismatch;
		// verification still compares it against the pinned key, so a manifest
		// cannot introduce its own trust root.
		PublicKey string `json:"public_key"`
		Value     string `json:"value"`
	} `json:"signature"`
}

// signaturePayload is the exact JSON structure the signature covers. It mirrors
// the manifest minus the signature block, so the signed bytes never include the
// signature that covers them.
type signaturePayload struct {
	ManifestVersion int        `json:"manifest_version"`
	Release         string     `json:"release"`
	SignedAt        time.Time  `json:"signed_at"`
	Artifacts       []Artifact `json:"artifacts"`
}

// payload renders the canonical bytes a signature covers. The artifact list is
// sorted by path first so two semantically identical manifests produce the same
// bytes regardless of the order the release tool listed them in.
func (m Manifest) payload() ([]byte, error) {
	artifacts := append([]Artifact(nil), m.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	// json.Marshal emits struct fields in declaration order and never consults a
	// map, so the encoding is deterministic for identical input.
	return json.Marshal(signaturePayload{
		ManifestVersion: m.ManifestVersion,
		Release:         m.Release,
		SignedAt:        m.SignedAt.UTC(),
		Artifacts:       artifacts,
	})
}

// Sign returns a copy of the manifest carrying a detached signature made with
// privateKey. It fails rather than returning a manifest that would not verify,
// so callers cannot publish an unsigned release by accident.
func (m Manifest) Sign(privateKey ed25519.PrivateKey) (Manifest, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Manifest{}, errors.New("release: invalid Ed25519 private key")
	}
	m.ManifestVersion = ManifestVersion
	if err := m.validateShape(); err != nil {
		return Manifest{}, err
	}
	payload, err := m.payload()
	if err != nil {
		return Manifest{}, err
	}
	signature := ed25519.Sign(privateKey, payload)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	m.Signature.Algorithm = SignatureAlgorithm
	m.Signature.PublicKey = base64.RawURLEncoding.EncodeToString(publicKey)
	m.Signature.Value = base64.RawURLEncoding.EncodeToString(signature)
	return m, nil
}

// Verify checks the signature against a pinned public key and then validates
// every declared digest. A nil publicKey means the deployment did not pin one;
// the caller decides whether that is acceptable, so this returns
// ErrNoPublicKey instead of silently trusting the manifest.
func (m Manifest) Verify(publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return ErrNoPublicKey
	}
	if m.Signature.Algorithm != SignatureAlgorithm {
		return fmt.Errorf("release: unsupported signature algorithm %q", m.Signature.Algorithm)
	}
	if m.ManifestVersion != ManifestVersion {
		return fmt.Errorf("release: unsupported manifest version %d", m.ManifestVersion)
	}
	signature, err := base64.RawURLEncoding.DecodeString(m.Signature.Value)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("release: malformed manifest signature")
	}
	payload, err := m.payload()
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return errors.New("release: manifest signature does not match the pinned release key")
	}
	return m.validateShape()
}

// ErrNoPublicKey reports that the caller has no pinned release key. It is a
// distinct error so a caller can choose to warn instead of failing.
var ErrNoPublicKey = errors.New("release: no release public key is configured")

// validateShape rejects a manifest that could not be enforced safely.
func (m Manifest) validateShape() error {
	if m.ManifestVersion != ManifestVersion {
		return fmt.Errorf("release: unsupported manifest version %d", m.ManifestVersion)
	}
	if strings.TrimSpace(m.Release) == "" {
		return errors.New("release: manifest has no release identifier")
	}
	if len(m.Artifacts) == 0 {
		return errors.New("release: manifest declares no artifacts")
	}
	seen := make(map[string]struct{}, len(m.Artifacts))
	for _, artifact := range m.Artifacts {
		if !knownRole(artifact.Role) {
			return fmt.Errorf("release: unknown artifact role %q", artifact.Role)
		}
		if !strings.HasPrefix(artifact.Path, "/") {
			return fmt.Errorf("release: artifact path %q must be absolute", artifact.Path)
		}
		if _, exists := seen[artifact.Path]; exists {
			return fmt.Errorf("release: artifact path %q is declared twice", artifact.Path)
		}
		seen[artifact.Path] = struct{}{}
		if artifact.Size < 0 {
			return fmt.Errorf("release: artifact %q has a negative size", artifact.Path)
		}
		if err := ValidateDigest(artifact.SHA256); err != nil {
			return fmt.Errorf("release: artifact %q: %w", artifact.Path, err)
		}
	}
	return nil
}

func knownRole(role string) bool {
	for _, candidate := range KnownRoles {
		if role == candidate {
			return true
		}
	}
	return false
}

// DecodePublicKey accepts the raw-URL base64 form written by the release tooling
// and rejects anything that is not a 32-byte Ed25519 public key.
func DecodePublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("release: value is not a raw-URL base64 Ed25519 public key")
	}
	return ed25519.PublicKey(decoded), nil
}

// ValidateDigest reports whether value is a lowercase hex SHA-256 digest.
func ValidateDigest(value string) error {
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("digest must be %d hex characters", sha256.Size*2)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return errors.New("digest is not hexadecimal")
	}
	if value != strings.ToLower(value) {
		return errors.New("digest must be lowercase hex")
	}
	return nil
}

// Digest returns the lowercase hex SHA-256 of content.
func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// DigestBytes returns the raw SHA-256 of content, which is the form a streaming
// download already computes.
func DigestBytes(content []byte) []byte {
	sum := sha256.Sum256(content)
	return sum[:]
}

// Parse decodes a manifest and rejects trailing data, so a file cannot smuggle a
// second document past a verifier that only reads the first value.
func Parse(raw []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("release: parse manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("release: manifest must contain a single JSON document")
	}
	return manifest, nil
}

// Load reads and parses a manifest from disk.
func Load(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	return Parse(raw)
}

// Marshal renders the manifest as indented JSON with the signature last, which
// keeps a checked-in manifest reviewable.
func (m Manifest) Marshal() ([]byte, error) {
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// AgentBinary returns the artifact describing the Agent executable.
func (m Manifest) AgentBinary() (Artifact, bool) {
	for _, artifact := range m.Artifacts {
		if artifact.Role == RoleAgentBinary {
			return artifact, true
		}
	}
	return Artifact{}, false
}

// ArtifactAt returns the artifact served at the given request path.
func (m Manifest) ArtifactAt(path string) (Artifact, bool) {
	for _, artifact := range m.Artifacts {
		if artifact.Path == path {
			return artifact, true
		}
	}
	return Artifact{}, false
}

// ArtifactByRelativePath resolves an installer-relative asset name such as
// "deploy/systemd/qagent.service" to its signed artifact. The installer works
// with relative names while the manifest keys on request paths, so the mapping
// lives here instead of being re-derived by every client.
func (m Manifest) ArtifactByRelativePath(relative string) (Artifact, bool) {
	relative = strings.TrimPrefix(strings.TrimSpace(relative), "/")
	for _, artifact := range m.Artifacts {
		if strings.TrimPrefix(artifact.Path, InstallAssetPrefix) == relative {
			return artifact, true
		}
	}
	return Artifact{}, false
}

// InstallAssetPrefix is the request-path prefix used for installer assets.
const InstallAssetPrefix = "/install-assets/"
