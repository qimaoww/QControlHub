package serverconfig

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func nativeWSOriginRun(t *testing.T, engine core.Engine, binary, content string, port int, certPath string) func() string {
	t.Helper()
	if engine == core.EngineSingBox {
		return singBoxRun(t, binary, "native-ws-origin", content)
	}
	if engine == core.EngineXray {
		// New Xray releases default to bundled public roots. Keep verification
		// enabled and explicitly select the system store containing our test CA.
		var root map[string]any
		if err := json.Unmarshal([]byte(content), &root); err != nil {
			t.Fatal(err)
		}
		outbounds, _ := root["outbounds"].([]any)
		for _, item := range outbounds {
			outbound := item.(map[string]any)
			stream, _ := outbound["streamSettings"].(map[string]any)
			if security, ok := stream["tlsSettings"].(map[string]any); ok && certPath != "" {
				security["useSystemCA"] = true
			}
			if outbound["protocol"] == "freedom" {
				// The actual echo target is local; newer Xray blocks private
				// destinations by default. Permit only this fixture's loopback.
				settings, _ := outbound["settings"].(map[string]any)
				if settings == nil {
					settings = map[string]any{}
					outbound["settings"] = settings
				}
				settings["finalRules"] = []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.0/8", "::1/128"}}}
			}
		}
		encoded, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		content = string(encoded)
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "server.json")
	if err := os.WriteFile(config, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	checkArgs, runArgs := []string{"run", "-test", "-config", config}, []string{"run", "-config", config}
	if engine == core.EngineMihomo {
		checkArgs = []string{"-t", "-d", dir, "-f", config}
		runArgs = []string{"-d", dir, "-f", config}
	}
	if engine == core.EngineShadowsocksRust {
		checkArgs, runArgs = []string{"--version"}, []string{"-c", config}
	}
	check := exec.Command(binary, checkArgs...)
	check.Env = append(os.Environ(), "SAFE_PATHS="+filepath.Dir(certPath), "SSL_CERT_FILE="+certPath)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("%s rejected native WS: %v\n%s", engine, err, output)
	}
	logPath := filepath.Join(dir, "output.log")
	output, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, runArgs...)
	cmd.Env = check.Env
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("origin did not exit")
		}
		_ = output.Close()
	})
	readLog := func() string { data, _ := os.ReadFile(logPath); return string(data) }
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if engine == core.EngineXray {
			// Probing a tunnel client would itself start a second proxy request.
			if strings.Contains(readLog(), "started") {
				return readLog
			}
			continue
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return readLog
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("%s exited: %v\n%s", engine, err, readLog())
		default:
		}
	}
	t.Fatalf("%s origin did not listen: %s", engine, readLog())
	return readLog
}

func testWebSocketXrayImport(t *testing.T, plan Input, address, certPath string) {
	t.Helper()
	binary := nativeWSTestBinary(t, core.EngineXray)
	plan.TLSClientCertificate = ""
	out, err := BuildClientOutbound(core.EngineXray, plan, address, "")
	if err != nil {
		t.Fatal(err)
	}
	var outbound map[string]any
	if err := json.Unmarshal(out, &outbound); err != nil {
		t.Fatal(err)
	}
	echo := singBoxEcho(t, "tcp")
	port := singBoxFreePort(t)
	content, err := json.Marshal(map[string]any{"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "tunnel", "settings": map[string]any{"address": "127.0.0.1", "port": echo, "network": "tcp"}}}, "outbounds": []any{outbound}})
	if err != nil {
		t.Fatal(err)
	}
	logs := nativeWSOriginRun(t, core.EngineXray, binary, string(content), port, certPath)
	if err := singBoxEchoRoundTrip("tcp", port); err != nil {
		t.Fatalf("Xray outbound data failed: %v\n%s", err, logs())
	}
}
