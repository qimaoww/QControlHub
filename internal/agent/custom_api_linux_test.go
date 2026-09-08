package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestCustomXrayAPIRuntime(t *testing.T) {
	binary := os.Getenv("QCH_TEST_XRAY_BIN")
	if os.Getenv("QCH_TEST_NATIVE_TRAFFIC") != "1" || binary == "" {
		t.Skip("explicit native Xray test opt-in required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if os.Getenv("QCH_CUSTOM_API_CHILD") != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--net", os.Args[0], "-test.run=^TestCustomXrayAPIRuntime$", "-test.v")
		cmd.Env = append(os.Environ(), "QCH_CUSTOM_API_CHILD=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated Xray: %v\n%s", err, output)
		}
		t.Log(string(output))
		return
	}
	if output, err := exec.CommandContext(ctx, "ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Fatalf("loopback: %v %s", err, output)
	}
	content := `{"api":{"tag":"api","services":["HandlerService"]},"inbounds":[{"tag":"api-a","listen":"127.0.0.1","port":40152,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1"}},{"tag":"api-b","listen":"127.0.0.1","port":40153,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1"}},{"tag":"proxy","listen":"127.0.0.1","port":1080,"protocol":"socks","settings":{"auth":"noauth"}}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["api-a"],"outboundTag":"api"},{"type":"field","inboundTag":["api-b"],"outboundTag":"api"}]}}`
	plan, err := serverconfig.PrepareAccounting(core.EngineXray, content)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(plan.Content), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, binary, "run", "-c", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	for _, address := range []string{plan.API, "127.0.0.1:40153"} {
		for {
			_, err = queryNativeTrafficAt(ctx, core.EngineXray, address)
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatalf("original API %s unavailable: %v", address, err)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}

func TestNativeStatisticsAddressRejectsNonLoopback(t *testing.T) {
	for _, address := range []string{"example.com:10085", "0.0.0.0:10085", "192.0.2.1:10085", "127.0.0.1:0", "unix:///tmp/api"} {
		if _, err := queryNativeTrafficAt(context.Background(), core.EngineXray, address); err == nil {
			t.Fatalf("accepted %s", address)
		}
	}
}
