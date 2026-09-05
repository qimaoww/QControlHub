//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportResourcesReuseAndRollbackOwnership(t *testing.T) {
	requireAgentRoot(t)
	for _, scenario := range []string{"reused", "changed-source", "changed-destination", "symlink", "partial", "changed-after-stage"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "source.acl")
			if err := os.WriteFile(source, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			resource := coreImportResource{source: source, destination: filepath.Join(root, "import", "policy.acl"), digest: coreMigrationConfigDigest("original")}
			plan := coreImportPlan{stateRoot: root, resourceRoot: filepath.Join(root, "import"), resources: []coreImportResource{resource}}
			identity := commandIdentity{uid: 12345, gid: 12345}
			if scenario == "changed-after-stage" {
				if err := plan.stage(identity); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(resource.destination, []byte("external-change"), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := plan.cleanup(); err == nil {
					t.Fatal("removed externally changed resource")
				}
				return
			}
			previous := plan
			if err := previous.stage(identity); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "changed-source":
				if err := os.WriteFile(source, []byte("renewed"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "changed-destination":
				if err := os.WriteFile(resource.destination, []byte("external-change"), 0o640); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(resource.destination); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(source, resource.destination); err != nil {
					t.Fatal(err)
				}
			case "partial":
				plan.resources = append(plan.resources, coreImportResource{source: filepath.Join(root, "missing"), destination: filepath.Join(plan.resourceRoot, "missing")})
			}
			before, err := os.ReadFile(resource.destination)
			if err != nil {
				t.Fatal(err)
			}
			err = plan.stage(identity)
			if (scenario == "reused") != (err == nil) {
				t.Fatalf("stage %s: %v", scenario, err)
			}
			if err := plan.cleanup(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(resource.destination)
			if err != nil || string(after) != string(before) {
				t.Fatalf("cleanup changed a preexisting resource: %v", err)
			}
		})
	}
}
