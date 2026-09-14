package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/release"
)

// writeFile creates a file and its parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// assetPaths returns just the request paths, which is what the checksum list and
// the installer agree on.
func assetPaths(artifacts []release.Artifact) []string {
	paths := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		paths = append(paths, artifact.Path)
	}
	return paths
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

// TestCollectArtifactsKeepsAssetPathsRelativeToRoot pins the contract that the
// installer depends on: it checks the list from the directory it downloads into,
// so a top-level asset has to keep its deploy/ prefix rather than being reduced to
// a bare file name. Signing a directory tree is what previously produced a list
// covering files the web image never serves.
func TestCollectArtifactsKeepsAssetPathsRelativeToRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "bin", "qagent"), "agent-binary")
	writeFile(t, filepath.Join(root, "deploy", "bootstrap-core-services.sh"), "bootstrap")
	writeFile(t, filepath.Join(root, "deploy", "systemd", "qagent.service"), "unit")
	writeFile(t, filepath.Join(root, "examples", "configs", "xray-minimal.json"), "{}")

	assets := []string{
		filepath.Join(root, "deploy", "bootstrap-core-services.sh"),
		filepath.Join(root, "deploy", "systemd", "qagent.service"),
		filepath.Join(root, "examples", "configs", "xray-minimal.json"),
	}
	artifacts, err := collectArtifacts(filepath.Join(root, "bin", "qagent"), assets, root)
	if err != nil {
		t.Fatalf("collectArtifacts: %v", err)
	}

	paths := assetPaths(artifacts)
	for _, want := range []string{
		"/api/v1/agent-binary",
		"/install-assets/deploy/bootstrap-core-services.sh",
		"/install-assets/deploy/systemd/qagent.service",
		"/install-assets/examples/configs/xray-minimal.json",
	} {
		if !containsPath(paths, want) {
			t.Errorf("missing %s in %v", want, paths)
		}
	}
	for _, path := range paths {
		if path == "/install-assets/bootstrap-core-services.sh" {
			t.Errorf("asset lost its deploy/ prefix: %v", paths)
		}
	}
}

// TestCollectArtifactsRejectsAmbiguousOrUnsafeAssets covers the inputs that would
// otherwise produce a list naming a file twice or naming a path outside the served
// root.
func TestCollectArtifactsRejectsAmbiguousOrUnsafeAssets(t *testing.T) {
	root := t.TempDir()
	agent := filepath.Join(root, "bin", "qagent")
	writeFile(t, agent, "agent-binary")
	asset := filepath.Join(root, "deploy", "bootstrap-core-services.sh")
	writeFile(t, asset, "bootstrap")
	directory := filepath.Join(root, "deploy", "systemd")
	writeFile(t, filepath.Join(directory, "qagent.service"), "unit")

	cases := []struct {
		name   string
		assets []string
	}{
		{"no assets", nil},
		{"directory instead of a file", []string{directory}},
		{"same file twice", []string{asset, asset}},
		{"escapes the root", []string{asset}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assetRoot := root
			if testCase.name == "escapes the root" {
				assetRoot = filepath.Join(root, "deploy", "systemd")
			}
			if _, err := collectArtifacts(agent, testCase.assets, assetRoot); err == nil {
				t.Fatalf("expected an error for %s", testCase.name)
			}
		})
	}
}

// TestChecksumsListMatchesServedPaths signs the collected artifacts and checks the
// list the installer consumes: every entry has to resolve inside the served root
// and carry the digest of the file at that path, which is exactly what
// `sha256sum -c` does on a node.
func TestChecksumsListMatchesServedPaths(t *testing.T) {
	root := t.TempDir()
	agent := filepath.Join(root, "bin", "qagent")
	writeFile(t, agent, "agent-binary")
	writeFile(t, filepath.Join(root, "deploy", "bootstrap-core-services.sh"), "bootstrap")
	writeFile(t, filepath.Join(root, "deploy", "systemd", "qagent.service"), "unit")
	assets := []string{
		filepath.Join(root, "deploy", "bootstrap-core-services.sh"),
		filepath.Join(root, "deploy", "systemd", "qagent.service"),
	}
	artifacts, err := collectArtifacts(agent, assets, root)
	if err != nil {
		t.Fatalf("collectArtifacts: %v", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	list, signature, err := release.SignChecksums(artifacts, privateKey)
	if err != nil {
		t.Fatalf("SignChecksums: %v", err)
	}
	if len(signature) != ed25519.SignatureSize {
		t.Fatalf("signature is %d bytes, want %d", len(signature), ed25519.SignatureSize)
	}

	// Serve the assets the way the web image does, from install-assets/.
	served := filepath.Join(root, "install-assets")
	for _, artifact := range artifacts {
		if artifact.Role != release.RoleInstallAsset {
			continue
		}
		relative := strings.TrimPrefix(artifact.Path, release.InstallAssetPrefix)
		destination := filepath.Join(served, filepath.FromSlash(relative))
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		writeFile(t, destination, string(content))
	}

	parsed, err := release.ParseChecksums(list)
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	// The Agent binary is listed for the installer to verify, but the control
	// plane serves it at /api/v1/agent-binary, not from the asset root, so only
	// the installer assets have to exist below install-assets/.
	verified := 0
	for _, entry := range parsed {
		if !strings.HasPrefix(entry.Path, "deploy/") && !strings.HasPrefix(entry.Path, "examples/") {
			continue
		}
		servedPath := filepath.Join(served, filepath.FromSlash(entry.Path))
		content, err := os.ReadFile(servedPath)
		if err != nil {
			t.Fatalf("list names %s, which is not served: %v", entry.Path, err)
		}
		if actual := release.Digest(content); actual != entry.SHA256 {
			t.Errorf("digest mismatch for %s: list has %s, served file has %s", entry.Path, entry.SHA256, actual)
		}
		verified++
	}
	if verified != len(assets) {
		t.Errorf("verified %d served assets, want %d", verified, len(assets))
	}
	// One entry per asset plus the Agent binary.
	if len(parsed) != len(assets)+1 {
		t.Errorf("list covers %d files, want %d", len(parsed), len(assets)+1)
	}
}

// TestRejectOperands covers the guard that turns a silently truncated command into
// a failure. flag stops parsing at the first operand, so a split value such as
// "-assets deploy examples/configs" would otherwise drop every flag after it and
// still exit zero.
func TestRejectOperands(t *testing.T) {
	if err := rejectOperands("checksums", nil); err != nil {
		t.Fatalf("no operands should be accepted: %v", err)
	}
	err := rejectOperands("checksums", []string{"examples/configs"})
	if err == nil {
		t.Fatal("expected an error for a leftover operand")
	}
	if !strings.Contains(err.Error(), "examples/configs") {
		t.Errorf("error should name the operand, got %v", err)
	}
}
