package serverconfig

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// Import through the real subscription parser and native YAML, using the test
// CA through the normal trust store. No certificate checks are disabled.
func testWebSocketMihomoImport(t *testing.T, plan Input, address, certPath string, uri bool) {
	t.Helper()
	binary := nativeWSTestBinary(t, core.EngineMihomo)
	plan.TLSClientCertificate = ""
	profile, err := BuildClientProfile(plan, address, "")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	echo := singBoxEcho(t, "tcp")
	port := singBoxFreePort(t)
	root := map[string]any{"mixed-port": port, "bind-address": "127.0.0.1", "mode": "rule", "log-level": "info"}
	if uri {
		source := filepath.Join(dir, "subscription.txt")
		if err := os.WriteFile(source, []byte(profile.URI+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		root["proxy-providers"] = map[string]any{"export": map[string]any{"type": "file", "path": source}}
		root["proxy-groups"] = []any{map[string]any{"name": "imported", "type": "select", "use": []string{"export"}}}
		root["rules"] = []string{"MATCH,imported"}
	} else {
		var proxy map[string]any
		if err := yaml.Unmarshal([]byte(profile.Mihomo), &proxy); err != nil {
			t.Fatal(err)
		}
		root["proxies"] = []any{proxy}
		root["rules"] = []string{"MATCH," + proxy["name"].(string)}
	}
	content, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "mihomo.json")
	if err := os.WriteFile(config, content, 0600); err != nil {
		t.Fatal(err)
	}
	check := exec.Command(binary, "-t", "-d", dir, "-f", config)
	check.Env = append(os.Environ(), "SSL_CERT_FILE="+certPath)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("Mihomo import: %v\n%s", err, output)
	}
	output, err := os.Create(filepath.Join(dir, "core.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-d", dir, "-f", config)
	command.Env = check.Env
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		output.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("Mihomo did not exit")
		}
		_ = output.Close()
	})
	var last error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		last = func() error {
			conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
			if err != nil {
				return err
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			target := net.JoinHostPort("127.0.0.1", strconv.Itoa(echo))
			if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
				return err
			}
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
			if err != nil {
				return err
			}
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("CONNECT: %s", response.Status)
			}
			payload := "qch-native-ws-import"
			if _, err := io.WriteString(conn, payload); err != nil {
				return err
			}
			reply := make([]byte, len(payload))
			if _, err := io.ReadFull(reader, reply); err != nil {
				return err
			}
			if string(reply) != payload {
				return fmt.Errorf("payload mismatch")
			}
			return nil
		}()
		if last == nil {
			return
		}
	}
	logs, _ := os.ReadFile(filepath.Join(dir, "core.log"))
	t.Fatalf("Mihomo imported export failed: %v\n%s", last, logs)
}
