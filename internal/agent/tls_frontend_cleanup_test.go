package agent

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTLSCleanupRetainsRemovedAndRollbackReferences(t *testing.T) {
	old := core.TLSFrontend{Tag: "old", Mode: "caddy", Domain: "old.example.test", PublicPort: 443, BackendPort: 21001, Transport: "xhttp", Path: "/old", StaticHTML: "old page"}
	current := old
	current.Tag, current.Path, current.StaticHTML = "new", "/new", "new page"
	previous := tlsDeployments{"removed": {Frontends: []core.TLSFrontend{old}}, "changed": {Frontends: []core.TLSFrontend{current}, NativeHandoff: &tlsDeployment{Frontends: []core.TLSFrontend{old}}}}
	next := tlsDeployments{"changed": {Frontends: []core.TLSFrontend{current}}}
	retainRetiredTLS(previous, next)
	encoded, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	var restarted tlsDeployments
	if err := json.Unmarshal(encoded, &restarted); err != nil {
		t.Fatal(err)
	}
	if len(restarted["removed"].RetiredFrontends) != 1 || len(restarted["changed"].RetiredFrontends) != 2 {
		t.Fatal("removed/held references did not survive serialization")
	}
	retainRetiredTLS(restarted, restarted)
	if len(restarted["removed"].RetiredFrontends) != 1 || len(restarted["changed"].RetiredFrontends) != 2 {
		t.Fatal("retries accumulate duplicate cleanup references")
	}
	refs := tlsDeploymentReferences(previous["changed"])
	if len(refs) != 2 {
		t.Fatal("rollback reference was omitted from live ownership")
	}
}

func TestTLSCleanupCannotFollowStateSymlinks(t *testing.T) {
	directory, outside := t.TempDir(), t.TempDir()
	file := filepath.Join(outside, "keep")
	if err := os.WriteFile(file, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := removeTLSFiles(directory, "escape/keep"); err == nil {
		t.Fatal("cleanup followed an external state symlink")
	}
	if err := removeTLSFiles(directory, "escape"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "external" {
		t.Fatal("cleanup touched an external file", err)
	}
}

func TestTLSCleanupPreservesSharedCertificateSubjects(t *testing.T) {
	m := tlsFrontendManager{state: t.TempDir()}
	for _, domain := range []string{"removed.example.test", "shared.example.test"} {
		path := filepath.Join(m.state, "caddy", "certificates", "issuer", domain)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, domain+".key"), []byte("fixture key"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	retired := tlsDeployments{"removed": {Frontends: []core.TLSFrontend{{Domain: "removed.example.test"}, {Domain: "shared.example.test"}}}}
	if err := m.cleanupCaddyDomains(retired, map[string]bool{"shared.example.test": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.state, "caddy", "certificates", "issuer", "removed.example.test")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("removed certificate subject remains", err)
	}
	if _, err := os.Stat(filepath.Join(m.state, "caddy", "certificates", "issuer", "shared.example.test")); err != nil {
		t.Fatal("shared certificate subject was removed", err)
	}
}

func TestTLSCleanupRemovesOnlyItsInstalledCaddyBinary(t *testing.T) {
	for _, scenario := range []string{"owned", "preinstalled", "replaced", "already-removed"} {
		t.Run(scenario, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "caddy")
			content := []byte("owned executable")
			if scenario != "already-removed" {
				if err := os.WriteFile(binary, content, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "preinstalled" {
				if err := os.WriteFile(binary+".qch-owned", []byte(fmt.Sprintf("%x", sha256.Sum256(content))), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "replaced" {
				if err := os.WriteFile(binary, []byte("operator replacement"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := removeOwnedCaddyBinaryAt(binary); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(binary)
			if (scenario == "owned" || scenario == "already-removed") != errors.Is(err, os.ErrNotExist) {
				t.Fatal("binary ownership was not respected", err)
			}
			if err := removeOwnedCaddyBinaryAt(binary); err != nil {
				t.Fatal("cleanup retry", err)
			}
		})
	}
}
