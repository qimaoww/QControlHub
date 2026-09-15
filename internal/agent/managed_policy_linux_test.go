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
	for _, expected := range []string{"Storage=volatile", "RuntimeMaxUse=524288", "RuntimeMaxFileSize=262144"} {
		if !strings.Contains(config, expected) {
			t.Errorf("journal policy missing %q:\n%s", expected, config)
		}
	}
}

func TestManagedCoreJournalPolicyRecognitionRejectsOldUnboundedAllocation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "10-qcontrolhub-volatile.conf")
	managed := managedCoreJournalConfigForPolicy(core.AgentPolicy{CoreLogMaxMiB: 16, CoreLogRotateCount: 1})
	if err := os.WriteFile(path, managed, 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := hasManagedCoreJournalPolicy(path); err != nil || !ok {
		t.Fatalf("current managed policy = %v, %v", ok, err)
	}
	legacy := strings.Replace(string(managed), "# qcontrolhub-node-budget-v2\n", "", 1)
	legacy = strings.Replace(legacy, "RuntimeMaxUse=8388608", "RuntimeMaxUse=16777216", 1)
	legacy = strings.Replace(legacy, "RuntimeMaxFileSize=4194304", "RuntimeMaxFileSize=8388608", 1)
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := hasManagedCoreJournalPolicy(path); err != nil || ok {
		t.Fatalf("old full-budget journal policy = %v, %v", ok, err)
	}
}

func TestCoreLogCollectorStartsBelowEveryValidPanelLimit(t *testing.T) {
	t.Parallel()
	collector := NewCoreLogCollector()
	if collector.coreLogMaxBytes != 1<<20 || collector.coreLogRotateCount != 0 {
		t.Fatalf("startup policy = %d bytes/%d snapshots, want 1 MiB/0", collector.coreLogMaxBytes, collector.coreLogRotateCount)
	}
}

func TestSystemdCoreLogBackendsShareNodeWideBudget(t *testing.T) {
	t.Parallel()
	collector := NewCoreLogCollector()
	collector.coreLogMaxBytes = 1 << 20
	collector.coreLogRotateCount = 1
	fileBytes, rotations := collector.rotationPolicy()
	if rotations != 1 {
		t.Fatalf("rotation count = %d, want 1", rotations)
	}
	fallbackBudget := fileBytes * int64(len(collector.fileSources)) * int64(rotations+1)
	journalBudget := int64(1 << 19)
	if total := fallbackBudget + journalBudget; total > 1<<20 {
		t.Fatalf("combined cache budget = %d, exceeds panel limit %d", total, 1<<20)
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

func TestSystemdFallbackRotationTruncatesWithoutLogBackup(t *testing.T) {
	previousRoot := systemdFallbackCoreLogRoot
	systemdFallbackCoreLogRoot = t.TempDir()
	t.Cleanup(func() { systemdFallbackCoreLogRoot = previousRoot })
	path, err := systemdFallbackCoreLogPath("qagent-xray.service")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	source := coreLogFileSource{path: path, root: systemdFallbackCoreLogRoot, engine: core.EngineXray, kind: "systemd-fallback"}
	collector := &CoreLogCollector{
		coreLogMaxBytes: 1 << 20, coreLogRotateCount: 1, fileBudgetDivisor: 2,
		fileSources: []coreLogFileSource{source},
	}
	collector.rotateFile(source)
	info, err := os.Stat(path)
	if err != nil || info.Size() != 0 {
		t.Fatalf("fallback cache after rotation = %+v, %v", info, err)
	}
	if _, err := os.Stat(path + ".old"); !os.IsNotExist(err) {
		t.Fatalf("fallback rotation retained a log backup: %v", err)
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
