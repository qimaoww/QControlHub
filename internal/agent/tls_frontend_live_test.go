package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestManagedTLSProxyLiveMatrix(t *testing.T) {
	binary := os.Getenv("QCH_SING_BOX_BINARY")
	if binary == "" {
		t.Skip("requires actual sing-box and proxy binaries")
	}
	for _, mode := range []string{"caddy", "nginx"} {
		proxy := os.Getenv("QCH_" + strings.ToUpper(mode) + "_BINARY")
		if proxy == "" {
			t.Skip("requires proxy binary")
		}
		for _, base := range []string{"vmess", "vless", "trojan"} {
			for _, transport := range []string{"ws", "httpupgrade", "h2"} {
				if mode == "nginx" && transport == "h2" {
					continue
				}
				t.Run(mode+"/"+base+"/"+transport, func(t *testing.T) {
					protocol, _ := serverconfig.FindProtocol(core.EngineSingBox, base+"-"+transport+"-tls")
					input, err := serverconfig.NewPlan(protocol)
					if err != nil {
						t.Fatal(err)
					}
					// Use the panel-generated self-signed certificate as an isolated TLS fixture
					// for both actual proxies. Public ACME requires a publicly delegated domain.
					input.TLSMode = "nginx"
					if transport == "h2" {
						input.TLSMode = "caddy"
					}
					input.Port = singBoxFreePort(t)
					input.TLSPublicPort = singBoxFreePort(t)
					input.TLSServerName = "tls.example.test"
					input.TransportHost = "different-host.example.test"
					content, err := serverconfig.Generate(core.EngineSingBox, input)
					if err != nil {
						t.Fatal(err)
					}
					encoded, err := serverconfig.MarshalClientMetadataWithPrevious(input, "")
					if err != nil {
						t.Fatal(err)
					}
					frontends, err := serverconfig.ManagedTLSFrontends(core.EngineSingBox, content, map[string]string{input.Tag: encoded})
					if err != nil {
						t.Fatal(err)
					}
					parsed, ok := serverconfig.Parse(core.EngineSingBox, content)
					if !ok {
						t.Fatal("parse")
					}
					if err = serverconfig.ApplyClientMetadata(&parsed, encoded); err != nil {
						t.Fatal(err)
					}
					outbound, err := serverconfig.BuildClientOutbound(core.EngineSingBox, parsed, "127.0.0.1", "")
					if err != nil {
						t.Fatal(err)
					}
					var serverRoot map[string]any
					_ = json.Unmarshal([]byte(content), &serverRoot)
					serverRoot["route"] = map[string]any{"rules": []any{map[string]any{"source_ip_cidr": []string{"198.51.100.9/32"}, "action": "reject"}}}
					checked, _ := json.Marshal(serverRoot)
					serverLog := singBoxRun(t, binary, "backend", string(checked))
					dir := t.TempDir()
					f := frontends[0]
					if transport == "h2" {
						fixture := input
						fixture.Protocol, fixture.Transport, fixture.TLSMode = base+"-ws-tls", "websocket", "nginx"
						local, err := serverconfig.MarshalClientMetadataWithPrevious(fixture, "")
						if err != nil {
							t.Fatal(err)
						}
						var metadata struct {
							Frontend *core.TLSFrontend `json:"tls_frontend"`
						}
						_ = json.Unmarshal([]byte(local), &metadata)
						f.Certificate, f.PrivateKey = metadata.Frontend.Certificate, metadata.Frontend.PrivateKey
						var out map[string]any
						_ = json.Unmarshal(outbound, &out)
						out["tls"].(map[string]any)["certificate"] = []string{f.Certificate}
						outbound, _ = json.Marshal(out)
					}
					f.Mode = mode
					cert := filepath.Join(dir, frontendKey(f)+".crt")
					key := filepath.Join(dir, frontendKey(f)+".key")
					_ = os.WriteFile(cert, []byte(f.Certificate), 0600)
					_ = os.WriteFile(key, []byte(f.PrivateKey), 0600)
					config := renderNginxFrontends([]core.TLSFrontend{f}, dir, dir)
					args := []string{"-p", dir + "/", "-c", filepath.Join(dir, "proxy.conf")}
					if mode == "caddy" {
						config, err = renderCaddyFrontends([]core.TLSFrontend{f}, dir)
						if err != nil {
							t.Fatal(err)
						}
						production := filepath.Join(dir, "production.json")
						_ = os.WriteFile(production, []byte(config), 0600)
						if output, err := exec.Command(proxy, "validate", "--config", production).CombinedOutput(); err != nil {
							t.Fatalf("production Caddy schema invalid: %v: %s", err, output)
						}
						var root map[string]any
						_ = json.Unmarshal([]byte(config), &root)
						apps := root["apps"].(map[string]any)
						apps["tls"] = map[string]any{"certificates": map[string]any{"load_files": []any{map[string]any{"certificate": cert, "key": key}}}}
						for _, server := range apps["http"].(map[string]any)["servers"].(map[string]any) {
							server.(map[string]any)["automatic_https"] = map[string]any{"disable": true}
						}
						data, _ := json.Marshal(root)
						config = string(data)
						args = []string{"run", "--config", filepath.Join(dir, "proxy.conf")}
					}
					path := filepath.Join(dir, "proxy.conf")
					_ = os.WriteFile(path, []byte(config), 0600)
					checkArgs := []string{"-t", "-p", dir + "/", "-c", path}
					if mode == "caddy" {
						checkArgs = []string{"validate", "--config", path}
					}
					if output, err := exec.Command(proxy, checkArgs...).CombinedOutput(); err != nil {
						t.Fatalf("proxy configuration invalid: %v: %s", err, output)
					}
					log, err := os.Create(filepath.Join(dir, "proxy.log"))
					if err != nil {
						t.Fatal(err)
					}
					command := exec.Command(proxy, args...)
					command.Stdout = log
					command.Stderr = log
					if err = command.Start(); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait(); _ = log.Close() })
					deadline := time.Now().Add(5 * time.Second)
					for {
						conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(f.PublicPort)), 100*time.Millisecond)
						if err == nil {
							conn.Close()
							break
						}
						if time.Now().After(deadline) {
							data, _ := os.ReadFile(filepath.Join(dir, "proxy.log"))
							t.Fatalf("proxy did not listen: %s", data)
						}
						time.Sleep(20 * time.Millisecond)
					}
					pool := x509.NewCertPool()
					pool.AppendCertsFromPEM([]byte(f.Certificate))
					alpn := []string{"http/1.1"}
					if transport == "h2" {
						alpn = []string{"h2"}
					}
					if err := probeFrontendProtocol(context.Background(), f, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: f.Domain, RootCAs: pool, NextProtos: alpn}); err != nil {
						t.Fatalf("readiness handshake: %v", err)
					}
					for _, network := range []string{"tcp", "udp"} {
						t.Run(network, func(t *testing.T) {
							echo := singBoxEcho(t, network)
							port := singBoxFreePort(t)
							var out map[string]any
							_ = json.Unmarshal(outbound, &out)
							out["transport"].(map[string]any)["headers"] = map[string]any{"X-Forwarded-For": "198.51.100.9"}
							client, _ := json.Marshal(map[string]any{"log": map[string]any{"level": "debug"}, "inbounds": []any{map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": port, "network": network, "override_address": "127.0.0.1", "override_port": echo}}, "outbounds": []any{out}})
							clientLog := singBoxRun(t, binary, "client", string(client))
							if err := singBoxEchoRoundTrip(network, port); err != nil {
								data, _ := os.ReadFile(filepath.Join(dir, "proxy.log"))
								t.Fatalf("%v\nserver: %s\nclient: %s\nproxy: %s", err, serverLog(), clientLog(), data)
							}
						})
					}
				})
			}
		}
	}
}

func singBoxRun(t *testing.T, binary, name, content string) func() string {
	t.Helper()
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	logPath := filepath.Join(dir, "output.log")
	if err := os.WriteFile(config, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "check", "-c", config).CombinedOutput(); err != nil {
		t.Fatalf("%s check: %v\n%s", name, err, output)
	}
	output, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "run", "-c", config)
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("core did not exit")
		}
		_ = output.Close()
	})
	readLog := func() string { data, _ := os.ReadFile(logPath); return string(data) }
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(readLog(), "sing-box started") {
			return readLog
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("%s exited: %v\n%s", name, err, readLog())
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s did not start: %s", name, readLog())
	return readLog
}

func singBoxFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func singBoxEcho(t *testing.T, network string) int {
	t.Helper()
	if network == "tcp" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				go func() {
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
					_, _ = io.Copy(conn, conn)
				}()
			}
		}()
		return listener.Addr().(*net.TCPAddr).Port
	}
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		data := make([]byte, 65535)
		for {
			n, addr, err := listener.ReadFrom(data)
			if err != nil {
				return
			}
			_, _ = listener.WriteTo(data[:n], addr)
		}
	}()
	return listener.LocalAddr().(*net.UDPAddr).Port
}

func singBoxEchoRoundTrip(network string, port int) error {
	conn, err := net.DialTimeout(network, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("qcontrolhub/native/" + network)
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if !bytes.Equal(reply, payload) {
		return fmt.Errorf("payload mismatch")
	}
	return nil
}
