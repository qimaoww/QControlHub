//go:build linux

package agent

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestManagedCoreJournalConfigForPolicy(t *testing.T) {
	t.Parallel()
	config := string(managedCoreJournalConfigForPolicy(core.AgentPolicy{CoreLogMaxMiB: 1, CoreLogRotateCount: 1}))
	for _, expected := range []string{"Storage=volatile", "RuntimeMaxUse=1048576", "RuntimeMaxFileSize=524288"} {
		if !strings.Contains(config, expected) {
			t.Errorf("journal policy missing %q:\n%s", expected, config)
		}
	}
}

func TestOpenRCCoreLogPolicyUsesNodeWideBudget(t *testing.T) {
	t.Parallel()
	collector := &CoreLogCollector{
		coreLogMaxBytes: 1 << 20, coreLogRotateCount: 1,
		fileSources: []coreLogFileSource{{}, {}},
	}
	bytes, rotations := collector.rotationPolicy()
	if bytes != 256<<10 || rotations != 1 {
		t.Fatalf("rotation policy = %d bytes/%d rotations, want %d/1", bytes, rotations, 256<<10)
	}
}
