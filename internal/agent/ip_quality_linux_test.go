//go:build linux

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func agentQualityReport(ip, organization string) string {
	return `{"Head":{"IP":"` + ip + `"},"Info":{"Organization":"` + organization + `"},"Type":{},"Score":{"IPQS":"null"},"Factor":{},"Media":{},"Mail":{}}`
}

func TestIPQualityExecutableChecksMetadata(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "bash")
	if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "sh")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if resolved, err := ipQualityExecutable(link); err != nil || resolved != path {
		t.Fatalf("safe system-style symlink: %q %v", resolved, err)
	}
	for _, mode := range []os.FileMode{0o600, 0o722} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := ipQualityExecutable(link); err == nil {
			t.Fatalf("accepted an unsafe executable with mode %o", mode)
		}
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := ipQualityExecutable(path); err == nil {
		t.Fatal("accepted an unprotected executable directory")
	}
}

func TestIPQualityProgramUsesOfficialOneLiner(t *testing.T) {
	for _, want := range []string{
		"curl -Ls https://IP.Check.Place",
		`-y -p -f -o "$1"`,
	} {
		if !strings.Contains(ipQualityProgram, want) {
			t.Fatalf("IPQuality program is missing %q", want)
		}
	}
	// Privacy mode keeps the detector from uploading to the upstream host; the
	// panel renders the image from the returned JSON instead.
	if strings.Contains(ipQualityProgram, "upload.check.place") || strings.Contains(ipQualityProgram, "QCH_IPQUALITY_LINKS") {
		t.Fatal("IPQuality program still exports an upstream report link")
	}
}

func TestIPQualityLevelsFromPrintedReport(t *testing.T) {
	// The Agent keeps upstream's printed report on stdout to recover the risk
	// wording ipapi and DB-IP return from their own APIs.
	printed := "\x1b[36mIP质量体检报告：203.0.113.1\x1b[0m\n" +
		"三、风险评分\n" +
		"IP2Location：\x1b[32m  3\x1b[0m|\x1b[32m低风险\x1b[0m\n" +
		"ipapi：\x1b[32m    0.00%\x1b[0m|\x1b[32m极低风险\x1b[0m\n" +
		"IP质量体检报告(Lite)：2001:db8::1\n" +
		"ipapi：\x1b[31m    5.47%\x1b[0m|\x1b[31m高风险\x1b[0m\n" +
		"DB-IP：\x1b[33m  40\x1b[0m|\x1b[33m中风险\x1b[0m\n"
	levels := parseIPQualityLevels(printed)
	if len(levels) != 2 || len(levels[0]) != 2 || len(levels[1]) != 2 {
		t.Fatalf("parsed levels = %+v", levels)
	}
	if levels[0]["IP2LOCATION"] != "低风险" || levels[0]["ipapi"] != "极低风险" ||
		levels[1]["ipapi"] != "高风险" || levels[1]["DBIP"] != "中风险" {
		t.Fatalf("parsed levels = %+v", levels)
	}
	if got := parseIPQualityLevels("no report here"); len(got) != 0 {
		t.Fatalf("parsed levels from empty output = %+v", got)
	}
}

func TestIPQualityScriptExecutionContract(t *testing.T) {
	bash := "/bin/bash"
	if _, err := os.Stat(bash); err != nil {
		t.Skip("requires bash")
	}
	report := agentQualityReport("203.0.113.1", "fixture")
	for _, test := range []struct {
		name, program string
		wantError     bool
	}{
		{"ipv4-final-status-one", `test -z "$QCH_IPQUALITY_SECRET" || exit 3
test -z "$HTTP_PROXY" || exit 4
test -z "$BASH_ENV" || exit 5
printf '%s\n' '` + report + `' > "$1"
exit 1`, false},
		{"dual-stack", `printf '%s\n' '` + report + `' '` + agentQualityReport("2001:db8::1", "fixture") + `' > "$1"`, false},
		{"missing-report", "exit 0", true},
		{"malformed-report", `printf 'not JSON' > "$1"`, true},
		{"partial-report", `printf '%s\n' '` + report + `' '{' > "$1"`, true},
		{"symlink-report", `ln -s /etc/passwd "$1"`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("QCH_IPQUALITY_SECRET", "must-not-leak")
			t.Setenv("HTTP_PROXY", "http://invalid.example.test")
			t.Setenv("BASH_ENV", "/nonexistent")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := executeIPQualityProgram(ctx, bash, test.program)
			if (err != nil) != test.wantError {
				t.Fatalf("%+v, %v", result, err)
			}
			if !test.wantError && len(result.Reports) < 1 {
				t.Fatal("successful result lost its report")
			}
		})
	}
}

func TestIPQualityCancellationAndBoundedReport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := executeIPQualityProgram(ctx, "/bin/bash", "sleep 20 &\nwait\n"); err == nil {
		t.Fatal("canceled command succeeded")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("cancellation did not terminate child processes")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "report.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", core.MaxIPQualityResultBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readIPQualityReport(directory); err == nil {
		t.Fatal("accepted oversized report")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readIPQualityReport(directory); err == nil {
		t.Fatal("accepted a FIFO as a report")
	}
}

func TestIPQualityResultStaysInMemoryAndIsNotPersisted(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	report, err := core.ParseIPQualityReports([]byte(agentQualityReport("203.0.113.1", strings.Repeat("a", 64<<10))))
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	client := &Client{
		config:        ClientConfig{StatePath: statePath},
		creds:         credentials{AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(key)},
		ipQualityFunc: func(context.Context) (core.IPQualityResult, error) { return report, nil },
	}
	task := core.Task{ID: "tsk_0123456789abcdef", AgentID: client.creds.AgentID,
		LeaseID: strings.Repeat("a", 32), Action: core.ActionIPQuality, Status: core.TaskRunning}
	if !client.validTask(task) {
		t.Fatal("rejected valid IPQuality task")
	}
	bad := task
	bad.ConfigContent = "must not execute"
	if client.validTask(bad) {
		t.Fatal("accepted configuration data on a quality task")
	}
	result := client.resultForTask(context.Background(), task)
	if !result.Success || result.IPQuality == nil {
		t.Fatalf("execution failed: %+v", result)
	}
	for i := range 12 {
		if err := client.rememberTaskResult(fmt.Sprintf("tsk_%016d", i), result); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := loadCredentials(statePath)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(statePath)
	if info.Size() >= 512<<10 {
		t.Fatal("reports exceeded the credentials read budget")
	}
	if len(loaded.CompletedTasks) != 0 {
		t.Fatal("IPQuality reports were persisted on the Agent")
	}
	contents, err := os.ReadFile(statePath)
	if err != nil || strings.Contains(string(contents), "203.0.113.1") {
		t.Fatal("Agent state contains report data")
	}
	restarted := client // Live reconnects still reuse the complete in-memory result.
	task.ID, task.LeaseID = "tsk_0000000000000011", strings.Repeat("b", 32)
	cached, ok := restarted.cachedTaskResult(task)
	if !ok || cached.LeaseID != task.LeaseID || cached.IPQuality == nil {
		t.Fatal("restart lost report or replayed the old lease")
	}
	want, _ := json.Marshal(report)
	got, _ := json.Marshal(cached.IPQuality)
	if string(got) != string(want) {
		t.Fatal("cached report was truncated or changed")
	}
}
