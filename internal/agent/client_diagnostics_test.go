package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestFullTaskDiagnosticSurvivesCacheRestartAndRetransmission(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	id := "tsk_0123456789abcdef"
	original := core.TaskResultRequest{Error: "validation failed\n" + strings.Repeat("中文嵌套原因_", 15000) + "\nrollback: failed\npassword=private-fixture", Output: strings.Repeat("output_no_spaces_", 12000) + "\noutput-tail"}
	client := &Client{config: ClientConfig{StatePath: filepath.Join(dir, "state.json")}, creds: credentials{AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(key)}}
	result := original
	if err = client.persistDiagnostic(id, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Output) > 5<<10 || len(result.Error) > 3<<10 || result.DiagnosticDigest == "" {
		t.Fatal("unbounded summary cache")
	}
	if err = client.rememberTaskResult(id, result); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadCredentials(client.config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var chunks []string
	var digest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/agent/v1/tasks/"+id+"/diagnostics" {
			t.Error("incorrect diagnostic target")
			w.WriteHeader(400)
			return
		}
		var c core.TaskDiagnosticChunk
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if c.LeaseID != "replacement-lease-0123456789abcdef" || c.Index != len(chunks) {
			t.Error("retransmission lost attempt lease or chunk ordering")
		}
		digest = c.Digest
		chunks = append(chunks, c.Content)
		w.WriteHeader(204)
	}))
	defer server.Close()
	restarted := &Client{config: ClientConfig{StatePath: client.config.StatePath, ServerURL: server.URL}, creds: loaded, http: server.Client()}
	cached, ok := restarted.cachedTaskResult(core.Task{ID: id, LeaseID: "replacement-lease-0123456789abcdef"})
	if !ok {
		t.Fatal("restart lost cached result")
	}
	if err = restarted.uploadDiagnostic(context.Background(), id, cached); err != nil {
		t.Fatal(err)
	}
	encoded := []byte(strings.Join(chunks, ""))
	sum := sha256.Sum256(encoded)
	if digest != hex.EncodeToString(sum[:]) || len(chunks) < 3 {
		t.Fatal("diagnostic integrity manifest is incorrect")
	}
	var full core.TaskDiagnostic
	if err = json.Unmarshal(encoded, &full); err != nil {
		t.Fatal(err)
	}
	if full.Output != core.DiagnosticText(original.Output) || full.Error != core.DiagnosticText(original.Error) || strings.Contains(full.Error, "private-fixture") {
		t.Fatal("restart or redaction lost full diagnostic")
	}
	// Reconnection uses the same durable bytes and the newly leased identity.
	chunks = nil
	if err = restarted.uploadDiagnostic(context.Background(), id, cached); err != nil {
		t.Fatal(err)
	}
	if strings.Join(chunks, "") != string(encoded) {
		t.Fatal("retransmission changed diagnostic")
	}
	if err = os.WriteFile(client.config.StatePath+".diagnostics/"+id+".json", []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = restarted.uploadDiagnostic(context.Background(), id, cached); err == nil {
		t.Fatal("modified spool was accepted")
	}
}

func TestTaskDiagnosticAcquisitionLimitIsExplicit(t *testing.T) {
	value := boundedDiagnostic(strings.Repeat("多", (core.MaxTaskDiagnosticBytes/2)/3+100))
	if !strings.Contains(value, "此记录不完整") || len(value) > core.MaxTaskDiagnosticBytes/2+512 {
		t.Fatal("acquisition limit silently discarded content or was unbounded")
	}
}
