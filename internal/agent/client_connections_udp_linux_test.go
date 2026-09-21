//go:build linux

package agent

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestClientConnectionUDPActiveFamilies(t *testing.T) {
	local := map[netip.Addr]bool{netip.MustParseAddr("192.0.2.1"): true, netip.MustParseAddr("2001:db8::1"): true}
	listeners := []serverconfig.ConnectionListener{{Port: 443, Transport: core.TrafficProtocolBoth}}
	for _, tc := range []struct {
		name    string
		sockets []string
		want    []string
	}{
		{"no UDP listener", nil, nil},
		{"unrelated UDP port", []string{"0.0.0.0:53"}, nil},
		{"IPv4 listener", []string{"0.0.0.0:443"}, []string{"ipv4"}},
		{"IPv6 listener", []string{"[2001:db8::1]:443"}, []string{"ipv6"}},
		{"dual stack listener", []string{"[::]:443"}, []string{"ipv4", "ipv6"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sockets := map[netip.AddrPort]bool{}
			for _, socket := range tc.sockets {
				sockets[netip.MustParseAddrPort(socket)] = true
			}
			if got := clientUDPConnectionFamilies(sockets, listeners, local); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("families=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestClientConnectionUDPCommandCoverage(t *testing.T) {
	local := map[netip.Addr]bool{netip.MustParseAddr("192.0.2.1"): true, netip.MustParseAddr("2001:db8::1"): true}
	sockets := map[netip.AddrPort]bool{netip.MustParseAddrPort("[::]:443"): true}
	for _, family := range []string{"ipv4", "ipv6"} {
		t.Run(family, func(t *testing.T) {
			command := filepath.Join(t.TempDir(), "conntrack")
			writeExecutable(t, command, `#!/bin/sh
[ "$*" = '-L -f `+family+` -p udp -o extended' ] || exit 64
case "$3" in
 ipv4) printf '%s\n' 'udp src=198.51.100.8 dst=192.0.2.1 sport=50123 dport=443 src=192.0.2.1 dst=198.51.100.8 sport=443 dport=50123' ;;
 ipv6) printf '%s\n' 'udp src=2001:db8::8 dst=2001:db8::1 sport=50123 dport=443 src=2001:db8::1 dst=2001:db8::8 sport=443 dport=50123' ;;
esac
`)
			var flows []core.ClientConnection
			detail := collectClientUDPFamily(context.Background(), command, family, local, sockets, func(flow core.ClientConnection) { flows = append(flows, flow) })
			if detail != "" || len(flows) != 1 || flows[0].ClientPort != 50123 {
				t.Fatalf("detail=%q flows=%+v", detail, flows)
			}
		})
	}
	for _, tc := range []struct{ name, script, want string }{
		{"permission", "echo 'Operation not permitted (CAP_NET_ADMIN)' >&2; exit 1", "permission denied"},
		{"kernel", "echo 'protocol not supported' >&2; exit 1", "check kernel"},
		{"partial", "echo 'udp src=198.51.100.8 dst=192.0.2.1 sport=50123 dport=443'; exit 1", "check kernel"},
		{"empty", "exit 0", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := filepath.Join(t.TempDir(), "conntrack")
			writeExecutable(t, command, "#!/bin/sh\n"+tc.script+"\n")
			count := 0
			detail := collectClientUDPFamily(context.Background(), command, "ipv4", local, sockets, func(core.ClientConnection) { count++ })
			if !strings.Contains(detail, tc.want) || (tc.want == "" && detail != "") {
				t.Fatalf("detail=%q", detail)
			}
			if tc.name == "partial" && count != 1 {
				t.Fatal("available observations lost on command failure")
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		detail := collectClientUDPFamily(context.Background(), "conntrack", "ipv4", local, sockets, func(core.ClientConnection) { t.Fatal("unexpected flow") })
		if detail != "conntrack not installed" {
			t.Fatal(detail)
		}
	})
}

func TestClientConnectionConfigurationFailureDetail(t *testing.T) {
	config := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(config, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineXray: {ConfigPath: config}}}
	report := executor.collectClientConnections(context.Background())
	if report.Status != "partial" || report.Detail != "xray: listener discovery failed" {
		t.Fatalf("lost failure: %+v", report)
	}
}
