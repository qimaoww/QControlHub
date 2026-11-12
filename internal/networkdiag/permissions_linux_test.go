//go:build linux

package networkdiag

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nxtrace/NTrace-core/trace"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Exercise the actual runProcess exec path using the test executable as Agent.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "network-trace-stream" {
		if err := RunNextTraceStream(context.Background(), os.Stdin, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNextTraceExecutionPermissionsSandbox(t *testing.T) {
	mode := os.Getenv("QCH_RAW_SOCKET_FIXTURE")
	if mode == "" {
		t.Skip("requires an isolated dual-stack network with fixture addresses and explicit RAW policy")
	}
	allowed := mode != "denied"
	pingReady := pingCapability()
	if (pingReady.Error == "") != allowed || pingReady.AutoInstall {
		t.Fatalf("ping capability disagrees: %+v", pingReady)
	}
	cap := nextTraceCapability(context.Background())
	if (cap.Error == "") != allowed || cap.AutoInstall {
		t.Fatalf("capability: %+v", cap)
	}
	if !allowed && (!strings.Contains(cap.Error, "原始套接字") || !strings.Contains(cap.Error, "CAP_NET_RAW")) {
		t.Fatal(cap.Error)
	}
	if err := PrepareTool(context.Background(), "nexttrace"); (err == nil) != allowed {
		t.Fatalf("preparation disagrees: %v", err)
	}
	for _, family := range []int{4, 6} {
		source, target := "192.0.2.10", "192.0.2.11"
		if family == 6 {
			source, target = "fd42:7163::10", "fd42:7163::11"
		}
		familyAllowed := allowed && !(mode == "ipv4-only" && family == 6) && !(mode == "ipv6-only" && family == 4)
		for _, protocol := range []string{"icmp", "udp", "tcp"} {
			for _, tool := range []string{"traceroute", "mtr"} {
				t.Run(fmt.Sprintf("%d/%s/%s", family, protocol, tool), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
					defer cancel()
					cfg := trace.Config{SrcAddr: source, DstIP: net.ParseIP(target), SrcPort: -1, DstPort: 5201}
					err := checkNextTraceBackend(ctx, trace.Method(protocol), cfg)
					if familyAllowed {
						wrong := cfg
						wrong.SrcAddr = "192.0.2.99"
						if family == 6 {
							wrong.SrcAddr = "fd42:7163::99"
						}
						if e := checkNextTraceBackend(ctx, trace.Method(protocol), wrong); e == nil || rawSocketDenied(e) || !strings.Contains(e.Error(), "cannot assign requested address") {
							t.Fatalf("source error misclassified: %v", e)
						}
					}
					if (err == nil) != familyAllowed {
						t.Fatalf("parent backend: %v", err)
					}
					c := core.DiagnosticCommand{ID: "ndg_raw_fixture", SessionID: "fixture", Role: "probe", Source: core.DiagnosticEndpoint{Address: source}, Target: core.DiagnosticEndpoint{Address: target}, Parameters: core.DiagnosticParameters{Tool: tool, Protocol: protocol, Provider: "disable-geoip", Count: 1, IntervalMS: 200, TimeoutMS: 300, MaxHops: 2, Size: 80, Port: 5201, Duration: 1, Streams: 1}}
					r := runProcess(ctx, c, func() {}, func(int) error { return nil })
					if familyAllowed {
						if r.State != "succeeded" || len(r.Output) == 0 || !strings.Contains(string(r.Output), target) {
							t.Fatalf("real child measurement: %+v", r)
						}
					} else if allowed {
						if r.State != "failed" || !strings.Contains(r.Error, "地址族不可用") || strings.Contains(r.Error, "CAP_NET_RAW") || len(r.Output) > 0 {
							t.Fatalf("family error confused with permission denial: %+v", r)
						}
					} else if r.State != "failed" || !strings.Contains(r.Error, "CAP_NET_RAW") || len(r.Output) > 0 {
						t.Fatalf("child denied permission hidden: %+v", r)
					}
					if !r.Cleaned {
						t.Fatal("child not cleaned")
					}
				})
			}
		}
	}
}
