//go:build linux

package agent

import (
	"bytes"
	"context"
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

func TestCoreLogRotationBoundsOversizedSnapshotToPanelBudget(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sing-box.log")
	content := bytes.Repeat([]byte("0123456789abcdef"), 128<<10)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	collector := &CoreLogCollector{
		coreLogMaxBytes: 1 << 20, coreLogRotateCount: 1,
		fileSources: []coreLogFileSource{{path: path, kind: "file"}},
	}
	collector.rotateFile(collector.fileSources[0])
	live, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if live.Size() != 0 {
		t.Fatalf("live log after rotation = %d bytes, want 0", live.Size())
	}
	archive, err := os.ReadFile(path + ".old")
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, _ := collector.rotationPolicy()
	if int64(len(archive)) != wantBytes || !bytes.Equal(archive, content[len(content)-int(wantBytes):]) {
		t.Fatalf("archive size/tail = %d bytes, want bounded %d-byte tail", len(archive), wantBytes)
	}
}

func TestCoreLogPolicyEnforcesUnchangedLimitAfterAgentRestart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "qagent-sing-box.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	source := coreLogFileSource{path: path, root: root, engine: core.EngineSingBox, kind: "openrc"}
	collector := &CoreLogCollector{
		coreLogMaxBytes: 1 << 20, coreLogRotateCount: 1,
		fileSources: []coreLogFileSource{source},
	}
	validated, err := openValidatedCoreLogFileContext(context.Background(), source)
	if err != nil {
		t.Fatalf("validate staged cache: %v", err)
	}
	_ = validated.file.Close()
	collector.ApplyPolicy(core.AgentPolicy{CoreLogMaxMiB: 1, CoreLogRotateCount: 1})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("oversized cache survived unchanged policy: %d bytes", info.Size())
	}
}
