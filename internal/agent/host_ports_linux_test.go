//go:build linux

package agent

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestHostPortsObserveRealTCPAndUDPSockets(t *testing.T) {
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	observation, err := observeHostPorts()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{tcp.Addr().String(), udp.LocalAddr().String()} {
		_, raw, _ := net.SplitHostPort(address)
		port, _ := strconv.Atoi(raw)
		found := false
		for _, observed := range observation.Ports {
			found = found || port == observed
		}
		if !found {
			t.Fatalf("real listening socket %s absent", address)
		}
	}
	for _, table := range []string{"", "sl local_address\n0: broken\n", "sl local_address\n0: 0100007F:ZZZZ 0:0 0A 0 0 0 0 0 0\n"} {
		if readHostPortTable(strings.NewReader(table), true, map[int]bool{}) == nil {
			t.Fatal("incomplete observation accepted")
		}
	}
}
