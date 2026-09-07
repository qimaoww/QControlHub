package agent

import (
	"context"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestMarkedTrafficNetworkNamespace(t *testing.T) {
	if os.Getenv("QCH_TEST_NFTABLES") != "1" {
		t.Skip("QCH_TEST_NFTABLES is not enabled")
	}
	if os.Getenv("QCH_MARK_TEST_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "unshare", "--net", os.Args[0], "-test.run=^TestMarkedTrafficNetworkNamespace$", "-test.v")
		command.Env = append(os.Environ(), "QCH_MARK_TEST_CHILD=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated marks: %v\n%s", err, output)
		}
		return
	}
	if output, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Fatalf("loopback: %v %s", err, output)
	}
	if err := checkOutboundMarkRouting(context.Background()); err != nil {
		t.Fatalf("default routing rejected: %v", err)
	}
	if output, err := exec.Command("ip", "rule", "add", "priority", "100", "fwmark", "1", "lookup", "100").CombinedOutput(); err != nil {
		t.Fatalf("isolated policy rule: %v %s", err, output)
	}
	if err := checkOutboundMarkRouting(context.Background()); err == nil {
		t.Fatal("custom mark routing was not rejected")
	}
	if output, err := exec.Command("ip", "rule", "del", "priority", "100", "fwmark", "1", "lookup", "100").CombinedOutput(); err != nil {
		t.Fatalf("remove isolated policy rule: %v %s", err, output)
	}
	for _, network := range []string{"udp4", "udp6"} {
		t.Run(network, func(t *testing.T) {
			address, overhead := "127.0.0.1:0", uint64(28)
			if network == "udp6" {
				address, overhead = "[::1]:0", 48
			}
			listener, err := net.ListenPacket(network, address)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			manager, _, now, policy := newAccuracyTrafficManager(t)
			policy.Engine = core.EngineShadowsocksRust
			// This listener represents the client leg; the real target below
			// deliberately uses a different port and must be attributed by mark.
			policy.Port = 18080
			mark := uint32(0x51430000) | uint32(policy.Port)
			manager.backend = &nftBackend{nftPath: "/usr/sbin/nft", direct: true}
			manager.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
				return nativeAccountingSnapshot{Plan: serverconfig.AccountingPlan{Source: "nft-dual", Ports: []serverconfig.AccountingPort{{Port: policy.Port, Mark: mark}}}}, nil
			}
			if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
				t.Fatal(err)
			}
			dialer := net.Dialer{Control: func(_, _ string, raw syscall.RawConn) error {
				var socketErr error
				err := raw.Control(func(fd uintptr) {
					socketErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, int(mark))
				})
				if err != nil {
					return err
				}
				return socketErr
			}}
			connection, err := dialer.Dial(network, listener.LocalAddr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			transfer := func() {
				t.Helper()
				_ = connection.SetDeadline(time.Now().Add(time.Second))
				_ = listener.SetDeadline(time.Now().Add(time.Second))
				if _, err := connection.Write([]byte("upload")); err != nil {
					t.Fatal(err)
				}
				buffer := make([]byte, 100)
				_, peer, err := listener.ReadFrom(buffer)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := listener.WriteTo([]byte("download-body"), peer); err != nil {
					t.Fatal(err)
				}
				if _, err := connection.Read(buffer); err != nil {
					t.Fatal(err)
				}
			}
			for i := 1; i <= 2; i++ {
				transfer()
				*now = now.Add(time.Second)
				manager.collect(context.Background(), false)
				usage := manager.Snapshot()[0]
				if !usage.EnforcementAvailable || usage.ReceivedBytes != uint64(i)*(13+overhead) || usage.SentBytes != uint64(i)*(6+overhead) || usage.Accounting.ClientReceived != 0 || usage.Accounting.TargetReceived != usage.ReceivedBytes {
					t.Fatalf("target attribution: %+v / %+v", usage, usage.Accounting)
				}
				manager.collect(context.Background(), true)
			}
			if err := manager.SetPolicies(context.Background(), nil, policy.AgentID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
