//go:build linux

package serverconfig

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Opt-in only: the target runs checksum-pinned official binaries in a
// non-root, network-isolated, read-only Docker container, never host services.
func TestSSRustNativeFieldScopes(t *testing.T) {
	binaries := os.Getenv("QCH_SS_RUST_NATIVE_BIN")
	if binaries == "" {
		t.Skip("requires make ss-rust-runtime-test")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Geteuid() == 0 {
		t.Fatal("native test requires a non-root Docker container")
	}
	root := t.TempDir()
	block := filepath.Join(root, "block.acl")
	empty := filepath.Join(root, "empty.acl")
	for path, value := range map[string]string{block: "[outbound_block_list]\n127.0.0.1\n", empty: "[outbound_block_list]\n"} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ports := []int{nativeSSRustPort(t), nativeSSRustPort(t), nativeSSRustPort(t)}
	server := map[string]any{"mode": "tcp_only", "acl": block, "outbound_bind_addr": "127.0.0.1", "servers": []any{
		map[string]any{"server": "127.0.0.1", "server_port": ports[0], "method": "aes-256-gcm", "password": "test-password-native", "acl": block},
		map[string]any{"server": "127.0.0.1", "server_port": ports[1], "method": "aes-256-gcm", "password": "test-password-native", "outbound_bind_addr": "127.0.0.2"},
		map[string]any{"server": "127.0.0.1", "server_port": ports[2], "method": "aes-256-gcm", "password": "test-password-native", "mode": "udp_only"},
	}}
	serverConfig := nativeSSRustConfig(t, root, "server.json", server)
	nativeSSRustStart(t, root, filepath.Join(binaries, "ssserver"), "-c", serverConfig, "--acl", empty)
	nativeSSRustWait(t, ports[0])
	nativeSSRustWait(t, ports[1])
	if conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[2])), 200*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("udp_only port unexpectedly accepts TCP")
	}
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.RemoteAddr) }))
	t.Cleanup(echo.Close)
	_, targetPort, _ := net.SplitHostPort(echo.Listener.Addr().String())
	for index, blocked := range []bool{true, false} {
		localPort := nativeSSRustPort(t)
		client := map[string]any{"server": "127.0.0.1", "server_port": ports[index], "method": "aes-256-gcm", "password": "test-password-native", "local_address": "127.0.0.1", "local_port": localPort}
		clientConfig := nativeSSRustConfig(t, root, fmt.Sprintf("client-%d.json", index), client)
		nativeSSRustStart(t, root, filepath.Join(binaries, "sslocal"), "-c", clientConfig)
		nativeSSRustWait(t, localPort)
		body, err := nativeSSRustHTTP(localPort, targetPort)
		if blocked && err == nil {
			t.Fatalf("port ACL did not block destination: %s", body)
		}
		if !blocked {
			if err != nil {
				t.Fatalf("CLI empty ACL did not override root ACL: %v", err)
			}
			host, _, _ := net.SplitHostPort(body)
			if host != "127.0.0.2" {
				t.Fatalf("port binding did not override global: %s", body)
			}
		}
	}
}

func nativeSSRustConfig(t *testing.T, root, name string, value any) string {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func nativeSSRustPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func nativeSSRustStart(t *testing.T, root, binary string, args ...string) {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Dir = root
	log, err := os.CreateTemp(root, "process-*.log")
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = log.Close()
		if t.Failed() {
			contents, _ := os.ReadFile(log.Name())
			t.Log(string(contents))
		}
	})
}

func nativeSSRustWait(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("native process did not listen on %d", port)
}

func nativeSSRustHTTP(localPort int, targetPort string) (string, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)), time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = conn.Write([]byte{5, 1, 0}); err != nil {
		return "", err
	}
	var greeting [2]byte
	if _, err = io.ReadFull(conn, greeting[:]); err != nil {
		return "", err
	}
	if greeting != [2]byte{5, 0} {
		return "", fmt.Errorf("SOCKS greeting: %v", greeting)
	}
	port, _ := strconv.Atoi(targetPort)
	if _, err = conn.Write([]byte{5, 1, 0, 1, 127, 0, 0, 1, byte(port >> 8), byte(port)}); err != nil {
		return "", err
	}
	var reply [10]byte
	if _, err = io.ReadFull(conn, reply[:]); err != nil {
		return "", err
	}
	if reply[1] != 0 {
		return "", fmt.Errorf("SOCKS reply: %v", reply)
	}
	if _, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		return "", err
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return string(body), err
}
