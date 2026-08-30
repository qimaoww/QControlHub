//go:build linux

package agent

import (
	"os"
	"path/filepath"
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

func TestOpenRCCoreLogPolicyWithoutArchivesTruncatesInPlace(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "qagent-mihomo.log")
	if err := os.WriteFile(path, []byte("bounded log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	collector := &CoreLogCollector{coreLogMaxBytes: 1 << 20, coreLogRotateCount: 0}
	collector.rotateFile(coreLogFileSource{path: path, kind: "openrc"})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("live log size = %d, want 0", info.Size())
	}
	if _, err := os.Stat(path + ".old"); !os.IsNotExist(err) {
		t.Fatalf("zero-archive policy created a snapshot: %v", err)
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
