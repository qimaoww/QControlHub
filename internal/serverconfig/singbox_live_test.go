package serverconfig

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
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

func TestSingBoxFinalInboundDeletion(t *testing.T) {
	binary := os.Getenv("QCH_SING_BOX_BINARY")
	if binary == "" {
		t.Skip("QCH_SING_BOX_BINARY is not set")
	}
	protocol, _ := FindProtocol(core.EngineSingBox, "vless-ws-tls")
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.TLSServerName = "tls.example.test"
	content, err := Generate(core.EngineSingBox, input)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanPresetAccounting(core.EngineSingBox, content)
	if err != nil {
		t.Fatal(err)
	}
	content, err = PresetAccountingSource(core.EngineSingBox, plan.Content)
	if err == nil {
		content, err = DeletePresetInbound(core.EngineSingBox, content, input.Tag)
	}
	if err == nil {
		content, err = ReconcilePresetInboundReferences(core.EngineSingBox, content, input.Tag, "")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, shared := range []bool{false, true} {
		name, runtime := "ordinary", content
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			if shared {
				var err error
				runtime, err = SharedInstanceRuntimeContent(core.EngineSingBox, runtime)
				if err != nil {
					t.Fatal(err)
				}
			}
			endpoints, err := SharedTrafficEndpoints(core.EngineSingBox, runtime)
			if err != nil || len(endpoints) != 0 || strings.Contains(runtime, `"v2ray_api"`) {
				t.Fatalf("empty runtime has ingress or statistics API: %s %v", runtime, err)
			}
			log := singBoxRun(t, binary, "empty-"+name+"-instance", runtime)
			if strings.Contains(log(), "inbound/") {
				t.Fatalf("empty instance started an inbound: %s", log())
			}
		})
	}
}

// Opt-in, hermetic network matrix: official sing-box on both sides, local
// TCP/UDP echo destinations and a local TLS 1.3 Reality handshake target.
// CI runs this against the same verified official binary as permission tests.
func TestSingBoxUpstreamLiveMatrix(t *testing.T) {
	binary := os.Getenv("QCH_SING_BOX_BINARY")
	if binary == "" {
		t.Skip("QCH_SING_BOX_BINARY is not set")
	}
	version, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, version)
	}
	t.Log(strings.TrimSpace(string(version)))
	certificate, certPath, keyPath := singBoxTestCertificate(t)
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		for {
			conn, err := target.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	targetPort := target.Addr().(*net.TCPAddr).Port
	for _, protocol := range Protocols(core.EngineSingBox) {
		if protocol.UsesEndpoint || protocol.PortForward {
			continue
		}
		methods := protocol.Methods
		if len(methods) == 0 {
			methods = []string{""}
		}
		for _, method := range methods {
			t.Run(protocol.Key+"/"+method, func(t *testing.T) {
				input, err := nativeTLSFixturePlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				if method != "" {
					input.Method = method
					input.Credential, err = NewCredential(input.Protocol, method)
					if err != nil {
						t.Fatal(err)
					}
				}
				input.Listen = "127.0.0.1"
				input.Port = singBoxFreePort(t)
				if input.TLSEnabled {
					input.CertificatePath = certPath
					input.PrivateKeyPath = keyPath
					input.TLSServerName = "tls.example.test"
					input.TLSClientCertificate = certificate
				}
				if input.Transport == "http" || input.Transport == "httpupgrade" || input.Transport == "websocket" {
					input.TransportHost = "http.example.test"
				}
				if input.Transport == "http" {
					input.TransportHTTPMethod = "POST"
				}
				server, err := Generate(core.EngineSingBox, input)
				if err != nil {
					t.Fatal(err)
				}
				parsed, ok := Parse(core.EngineSingBox, server)
				if !ok {
					t.Fatal("generated source not readable")
				}
				metadata, err := MarshalClientMetadata(input)
				if err != nil {
					t.Fatal(err)
				}
				if err := ApplyClientMetadata(&parsed, metadata); err != nil {
					t.Fatal(err)
				}
				outbound, err := BuildClientOutbound(core.EngineSingBox, parsed, "127.0.0.1", "")
				if err != nil {
					t.Fatal(err)
				}
				profile, err := BuildClientProfile(parsed, "127.0.0.1", "")
				if err != nil {
					t.Fatal(err)
				}
				if profile.Format == "sing-box client JSON" {
					var native map[string]any
					if err := json.Unmarshal([]byte(profile.URI), &native); err != nil {
						t.Fatal(err)
					}
					exported, _ := json.Marshal(firstMap(native["outbounds"]))
					if !bytes.Equal(exported, outbound) {
						t.Fatal("profile and outbound differ")
					}
				}
				if input.RealityEnabled {
					// Only the external handshake destination is redirected to a
					// local TLS fixture; generated SNI, flow, keys and transport stay.
					var root map[string]any
					_ = json.Unmarshal([]byte(server), &root)
					reality := mapValue(mapValue(firstMap(root["inbounds"])["tls"])["reality"])
					reality["handshake"] = map[string]any{"server": "127.0.0.1", "server_port": targetPort}
					encoded, _ := json.Marshal(root)
					server = string(encoded)
				}
				serverLog := singBoxRun(t, binary, "server", server)
				for _, network := range []string{"tcp", "udp"} {
					t.Run(network, func(t *testing.T) {
						echoPort := singBoxEcho(t, network)
						clientPort := singBoxFreePort(t)
						var out map[string]any
						_ = json.Unmarshal(outbound, &out)
						client, _ := json.Marshal(map[string]any{"log": map[string]any{"level": "debug"}, "inbounds": []any{map[string]any{"type": "direct", "tag": "local", "listen": "127.0.0.1", "listen_port": clientPort, "network": network, "override_address": "127.0.0.1", "override_port": echoPort}}, "outbounds": []any{out}, "route": map[string]any{"final": "peer"}})
						clientLog := singBoxRun(t, binary, "client-"+network, string(client))
						if err := singBoxEchoRoundTrip(network, clientPort); err != nil {
							t.Fatalf("%v\nserver:\n%s\nclient:\n%s", err, serverLog(), clientLog())
						}
					})
				}
				// A wrong password/UUID must not be usable through the exported
				// client. Exercise representative authentication implementations.
				if input.Protocol == "vmess-tcp" || input.Protocol == ProtocolSocks || input.Protocol == ProtocolTrojan || input.Protocol == ProtocolVLESSHTTP2Reality {
					t.Run("reject-wrong-credential", func(t *testing.T) {
						parsed.Credential, _ = NewCredential(parsed.Protocol, parsed.Method)
						bad, _ := BuildClientOutbound(core.EngineSingBox, parsed, "127.0.0.1", "")
						port := singBoxFreePort(t)
						echo := singBoxEcho(t, "tcp")
						var out map[string]any
						_ = json.Unmarshal(bad, &out)
						client, _ := json.Marshal(map[string]any{"inbounds": []any{map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": port, "network": "tcp", "override_address": "127.0.0.1", "override_port": echo}}, "outbounds": []any{out}})
						singBoxRun(t, binary, "bad-client", string(client))
						if err := singBoxEchoRoundTrip("tcp", port); err == nil {
							t.Fatal("wrong credential connected")
						}
					})
				}
			})
		}
	}
}

func TestSingBoxTLSVerificationAndRenewal(t *testing.T) {
	binary := os.Getenv("QCH_SING_BOX_BINARY")
	if binary == "" {
		t.Skip("QCH_SING_BOX_BINARY is not set")
	}
	certificate, certPath, keyPath := singBoxTestCertificate(t)
	protocol, _ := FindProtocol(core.EngineSingBox, "trojan-httpupgrade-tls")
	input, err := nativeTLSFixturePlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.Listen = "127.0.0.1"
	input.Port = singBoxFreePort(t)
	input.CertificatePath = certPath
	input.PrivateKeyPath = keyPath
	input.TLSServerName = "tls.example.test"
	input.TLSClientCertificate = certificate
	server, err := Generate(core.EngineSingBox, input)
	if err != nil {
		t.Fatal(err)
	}
	serverLog := singBoxRun(t, binary, "server", server)
	connect := func(t *testing.T, plan Input, want bool) {
		t.Helper()
		out, err := BuildClientOutbound(core.EngineSingBox, plan, "127.0.0.1", "")
		if err != nil {
			t.Fatal(err)
		}
		var outbound map[string]any
		_ = json.Unmarshal(out, &outbound)
		port, echo := singBoxFreePort(t), singBoxEcho(t, "tcp")
		client, _ := json.Marshal(map[string]any{"inbounds": []any{map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": port, "network": "tcp", "override_address": "127.0.0.1", "override_port": echo}}, "outbounds": []any{outbound}})
		clientLog := singBoxRun(t, binary, "tls-client", string(client))
		err = singBoxEchoRoundTrip("tcp", port)
		if (err == nil) != want {
			t.Fatalf("connection success=%v, want=%v: %v\nserver: %s\nclient: %s", err == nil, want, err, serverLog(), clientLog())
		}
	}
	t.Run("trusted", func(t *testing.T) { connect(t, input, true) })
	t.Run("wrong-sni", func(t *testing.T) { bad := input; bad.TLSServerName = "wrong.example.test"; connect(t, bad, false) })
	t.Run("untrusted", func(t *testing.T) { bad := input; bad.TLSClientCertificate = ""; connect(t, bad, false) })
	t.Run("renew-certificate-and-key", func(t *testing.T) {
		newCertificate, newCertPath, newKeyPath := singBoxTestCertificate(t)
		for _, pair := range [][2]string{{newKeyPath, keyPath}, {newCertPath, certPath}} {
			data, err := os.ReadFile(pair[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pair[1]+".renew", data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(pair[1]+".renew", pair[1]); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(1200 * time.Millisecond)
		renewed := input
		renewed.TLSClientCertificate = newCertificate
		connect(t, renewed, true)
	})
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

func singBoxTestCertificate(t *testing.T) (string, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "native preset test"}, DNSNames: []string{"tls.example.test", DefaultRealityServerName}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, publicPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, privatePEM, 0600); err != nil {
		t.Fatal(err)
	}
	return string(publicPEM), certPath, keyPath
}
