//go:build linux

package agent

import (
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionSocketParsing(t *testing.T) {
	for input, want := range map[string]string{"0100007F:01BB": "127.0.0.1:443", "00000000000000000000000001000000:01BB": "[::1]:443", "0000000000000000FFFF00000100007F:01BB": "127.0.0.1:443"} {
		got, err := parseProcSocket(input)
		if err != nil || got.String() != want {
			t.Fatalf("%s: %s %v", input, got, err)
		}
	}
	input := ` sl local_address rem_address st
0: 00000000:01BB 00000000:0000 0A
1: 0100007F:01BB 0200007F:C350 01
2: 0100007F:C351 0200007F:01BB 01
3: 0100007F:01BB 0200007F:C352 06
`
	flows, err := readClientTCPSockets(strings.NewReader(input))
	if err != nil || len(flows) != 1 || flows[0].ClientIP != "127.0.0.2" || flows[0].LocalPort != 443 || flows[0].ClientPort != 50000 {
		t.Fatalf("inbound sockets=%+v %v", flows, err)
	}
}

func TestClientConnectionUDPOriginalDirection(t *testing.T) {
	local := map[netip.Addr]bool{netip.MustParseAddr("192.0.2.1"): true}
	line := "ipv4 2 udp 17 29 src=198.51.100.8 dst=192.0.2.1 sport=50123 dport=443 src=192.0.2.1 dst=198.51.100.8 sport=443 dport=50123 [ASSURED]"
	flow, ok := parseClientUDPFlow(line, local)
	if !ok || flow.ClientIP != "198.51.100.8" || flow.ClientPort != 50123 || flow.LocalPort != 443 {
		t.Fatalf("wrong original direction: %+v %v", flow, ok)
	}
	if _, ok := parseClientUDPFlow("udp src=192.0.2.1 dst=198.51.100.8 sport=443 dport=50123 src=198.51.100.8 dst=192.0.2.1 sport=50123 dport=443", local); ok {
		t.Fatal("outbound flow treated as inbound")
	}
}

func TestCollectClientConnectionsLiveInbound(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	config := filepath.Join(t.TempDir(), "config.yaml")
	// HTTP is TCP-only, so this fixture does not require conntrack privileges.
	if err := os.WriteFile(config, []byte("port: "+strings.Split(listener.Addr().String(), ":")[1]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineMihomo: {ConfigPath: config}}}
	report := executor.collectClientConnections(context.Background())
	if report.Status != "ok" || len(report.Connections) != 1 || report.Connections[0].LocalPort != port || report.Connections[0].ClientPort != client.LocalAddr().(*net.TCPAddr).Port || report.Connections[0].Protocol != "http" {
		t.Fatalf("live inbound sample: %+v", report)
	}
}

func TestClientConnectionUDPSocketScope(t *testing.T) {
	sockets, err := readClientUDPSockets(strings.NewReader("sl local_address rem_address st\n0: 00000000:01BB 00000000:0000 07\n1: 0100007F:14E9 00000000:0000 07\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !clientSocketListening(sockets, "192.0.2.1", 443) || !clientSocketListening(sockets, "127.0.0.1", 5353) || clientSocketListening(sockets, "192.0.2.1", 5353) || clientSocketListening(sockets, "192.0.2.1", 8080) {
		t.Fatal("UDP listener address/port filtering failed")
	}
}
