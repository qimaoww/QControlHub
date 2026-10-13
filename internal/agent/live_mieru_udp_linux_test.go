//go:build linux

package agent

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"golang.org/x/net/dns/dnsmessage"
	"gopkg.in/yaml.v3"
)

// This test needs a real systemd manager, root and an official Mihomo binary.
// Unlike a standalone core test, it reproduces the managed service's socket
// filter, including a negative control with the historical AF_NETLINK omission.
func TestMieruUDPWithSystemdSocketPolicy(t *testing.T) {
	if os.Getenv("QCH_LIVE_MIERU_SYSTEMD_TEST") != "1" {
		t.Skip("QCH_LIVE_MIERU_SYSTEMD_TEST is not enabled")
	}
	if os.Geteuid() != 0 {
		t.Fatal("systemd socket policy test requires root")
	}
	binary := filepath.Join(os.Getenv("QCH_LIVE_CORE_ROOT"), "bin", "mihomo")
	version, err := exec.Command(binary, "-v").CombinedOutput()
	if err != nil {
		t.Fatalf("Mihomo version: %v %s", err, version)
	}
	t.Log(strings.TrimSpace(string(version)))
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "mieru-tcp-ok") }))
	defer backend.Close()
	echo1, hits1 := mieruUDPBackend(t, "udp4", "127.0.0.1:0", func(b []byte) []byte { return append([]byte("echo-one:"), b...) })
	echo2, _ := mieruUDPBackend(t, "udp4", "127.0.0.1:0", func(b []byte) []byte { return append([]byte("echo-two:"), b...) })
	echo6, _ := mieruUDPBackend(t, "udp6", "[::1]:0", func(b []byte) []byte { return append([]byte("echo-six:"), b...) })
	dns, _ := mieruUDPBackend(t, "udp4", "127.0.0.1:0", func(b []byte) []byte {
		var query dnsmessage.Message
		if query.Unpack(b) != nil || len(query.Questions) != 1 {
			return nil
		}
		query.Response, query.Authoritative = true, true
		query.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 0}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 42}}}}
		response, _ := query.Pack()
		return response
	})
	tests := []struct {
		name, transport string
		families        []string
		blocked         bool
	}{
		{"historical/TCP", "TCP", []string{"AF_UNIX AF_INET AF_INET6"}, true},
		{"current/TCP", "TCP", []string{mieruUnitSocketFamilies(t, "qagent-mihomo.service")}, false},
		{"upgraded/TCP", "TCP", []string{"AF_UNIX AF_INET AF_INET6", "AF_NETLINK"}, false},
		{"shared/TCP", "TCP", []string{mieruUnitSocketFamilies(t, "qagent-mihomo@.service")}, false},
		{"current/UDP", "UDP", []string{mieruUnitSocketFamilies(t, "qagent-mihomo.service")}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolMieru)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Listen, input.Port, input.MieruTransport, input.MieruTrafficPattern = "127.0.0.1", availableTCPPort(t), test.transport, "balanced"
			if test.transport == "UDP" {
				p, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				input.Port = p.LocalAddr().(*net.UDPAddr).Port
				_ = p.Close()
			}
			generated, err := serverconfig.Generate(core.EngineMihomo, input)
			if err != nil {
				t.Fatal(err)
			}
			merged, err := serverconfig.MutateGenerated(core.EngineMihomo, "listeners: []\nrules: ['MATCH,DIRECT']\n", generated, "", "add")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := serverconfig.PrepareIndependentEgress(core.EngineMihomo, merged)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := serverconfig.Parse(core.EngineMihomo, plan.Content)
			if !ok {
				t.Fatal("deployed configuration did not parse")
			}
			profile, err := serverconfig.BuildClientProfileNamed(parsed, "127.0.0.1", "", "LIVE")
			if err != nil {
				t.Fatal(err)
			}
			var proxy, serverRoot map[string]any
			if err := yaml.Unmarshal([]byte(profile.Mihomo), &proxy); err != nil {
				t.Fatal(err)
			}
			if proxy["udp"] != true || proxy["transport"] != test.transport {
				t.Fatal("export lost UDP capability or selected transport")
			}
			if err := yaml.Unmarshal([]byte(plan.Content), &serverRoot); err != nil {
				t.Fatal(err)
			}
			ready, clientPort := availableTCPPort(t), availableTCPPort(t)
			serverRoot["external-controller"], serverRoot["ipv6"], serverRoot["udp-timeout"] = fmt.Sprintf("127.0.0.1:%d", ready), true, 1
			serverYAML, err := yaml.Marshal(serverRoot)
			if err != nil {
				t.Fatal(err)
			}
			clientYAML, err := yaml.Marshal(map[string]any{"mixed-port": clientPort, "ipv6": true, "mode": "rule", "log-level": "info", "udp-timeout": 1, "proxies": []any{proxy}, "proxy-groups": []any{map[string]any{"name": "TEST", "type": "select", "proxies": []string{"LIVE"}}}, "rules": []string{"MATCH,TEST"}})
			if err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			server, unit := startMieruSocketPolicyServer(t, binary, filepath.Join(directory, "server"), string(serverYAML), ready, test.families)
			client := startMihomoForTrafficTest(t, binary, filepath.Join(directory, "client"), "config.yaml", string(clientYAML), clientPort)
			t.Cleanup(func() { client.stop(t) })
			assertProxyTraffic(t, clientPort, backend.URL, "mieru-tcp-ok", func() string { return server.log(t) }, func() string { return client.log(t) })
			association := mieruSOCKSUDP(t, clientPort)
			request := []byte("negative-control")
			if test.blocked {
				before := hits1.Load()
				_, err := association.exchange(echo1, request, time.Second)
				if err == nil {
					t.Fatal("historical socket policy unexpectedly forwarded UDP")
				}
				if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
					t.Fatalf("negative control failed before UDP read: %v", err)
				}
				if hits1.Load() != before || !strings.Contains(server.log(t), "netlinkrib: address family not supported by protocol") {
					t.Fatalf("negative control did not reproduce server egress failure\n%s", server.log(t))
				}
				t.Log("TCP passed; UDP timed out before reaching the target with the historical netlink restriction")
				return
			}
			transfer := func(a *mieruUDPAssociation, target *net.UDPAddr, prefix string, body []byte) {
				t.Helper()
				got, err := a.exchange(target, body, 3*time.Second)
				if err != nil || !bytes.Equal(got, append([]byte(prefix), body...)) {
					t.Fatalf("UDP echo %s: %v\nserver: %s\nclient: %s", target, err, server.log(t), client.log(t))
				}
			}
			targets := []struct {
				addr   *net.UDPAddr
				prefix string
			}{{echo1, "echo-one:"}, {echo2, "echo-two:"}, {echo6, "echo-six:"}}
			for i := 0; i < 24; i++ {
				target := targets[i%len(targets)]
				body := append([]byte(fmt.Sprintf("%s-%d:", test.name, i)), bytes.Repeat([]byte{0, 255, 1, 2}, i*13+1)...)
				transfer(association, target.addr, target.prefix, body)
			}
			name, _ := dnsmessage.NewName(fmt.Sprintf("qch-%d.test.", time.Now().UnixNano()))
			query := dnsmessage.Message{Header: dnsmessage.Header{ID: 0x5143}, Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
			wire, _ := query.Pack()
			reply, err := association.exchange(dns, wire, 3*time.Second)
			var answer dnsmessage.Message
			if err != nil || answer.Unpack(reply) != nil || answer.ID != query.ID || !answer.Response || len(answer.Answers) != 1 || len(answer.Questions) != 1 {
				t.Fatalf("raw UDP DNS response: %v", err)
			}
			resource, ok := answer.Answers[0].Body.(*dnsmessage.AResource)
			if !ok || resource.A != [4]byte{192, 0, 2, 42} || answer.Questions[0].Name != name {
				t.Fatal("unexpected DNS answer")
			}
			// Both NAT mappings expire at one second; keep the SOCKS control channel
			// and UDP source socket open to prove the same association recovers.
			time.Sleep(2200 * time.Millisecond)
			transfer(association, echo1, "echo-one:", []byte("after-idle"))
			association.close()
			transfer(mieruSOCKSUDP(t, clientPort), echo2, "echo-two:", []byte("new-association"))
			assertProxyTraffic(t, clientPort, backend.URL, "mieru-tcp-ok", func() string { return server.log(t) }, func() string { return client.log(t) })
			if !mieruUDPLogUses(client.log(t), "TEST[LIVE]") || !mieruUDPLogUses(server.log(t), plan.Ports[0].Outbounds[0]) {
				t.Fatalf("traffic did not use the selected Mieru node and independent egress\n%s\n%s", client.log(t), server.log(t))
			}
			// A stopped tunnel cannot reach our live UDP backend: no DIRECT fallback.
			if output, err := exec.Command("systemctl", "stop", unit).CombinedOutput(); err != nil {
				t.Fatalf("stop test server: %v %s", err, output)
			}
			blocked := mieruSOCKSUDP(t, clientPort)
			before := hits1.Load()
			_, err = blocked.exchange(echo1, []byte("tunnel-stopped"), time.Second)
			if err == nil || hits1.Load() != before {
				t.Fatal("UDP reached the target with the Mieru server stopped")
			}
			t.Log("24 binary echoes across IPv4/IPv6 targets, raw UDP DNS, idle recovery, reconnect, TCP before/after, and stopped-tunnel control passed")
		})
	}
}

func mieruUDPLogUses(log, outbound string) bool {
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "[UDP]") && strings.Contains(line, "using "+outbound) {
			return true
		}
	}
	return false
}

func mieruUnitSocketFamilies(t *testing.T, service string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("../../deploy/systemd", service))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		if value, ok := strings.CutPrefix(line, "RestrictAddressFamilies="); ok {
			return value
		}
	}
	t.Fatal("managed unit has no socket policy")
	return ""
}

func startMieruSocketPolicyServer(t *testing.T, binary, directory, content string, ready int, families []string) (*trafficMihomoProcess, string) {
	t.Helper()
	unit := fmt.Sprintf("qch-mieru-test-%d-%d", os.Getpid(), ready)
	// Keep fixture access independent of PrivateTmp/ProtectHome; this test
	// exercises the real systemd socket filter, not the filesystem sandbox.
	wrapper := filepath.Join(t.TempDir(), "mihomo-systemd")
	script := "#!/bin/sh\nexec systemd-run --pipe --wait --collect --quiet --unit=" + unit + " --property=RuntimeMaxSec=90s --property=NoNewPrivileges=yes"
	for _, value := range families {
		script += " --property='RestrictAddressFamilies=" + value + "'"
	}
	script += " -- '" + binary + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// Register even before readiness so failed startup cannot leave a service.
	var process *trafficMihomoProcess
	t.Cleanup(func() {
		_ = exec.Command("systemctl", "stop", unit).Run()
		if process != nil {
			process.stop(t)
		}
	})
	process = startMihomoForTrafficTest(t, wrapper, directory, "config.yaml", content, ready)
	return process, unit
}

func mieruUDPBackend(t *testing.T, network, address string, reply func([]byte) []byte) (*net.UDPAddr, *atomic.Int64) {
	t.Helper()
	listener, err := net.ListenPacket(network, address)
	if err != nil {
		t.Fatal(err)
	}
	hits := &atomic.Int64{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 65535)
		for {
			n, peer, err := listener.ReadFrom(buffer)
			if err != nil {
				return
			}
			hits.Add(1)
			if out := reply(buffer[:n]); out != nil {
				_, _ = listener.WriteTo(out, peer)
			}
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	return listener.LocalAddr().(*net.UDPAddr), hits
}

type mieruUDPAssociation struct {
	control net.Conn
	packet  net.Conn
}

func (a *mieruUDPAssociation) close() { _ = a.control.Close(); _ = a.packet.Close() }
func mieruSOCKSUDP(t *testing.T, port int) *mieruUDPAssociation {
	t.Helper()
	control, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	_ = control.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := control.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	var auth [2]byte
	if _, err := io.ReadFull(control, auth[:]); err != nil || auth != [2]byte{5, 0} {
		t.Fatalf("SOCKS5 auth: %v", err)
	}
	if _, err := control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	var header [3]byte
	if _, err := io.ReadFull(control, header[:]); err != nil || header != [3]byte{5, 0, 0} {
		t.Fatalf("SOCKS5 UDP association: %v", err)
	}
	relay, err := mieruReadSOCKSAddress(control)
	if err != nil {
		t.Fatal(err)
	}
	_ = control.SetDeadline(time.Time{})
	packet, err := net.Dial("udp4", relay.String())
	if err != nil {
		t.Fatal(err)
	}
	result := &mieruUDPAssociation{control, packet}
	t.Cleanup(result.close)
	return result
}
func (a *mieruUDPAssociation) exchange(target *net.UDPAddr, payload []byte, timeout time.Duration) ([]byte, error) {
	header := []byte{0, 0, 0}
	if ip := target.IP.To4(); ip != nil {
		header = append(header, 1)
		header = append(header, ip...)
	} else {
		header = append(header, 4)
		header = append(header, target.IP.To16()...)
	}
	header = binary.BigEndian.AppendUint16(header, uint16(target.Port))
	_ = a.packet.SetDeadline(time.Now().Add(timeout))
	if _, err := a.packet.Write(append(header, payload...)); err != nil {
		return nil, err
	}
	buffer := make([]byte, 65535)
	n, err := a.packet.Read(buffer)
	if err != nil {
		return nil, err
	}
	if n < 4 || !bytes.Equal(buffer[:3], []byte{0, 0, 0}) {
		return nil, fmt.Errorf("invalid SOCKS5 UDP header")
	}
	r := bytes.NewReader(buffer[3:n])
	source, err := mieruReadSOCKSAddress(r)
	if err != nil {
		return nil, err
	}
	if !source.IP.Equal(target.IP) || source.Port != target.Port {
		return nil, fmt.Errorf("reply from wrong UDP target: %s", source)
	}
	return io.ReadAll(r)
}
func mieruReadSOCKSAddress(r io.Reader) (*net.UDPAddr, error) {
	var kind [1]byte
	if _, err := io.ReadFull(r, kind[:]); err != nil {
		return nil, err
	}
	size := 4
	if kind[0] == 4 {
		size = 16
	} else if kind[0] != 1 {
		return nil, fmt.Errorf("unsupported SOCKS address type %d", kind[0])
	}
	address := make([]byte, size+2)
	if _, err := io.ReadFull(r, address); err != nil {
		return nil, err
	}
	return &net.UDPAddr{IP: net.IP(address[:size]), Port: int(binary.BigEndian.Uint16(address[size:]))}, nil
}
