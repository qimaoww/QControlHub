package release

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Keep the publication gate structural: no image job may get the production
// private key or advance latest before the complete signed release is present.
func TestReleaseWorkflowPublicationGate(t *testing.T) {
	content, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	type step struct {
		Uses string            `yaml:"uses"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
		With map[string]string `yaml:"with"`
	}
	type job struct {
		If          string            `yaml:"if"`
		Needs       string            `yaml:"needs"`
		Environment string            `yaml:"environment"`
		Env         map[string]string `yaml:"env"`
		Steps       []step            `yaml:"steps"`
	}
	// Other CI jobs may have array-valued needs. Decode only the release jobs.
	var workflow struct {
		Jobs map[string]yaml.Node `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatal(err)
	}
	jobs := make(map[string]job)
	for _, name := range []string{"sign-release", "publish", "promote"} {
		node, ok := workflow.Jobs[name]
		if !ok {
			t.Fatalf("missing release job %q", name)
		}
		var parsed job
		if err := node.Decode(&parsed); err != nil {
			t.Fatal(err)
		}
		jobs[name] = parsed
	}
	sign := jobs["sign-release"]
	if sign.Needs != "images" || sign.Environment != "release-signing" ||
		!strings.Contains(sign.If, "github.event_name == 'push'") || !strings.Contains(sign.If, "refs/heads/main") {
		t.Fatal("production signing must require tested main images and the protected environment")
	}
	if _, exposed := sign.Env["RELEASE_SIGNING_KEY"]; exposed {
		t.Fatal("private key must be step-scoped, not job-wide")
	}
	gated := false
	for _, step := range sign.Steps {
		if strings.Contains(step.Env["RELEASE_SIGNING_KEY"], "secrets.RELEASE_SIGNING_KEY") {
			gated = strings.Contains(step.Run, `if [ -z "$RELEASE_SIGNING_KEY" ]; then`) &&
				strings.Contains(step.Run, "exit 1")
		}
	}
	if !gated {
		t.Fatal("missing production key must fail before publication")
	}
	if jobs["publish"].Needs != "sign-release" || jobs["promote"].Needs != "publish" {
		t.Fatal("publication and latest promotion must wait for the complete preceding stage")
	}
	for _, name := range []string{"publish", "promote"} {
		node := workflow.Jobs[name]
		encoded, err := yaml.Marshal(&node)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "RELEASE_SIGNING_KEY") {
			t.Fatalf("%s job must not receive the production signing key", name)
		}
	}
	for _, step := range jobs["publish"].Steps {
		if strings.Contains(step.With["tags"], ":latest") {
			t.Fatal("image matrix may only publish immutable tags, not partial latest releases")
		}
	}
}
