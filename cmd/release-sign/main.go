// Command release-sign produces and verifies the signed release manifest that a
// control plane serves to enrolling and already deployed Agents.
//
// It is deliberately a separate binary from the control plane: the signing key
// must not live in the process that hands out the artifacts, otherwise the
// signature only proves the control plane trusts itself. Run `keygen` on the
// release machine or in a protected CI environment, keep the private key there,
// and give the control plane nothing but the public key and the signed manifest.
//
// Usage:
//
//	release-sign keygen   [-private key.txt] [-public key.pub]
//	release-sign sign     -key key.txt -release v1.2.3 -agent <file> -agent-version <label>
//	                      -assets <dir> [-asset-root <dir>] [-out release-manifest.json]
//	release-sign verify   -manifest release-manifest.json [-key key.pub]
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/release"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = runKeygen(os.Args[2:])
	case "sign":
		err = runSign(os.Args[2:])
	case "verify":
		err = runVerify(os.Args[2:])
	case "checksums":
		err = runChecksums(os.Args[2:])
	case "pubkey":
		err = runPubkey(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "release-sign: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `release-sign - sign the QControlHub release manifest

  keygen  -private <path> [-public <path>]            generate an Ed25519 keypair
  sign    -key <path> -release <id> -agent <path>
          -agent-version <label> -assets <dir>
          [-asset-root <dir>] [-out <path>]           build and sign a manifest
  verify  -manifest <path> [-key <path>]              verify a manifest
  pubkey  -public <path> [-out <path>]               convert the public key to
                                                      PEM for openssl pkeyutl
  checksums -key <path> -agent <path> -assets <dir>
          [-asset-root <dir>] [-out <dir>]            write SHA256SUMS and its
                                                      signature for the installer

The private key never belongs on a control plane: ship only the public key and
the signed manifest there.
`)
}

func runKeygen(args []string) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	privatePath := flags.String("private", "", "path to write the private key (required)")
	publicPath := flags.String("public", "", "path to write the public key (default: <private>.pub)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*privatePath) == "" {
		return errors.New("keygen requires -private")
	}
	if *publicPath == "" {
		*publicPath = *privatePath + ".pub"
	}
	for _, target := range []string{*privatePath, *publicPath} {
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("refusing to overwrite existing key file %s", target)
		}
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	// The private key is written 0600 and must stay on the release machine.
	if err := writeFileExclusive(*privatePath, []byte(base64.RawURLEncoding.EncodeToString(privateKey)), 0o600); err != nil {
		return err
	}
	if err := writeFileExclusive(*publicPath, []byte(base64.RawURLEncoding.EncodeToString(publicKey)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote private key %s (keep it off the control plane)\n", *privatePath)
	fmt.Printf("wrote public key  %s\n", *publicPath)
	fmt.Printf("release public key: %s\n", base64.RawURLEncoding.EncodeToString(publicKey))
	return nil
}

func runSign(args []string) error {
	flags := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyPath := flags.String("key", "", "private key file (required)")
	releaseID := flags.String("release", "", "release identifier, usually a tag or commit (required)")
	agentPath := flags.String("agent", "", "Agent executable to sign (required)")
	agentVersion := flags.String("agent-version", "", "Agent version label bound into the manifest (required)")
	assetsDir := flags.String("assets", "", "directory of installer assets to sign (required)")
	assetRoot := flags.String("asset-root", "", "path prefix the assets are served under (default: the assets directory itself)")
	outPath := flags.String("out", "release-manifest.json", "manifest output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"-key": *keyPath, "-release": *releaseID, "-agent": *agentPath,
		"-agent-version": *agentVersion, "-assets": *assetsDir,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("sign requires %s", name)
		}
	}
	privateKey, err := readPrivateKey(*keyPath)
	if err != nil {
		return err
	}

	artifacts, err := collectArtifacts(*agentPath, *assetsDir, *assetRoot)
	if err != nil {
		return err
	}
	// The version label travels in the JSON manifest; the signed checksum list
	// deliberately carries digests only.
	for index := range artifacts {
		artifacts[index].Version = *agentVersion
	}

	manifest := release.Manifest{
		ManifestVersion: release.ManifestVersion,
		Release:         *releaseID,
		SignedAt:        time.Now().UTC(),
		Artifacts:       artifacts,
	}

	signed, err := manifest.Sign(privateKey)
	if err != nil {
		return err
	}
	encoded, err := signed.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, encoded, 0o644); err != nil {
		return err
	}
	fmt.Printf("signed manifest %s: release=%s artifacts=%d\n", *outPath, signed.Release, len(signed.Artifacts))
	fmt.Printf("release public key: %s\n", signed.Signature.PublicKey)
	return nil
}

func runVerify(args []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "", "manifest to verify (required)")
	keyPath := flags.String("key", "", "public key file; verifies the signature when set")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*manifestPath) == "" {
		return errors.New("verify requires -manifest")
	}
	manifest, err := release.Load(*manifestPath)
	if err != nil {
		return err
	}
	if *keyPath != "" {
		content, err := os.ReadFile(*keyPath)
		if err != nil {
			return err
		}
		publicKey, err := decodePublicKey(string(content))
		if err != nil {
			return err
		}
		if err := manifest.Verify(publicKey); err != nil {
			return err
		}
		fmt.Printf("signature OK for release=%s\n", manifest.Release)
	} else {
		fmt.Println("no -key given: reporting contents without verifying the signature")
	}
	fmt.Printf("release=%s signed_at=%s artifacts=%d\n", manifest.Release, manifest.SignedAt.Format(time.RFC3339), len(manifest.Artifacts))
	artifacts := append([]release.Artifact(nil), manifest.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	for _, artifact := range artifacts {
		fmt.Printf("  %-58s %-10s %s\n", artifact.Path, artifact.Version, artifact.SHA256)
	}
	return nil
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(content)))
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s does not hold a raw-URL base64 Ed25519 private key", path)
	}
	return ed25519.PrivateKey(decoded), nil
}

func decodePublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("value is not a raw-URL base64 Ed25519 public key")
	}
	return ed25519.PublicKey(decoded), nil
}

func writeFileExclusive(path string, content []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// runChecksums writes the signed checksum list the POSIX installer verifies with
// stock tools. It covers the Agent binary and every installer asset, because the
// installer has no JSON parser and must not depend on this binary to check the
// files it is about to place on the host.
func runChecksums(args []string) error {
	flags := flag.NewFlagSet("checksums", flag.ContinueOnError)
	keyPath := flags.String("key", "", "private key file (required)")
	agentPath := flags.String("agent", "", "Agent executable to include (required)")
	assetsDir := flags.String("assets", "", "directory of installer assets to include (required)")
	assetRoot := flags.String("asset-root", "", "path prefix the assets are served under (default: the assets directory itself)")
	outDir := flags.String("out", ".", "directory to write SHA256SUMS and SHA256SUMS.sig into")
	if err := flags.Parse(args); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"-key": *keyPath, "-agent": *agentPath, "-assets": *assetsDir,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("checksums requires %s", name)
		}
	}
	privateKey, err := readPrivateKey(*keyPath)
	if err != nil {
		return err
	}
	artifacts, err := collectArtifacts(*agentPath, *assetsDir, *assetRoot)
	if err != nil {
		return err
	}
	if len(artifacts) == 0 {
		return errors.New("no artifacts to sign")
	}
	list, signature, err := release.SignChecksums(artifacts, privateKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	listPath := filepath.Join(*outDir, release.ChecksumsFile)
	signaturePath := filepath.Join(*outDir, release.ChecksumsSignatureFile)
	if err := os.WriteFile(listPath, list, 0o644); err != nil {
		return err
	}
	// The signature travels as base64 text so it survives copy, paste and text
	// transports; DecodeChecksumsSignature accepts it either way.
	if err := os.WriteFile(signaturePath, []byte(release.EncodeChecksumsSignature(signature)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d artifacts) and %s\n", listPath, len(artifacts), signaturePath)
	fmt.Printf("release public key: %s\n", base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)))
	return nil
}

// collectArtifacts hashes the Agent binary and every installer asset. Both the
// JSON manifest and the shell-consumable checksum list are built from this one
// list, so the two can never describe different bytes.
func collectArtifacts(agentPath, assetsDir, assetRoot string) ([]release.Artifact, error) {
	agentInfo, err := os.Stat(agentPath)
	if err != nil {
		return nil, err
	}
	if !agentInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("Agent binary %s is not a regular file", agentPath)
	}
	agentContent, err := os.ReadFile(agentPath)
	if err != nil {
		return nil, err
	}
	artifacts := []release.Artifact{{
		Role:   release.RoleAgentBinary,
		Path:   "/api/v1/agent-binary",
		SHA256: release.Digest(agentContent),
		Size:   int64(len(agentContent)),
	}}

	assetRootPath := assetRoot
	if assetRootPath == "" {
		assetRootPath = assetsDir
	}
	// Walk deterministically so the same inputs produce the same manifest.
	var relativeAssets []string
	err = filepath.WalkDir(assetRootPath, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("asset %s is not a regular file", current)
		}
		relative, err := filepath.Rel(assetRootPath, current)
		if err != nil {
			return err
		}
		relativeAssets = append(relativeAssets, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(relativeAssets) == 0 {
		return nil, fmt.Errorf("no assets found under %s", assetRootPath)
	}
	sort.Strings(relativeAssets)
	for _, relative := range relativeAssets {
		// Reject a traversal-shaped name instead of emitting a path a client
		// would resolve somewhere unexpected.
		if strings.HasPrefix(relative, "../") || path.IsAbs(relative) || strings.Contains(relative, "/../") {
			return nil, fmt.Errorf("asset path %q escapes the asset root", relative)
		}
		content, err := os.ReadFile(filepath.Join(assetRootPath, filepath.FromSlash(relative)))
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, release.Artifact{
			Role:   release.RoleInstallAsset,
			Path:   release.InstallAssetPrefix + relative,
			SHA256: release.Digest(content),
			Size:   int64(len(content)),
		})
	}
	return artifacts, nil
}

// runPubkey converts the raw-URL public key into a PEM file.
//
// OpenSSL verifies an Ed25519 signature with `pkeyutl -verify -rawin`, not with
// `dgst -sha256` (Ed25519 is PureEdDSA and rejects an external digest). The PEM
// form is also easy to get wrong by hand, so the tool that makes the key also
// makes the file the installer is verified against.
func runPubkey(args []string) error {
	flags := flag.NewFlagSet("pubkey", flag.ContinueOnError)
	publicPath := flags.String("public", "", "raw-URL base64 public key file (required)")
	outPath := flags.String("out", "", "PEM output path (default: <public>.pem)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*publicPath) == "" {
		return errors.New("pubkey requires -public")
	}
	if *outPath == "" {
		*outPath = *publicPath + ".pem"
	}
	content, err := os.ReadFile(*publicPath)
	if err != nil {
		return err
	}
	publicKey, err := decodePublicKey(string(content))
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return err
	}
	pem := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if pem == nil {
		return errors.New("encode PEM public key")
	}
	if err := os.WriteFile(*outPath, pem, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", *outPath)
	fmt.Printf("installers verify the release signature with:\n  openssl pkeyutl -verify -pubin -inkey %s -rawin -in SHA256SUMS -sigfile SHA256SUMS.sig.bin\n", *outPath)
	return nil
}
