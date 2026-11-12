//go:build linux

package networkdiag

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestIPerfStreamPreservesIncrementalAndFinalStatistics(t *testing.T) {
	var snapshots []json.RawMessage
	w := &iperfStreamWriter{publish: func(b json.RawMessage) { snapshots = append(snapshots, b) }}
	events := "{\"event\":\"start\",\"data\":{\"cookie\":\"private\",\"system_info\":\"private host\"}}\n" +
		"{\"event\":\"interval\",\"data\":{\"sum\":{\"start\":0,\"end\":1,\"bytes\":125000}}}\n" +
		"{\"event\":\"end\",\"data\":{\"sum_sent\":{\"bytes\":125123,\"bits_per_second\":1000984}}}\n"
	for i := 0; i < len(events); i += 7 {
		end := min(i+7, len(events))
		if _, err := w.Write([]byte(events[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	if len(snapshots) != 3 || strings.Contains(string(w.latest), "private") {
		t.Fatalf("unsafe or missing events: %s", w.latest)
	}
	var partial, final map[string]json.RawMessage
	_ = json.Unmarshal(snapshots[1], &partial)
	_ = json.Unmarshal(w.latest, &final)
	if partial["end"] != nil || !strings.Contains(string(final["end"]), "125123") || !strings.Contains(string(final["intervals"]), "125000") {
		t.Fatal("incremental and upstream final totals were conflated")
	}
	for _, bad := range []string{"not JSON\n", `{"event":"interval","data":{"sum":` + strings.Repeat(" ", MaxOutput) + "\n"} {
		if _, err := (&iperfStreamWriter{}).Write([]byte(bad)); err == nil {
			t.Fatal("accepted malformed/oversized stream")
		}
	}
}

func TestDiagnosticFailureMessages(t *testing.T) {
	c := core.DiagnosticCommand{Parameters: core.DiagnosticParameters{Tool: "iperf3"}}
	for _, tc := range [][2]string{{"connection refused", "TCP 控制连接"}, {"address already in use", "不会关闭已有服务"}, {"no route to host", "地址族"}, {"operation not permitted", "权限"}, {"address family not supported by protocol", "地址族不可用"}, {"cannot assign requested address", "源地址不可用"}, {"DNS resolution failed", "DNS"}, {"connection timed out", "UDP 测试"}} {
		if got := diagnosticFailure(c, "工具退出码 1", tc[0], 1); !strings.Contains(got, tc[1]) {
			t.Fatalf("%s: %s", tc[0], got)
		}
	}
}
