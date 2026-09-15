package frontend

import (
	"os/exec"
	"testing"
)

func TestAgentBrowserRuntimeSmoke(t *testing.T) {
	command := exec.Command("node", "agents_browser_smoke.mjs")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("agent browser runtime smoke failed: %v\n%s", err, output)
	}
}
