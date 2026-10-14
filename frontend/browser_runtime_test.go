package frontend

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestAgentBrowserRuntimeSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "agents_browser_smoke.mjs")
	// Let the scheduler terminate workers, browser process groups and profiles
	// before Go escalates. This deadline is inside the package's 20m deadline.
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = 10 * time.Second
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("agent browser runtime smoke failed: %v\n%s", err, output)
	}
	t.Logf("%s", output)
}
