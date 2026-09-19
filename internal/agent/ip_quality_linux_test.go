//go:build linux

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestIPQualityDownloadChecksContentBeforeExecution(t *testing.T) {
	content := "#!/bin/bash\nexit 0\n"
	digest := sha256.Sum256([]byte(content))
	checksum := hex.EncodeToString(digest[:])
	for _, test := range []struct {
		name, body, checksum string
		status               int
		wantError            bool
	}{
		{"valid", content, checksum, 200, false},
		{"changed", content + "# change", checksum, 200, true},
		{"html", "<html>error</html>", checksum, 200, true},
		{"http-error", content, checksum, 503, true},
		{"oversized", strings.Repeat("x", maxIPQualityScriptBytes+1), checksum, 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			got, err := downloadIPQualityScript(context.Background(), server.Client(), server.URL, test.checksum)
			if (err != nil) != test.wantError || err == nil && string(got) != test.body {
				t.Fatalf("download result %q, %v", got, err)
			}
		})
	}
}

func TestIPQualityScriptExecutionContract(t *testing.T) {
	bash := "/bin/bash"
	if _, err := os.Stat(bash); err != nil {
		t.Skip("requires bash")
	}
	report := agentQualityReport("203.0.113.1", "fixture")
	for _, test := range []struct {
		name, script string
		wantError    bool
	}{
		{"ipv4-final-status-one", `test "$*" = "-n -f -E -j -o $6" || exit 2
test -z "$QCH_IPQUALITY_SECRET" || exit 3
test -z "$HTTP_PROXY" || exit 4
test -z "$BASH_ENV" || exit 5
printf '%s\n' '` + report + `' > "$6"
exit 1`, false},
		{"dual-stack", `printf '%s\n' '` + report + `' '` + agentQualityReport("2001:db8::1", "fixture") + `' > "$6"`, false},
		{"missing-report", "exit 0", true},
		{"malformed-report", `printf 'not JSON' > "$6"`, true},
		{"partial-report", `printf '%s\n' '` + report + `' '{' > "$6"`, true},
		{"symlink-report", `ln -s /etc/passwd "$6"`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("QCH_IPQUALITY_SECRET", "must-not-leak")
			t.Setenv("HTTP_PROXY", "http://invalid.example.test")
			t.Setenv("BASH_ENV", "/nonexistent")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			script := test.script
			if !test.wantError {
				links := "203.0.113.1\thttps://Report.Check.Place/IP/fixture4.svg\n"
				if test.name == "dual-stack" {
					links += "2001:db8::1\thttps://Report.Check.Place/IP/fixture6.svg\n"
				}
				script = "printf '%s' '" + links + "' > \"$QCH_IPQUALITY_LINKS\"\n" + script
			}
			result, err := executeIPQualityScript(ctx, bash, []byte(script))
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
	if _, err := executeIPQualityScript(ctx, "/bin/bash", []byte("sleep 20 &\nwait\n")); err == nil {
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
