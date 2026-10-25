//go:build linux

package serverconfig

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Exercise the pinned 1.25.0 binary with loopback-only destinations, including
// an IPv4 client accepted by the same dual-stack listener used on ATT.
func TestSSRustNativeBindingFamilies(t *testing.T) {
	binaries := os.Getenv("QCH_SS_RUST_NATIVE_BIN")
	if binaries == "" {
		t.Skip("requires make ss-rust-runtime-test")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Geteuid() == 0 {
		t.Fatal("native test requires a non-root Docker container")
	}
	for _, tc := range []struct {
		name, global, override, target, wantPeer string
		ipv6First                                bool
	}{
		{"global-v6-to-v4", "::1", "", "127.0.0.1", "", false},
		{"port-v6-to-v4", "127.0.0.2", "::1", "127.0.0.1", "", false},
		{"ipv6-first-still-v4-literal", "::1", "", "127.0.0.1", "", true},
		{"global-v6-to-v6", "::1", "", "::1", "::1", false},
		{"port-v6-to-v6", "127.0.0.2", "::1", "::1", "::1", true},
		{"global-v4-to-v4", "127.0.0.2", "", "127.0.0.1", "127.0.0.2", false},
		{"port-v4-to-v4", "::1", "127.0.0.2", "127.0.0.1", "127.0.0.2", false},
		{"mapped-v4-to-v4", "::1", "::ffff:127.0.0.2", "127.0.0.1", "127.0.0.2", false},
		{"unbound-v4", "", "", "127.0.0.1", "127.0.0.1", false},
		{"unbound-v6", "", "", "::1", "::1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			serverPort := nativeSSRustPort(t)
			entry := map[string]any{"server": "::", "server_port": serverPort, "method": "aes-256-gcm", "password": "isolated-bind-test"}
			server := map[string]any{"mode": "tcp_and_udp", "ipv6_first": tc.ipv6First, "servers": []any{entry}}
			if tc.global != "" {
				server["outbound_bind_addr"] = tc.global
			}
			if tc.override != "" {
				entry["outbound_bind_addr"] = tc.override
			}
			logPath := nativeSSRustStart(t, root, filepath.Join(binaries, "ssserver"), "-c", nativeSSRustConfig(t, root, "server.json", server))
			nativeSSRustWait(t, serverPort)
			for _, network := range []string{"tcp", "udp"} {
				t.Run(network, func(t *testing.T) {
					target, err := net.ListenPacket("udp", net.JoinHostPort(tc.target, "0"))
					if err != nil {
						t.Fatal(err)
					}
					defer target.Close()
					address := target.LocalAddr().String()
					var listener net.Listener
					if network == "tcp" {
						listener, err = net.Listen("tcp", net.JoinHostPort(tc.target, "0"))
						if err != nil {
							t.Fatal(err)
						}
						address = listener.Addr().String()
						httpServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							host, _, _ := net.SplitHostPort(r.RemoteAddr)
							fmt.Fprint(w, host)
						}), ReadHeaderTimeout: time.Second}
						defer httpServer.Close()
						go httpServer.Serve(listener)
					} else {
						go func() {
							buffer := make([]byte, 32)
							for {
								_, peer, err := target.ReadFrom(buffer)
								if err != nil {
									return
								}
								host, _, _ := net.SplitHostPort(peer.String())
								_, _ = target.WriteTo([]byte(host), peer)
							}
						}()
					}
					localPort := nativeSSRustPort(t)
					local := net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))
					client := nativeSSRustConfig(t, root, "client-"+network+".json", map[string]any{"server": "127.0.0.1", "server_port": serverPort, "method": "aes-256-gcm", "password": "isolated-bind-test"})
					args := []string{"-c", client, "--protocol", "tunnel", "--local-addr", local, "--forward-addr", address}
					if network == "udp" {
						args = append(args, "-U")
					}
					nativeSSRustStart(t, root, filepath.Join(binaries, "sslocal"), args...)
					nativeSSRustWait(t, localPort)
					conn, err := net.DialTimeout(network, local, time.Second)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(time.Second))
					var body []byte
					if network == "tcp" {
						_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
						if err == nil {
							var response *http.Response
							response, err = http.ReadResponse(bufio.NewReader(conn), nil)
							if err == nil {
								defer response.Body.Close()
								body, err = io.ReadAll(response.Body)
							}
						}
					} else {
						for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
							_ = conn.SetDeadline(time.Now().Add(100 * time.Millisecond))
							_, err = conn.Write([]byte("echo"))
							if err != nil {
								break
							}
							buffer := make([]byte, 64)
							var n int
							n, err = conn.Read(buffer)
							body = buffer[:n]
							if err == nil {
								break
							}
						}
					}
					if tc.wantPeer != "" {
						if err != nil || string(body) != tc.wantPeer {
							t.Fatalf("outbound peer = %q, error = %v; want %s", body, err, tc.wantPeer)
						}
					} else {
						if err == nil {
							t.Fatalf("unexpected cross-family delivery: %q", body)
						}
						want := "bind_local_addr is not a valid IPv4-mapped IPv6 address"
						if network == "udp" {
							// Linux binds a dual-stack UDP socket; the attempted
							// mapped destination then fails during send instead.
							want = "ERROR udp relay"
						}
						logs, _ := os.ReadFile(logPath)
						if !strings.Contains(string(logs), want) {
							t.Fatalf("missing native error %q: %s", want, logs)
						}
						for _, line := range strings.Split(string(logs), "\n") {
							if strings.Contains(line, want) {
								t.Log(line)
								break
							}
						}
					}
				})
			}
		})
	}
}
