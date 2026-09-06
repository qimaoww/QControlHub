package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func tcpTestBackend(t *testing.T) (*systemBBRBackend, map[string]string) {
	t.Helper()
	values := map[string]string{
		bbrAlgorithmKey: "cubic", bbrQdiscKey: "fq_codel", "kernel.osrelease": "6.12-test",
		"net.ipv4.tcp_available_congestion_control": "reno cubic bbr", "net.ipv4.tcp_ecn": "2",
		"net.ipv4.tcp_rmem": "4096\t87380\t16777216", "net.core.wmem_max": "212992",
	}
	b := newSystemBBRBackend()
	b.configPath = filepath.Join(t.TempDir(), "90-qcontrolhub-bbr.conf")
	b.read = func(key string) (string, error) {
		value, ok := values[key]
		if !ok {
			return "", os.ErrNotExist
		}
		return value, nil
	}
	b.write = func(key, value string) error { values[key] = value; return nil }
	return b, values
}

func TestSystemBBRReadsExternalConfiguration(t *testing.T) {
	b, values := tcpTestBackend(t)
	values[bbrAlgorithmKey] = "bbr"
	status := b.snapshot()
	if !status.Available || status.CongestionControl != "bbr" || status.Persistence != "unmanaged" || status.Parameters["net.ipv4.tcp_ecn"] != "2" {
		t.Fatalf("external settings not reported: %+v", status)
	}
	if _, err := os.Stat(b.configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only collection created a config file")
	}
	delete(values, bbrQdiscKey)
	status = b.snapshot()
	if !status.Available || status.CongestionControl != "bbr" || status.Error == "" {
		t.Fatalf("partial sysctl support hid real BBR state: %+v", status)
	}
	delete(values, bbrAlgorithmKey)
	if b.snapshot().Available {
		t.Fatal("missing congestion control not marked unavailable")
	}
}

func TestSystemBBRCustomSettingsMergeAndDrift(t *testing.T) {
	b, values := tcpTestBackend(t)
	if _, err := b.apply(context.Background(), core.ActionEnableBBR, nil); err != nil {
		t.Fatal(err)
	}
	if values[bbrAlgorithmKey] != "bbr" || values[bbrQdiscKey] != "fq" {
		t.Fatal(values)
	}
	custom := core.TCPSettings{"net.ipv4.tcp_rmem": "4096 131072 33554432", "net.ipv4.tcp_ecn": "1"}
	if _, err := b.apply(context.Background(), core.ActionConfigureTCP, custom); err != nil {
		t.Fatal(err)
	}
	status := b.snapshot()
	if status.Persistence != "managed" || len(status.ConfiguredParameters) != 4 || status.ConfiguredAlgorithm != "bbr" || status.Parameters["net.core.wmem_max"] != "212992" {
		t.Fatalf("lost untouched parameters: %+v", status)
	}
	if _, err := b.apply(context.Background(), core.ActionDisableBBR, nil); err != nil {
		t.Fatal(err)
	}
	status = b.snapshot()
	if status.ConfiguredAlgorithm != "cubic" || status.ConfiguredParameters["net.ipv4.tcp_ecn"] != "1" {
		t.Fatalf("preset overwrote custom TCP settings: %+v", status)
	}
	values[bbrAlgorithmKey] = "bbr3"
	status = b.snapshot()
	if status.CongestionControl != "bbr3" || status.ConfiguredAlgorithm != "cubic" {
		t.Fatal("runtime drift was hidden")
	}
	info, err := os.Stat(b.configPath)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("config mode: %v, %v", info, err)
	}
}

func TestSystemBBRRollback(t *testing.T) {
	for _, failure := range []string{"write", "verify", "persist", "persist-after-rename", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			b, values := tcpTestBackend(t)
			// An existing managed profile must survive every error unchanged.
			if _, err := b.apply(context.Background(), core.ActionConfigureTCP, core.TCPSettings{"net.ipv4.tcp_ecn": "2"}); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(b.configPath)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			write, persist := b.write, b.persist
			b.write = func(key, value string) error {
				if key == bbrAlgorithmKey && value == "bbr" {
					if failure == "write" {
						return errors.New("module unavailable")
					}
					if failure == "verify" {
						return nil
					}
					if failure == "canceled" {
						cancel()
					}
				}
				return write(key, value)
			}
			failed := false
			b.persist = func(content string) error {
				if !failed && strings.HasPrefix(failure, "persist") {
					failed = true
					if failure == "persist-after-rename" {
						if err := persist(content); err != nil {
							return err
						}
					}
					return errors.New("disk failure")
				}
				return persist(content)
			}
			if _, err := b.apply(ctx, core.ActionEnableBBR, nil); err == nil {
				t.Fatal("expected failure")
			}
			if values[bbrAlgorithmKey] != "cubic" || values[bbrQdiscKey] != "fq_codel" {
				t.Fatalf("partial runtime write survived: %v", values)
			}
			after, _ := os.ReadFile(b.configPath)
			if string(before) != string(after) {
				t.Fatalf("persisted config changed after failure: %s", after)
			}
		})
	}
}

func TestSystemBBRRejectsUnsafeOrUnsupportedChanges(t *testing.T) {
	for _, kind := range []string{"foreign-file", "symlink", "unsafe-value", "unknown-key", "missing-key"} {
		t.Run(kind, func(t *testing.T) {
			b, _ := tcpTestBackend(t)
			settings := core.TCPSettings{bbrAlgorithmKey: "bbr"}
			switch kind {
			case "foreign-file":
				if err := os.WriteFile(b.configPath, []byte("net.ipv4.tcp_ecn=1\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "target"), b.configPath); err != nil {
					t.Fatal(err)
				}
			case "unsafe-value":
				settings[bbrAlgorithmKey] = "bbr\nnet.ipv4.ip_forward=1"
			case "unknown-key":
				settings = core.TCPSettings{"kernel.sysrq": "1"}
			case "missing-key":
				settings = core.TCPSettings{"net.core.somaxconn": "4096"}
			}
			b.write = func(string, string) error { t.Fatal("wrote before validation completed"); return nil }
			if _, err := b.apply(context.Background(), core.ActionConfigureTCP, settings); err == nil {
				t.Fatal("unsafe change accepted")
			}
		})
	}
}

func TestSystemBBRHelperHasNarrowWriteScope(t *testing.T) {
	settings := core.TCPSettings{"net.ipv4.tcp_ecn": "1", "net.core.wmem_max": "4194304"}
	args := bbrHelperArguments("/usr/local/lib/qagent/qagent", core.ActionConfigureTCP, settings)
	joined := strings.Join(args, "\n")
	for _, expected := range []string{"--property=ReadOnlyPaths=/proc/sys", "--property=ReadWritePaths=/etc/sysctl.d /proc/sys/net/core/wmem_max /proc/sys/net/ipv4/tcp_ecn", "--property=RuntimeMaxSec=30s", "--property=CapabilityBoundingSet=CAP_NET_ADMIN", "--property=ProtectSystem=strict"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %s: %v", expected, args)
		}
	}
	if !reflect.DeepEqual(args[len(args)-4:len(args)-1], []string{"/usr/local/lib/qagent/qagent", "system-bbr", "configure-tcp"}) {
		t.Fatal(args)
	}
	for _, forbidden := range []string{"CAP_SYS_ADMIN", "CAP_SYS_MODULE", "/bin/sh", "sysctl --system", "/etc/sysctl.conf"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("unsafe helper: %s", forbidden)
		}
	}
}
