//go:build linux

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Run the production shell wrapper with an upstream fixture instead of making
// network requests or installing packages on the test host.
func executeIPQualityFixture(t *testing.T, script string) (core.IPQualityResult, error) {
	t.Helper()
	program := "curl() { cat <<'QCH_IPQUALITY_FIXTURE'\n" + script + "\nQCH_IPQUALITY_FIXTURE\n}\n" + ipQualityProgram
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return executeIPQualityProgram(ctx, "/bin/bash", program)
}

func TestIPQualityDependencyFilesAreNotLimitedToReportSize(t *testing.T) {
	// Model a package installer writing a file larger than the old 256 KiB
	// process limit. The fixture must finish this write before emitting a report.
	script := `set -e
dd if=/dev/zero of=dependency-package bs=1024 count=512 2>/dev/null
test "$(wc -c < dependency-package)" -eq 524288
printf '%s\n' '` + agentQualityReport("203.0.113.1", "fixture") + `' > "$5"
printf 'IP质量体检报告：203.0.113.1\n依赖安装完成\n'`
	result, err := executeIPQualityFixture(t, script)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ReportsText) != 1 || !strings.Contains(result.ReportsText[0], "依赖安装完成") {
		t.Fatal("dependency write did not complete before the report")
	}

	// Removing the process-wide file limit must not allow oversized JSON into
	// result memory or onto the WSS result path.
	_, err = executeIPQualityFixture(t, `set -e
dd if=/dev/zero of="$5" bs=1024 count=129 2>/dev/null
printf 'IP质量体检报告：203.0.113.1\n'`)
	if err == nil || !strings.Contains(err.Error(), "大小受限的普通文件") {
		t.Fatalf("oversized JSON report was not rejected by the file check: %v", err)
	}
}

func TestIPQualityPrintedCapturePreservesBothFamilies(t *testing.T) {
	for _, size := range []int{20 << 10, ipQualityPrintedReportLimit} {
		t.Run(fmt.Sprintf("%d-bytes-per-family", size), func(t *testing.T) {
			var script strings.Builder
			var expected []string
			for _, ip := range []string{"203.0.113.1", "2001:db8::1"} {
				header := "IP质量体检报告：" + ip + "\n"
				tail := "\n报告结束：" + ip
				printed := header + strings.Repeat("x", size-len(header)-len(tail)) + tail
				expected = append(expected, printed)
				fmt.Fprintf(&script, "printf '%%s\\n' '%s' >> \"$5\"\nprintf '%%s\\n' '%s'\n",
					agentQualityReport(ip, "fixture"), printed)
			}
			result, err := executeIPQualityFixture(t, script.String())
			if err != nil {
				t.Fatal(err)
			}
			if len(result.ReportsText) != len(expected) {
				t.Fatalf("captured %d printed reports, want %d", len(result.ReportsText), len(expected))
			}
			for index, want := range expected {
				if result.ReportsText[index] != want {
					t.Fatalf("report %d changed: got %d bytes, want %d", index, len(result.ReportsText[index]), len(want))
				}
			}
		})
	}
}

func TestIPQualityPrintedCaptureRejectsIncompleteReports(t *testing.T) {
	header := "IP质量体检报告：203.0.113.1\n"
	for _, test := range []struct {
		name, printed, wantError string
	}{
		{"capture-overflow", header + strings.Repeat("x", ipQualityPrintedOutputLimit), "已拒绝截断的报告"},
		{"family-overflow", header + strings.Repeat("x", ipQualityPrintedReportLimit+1-len(header)), "单个地址族"},
		{"missing-text", "", "份数不匹配"},
		{"extra-family", header + "IP质量体检报告：2001:db8::1\n", "份数不匹配"},
		{"invalid-utf8", header + string([]byte{0xff}), "not UTF-8"},
		// Each ESC expands to six bytes in JSON. Raw capture bounds alone do
		// not guarantee that the structured result fits the wire budget.
		{"encoded-overflow", header + strings.Repeat("\x1b", 24<<10), "size limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := fmt.Sprintf("printf '%%s\\n' '%s' > \"$5\"\nprintf '%%s' '%s'\n",
				agentQualityReport("203.0.113.1", "fixture"), test.printed)
			result, err := executeIPQualityFixture(t, script)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected %q, got %v", test.wantError, err)
			}
			if len(result.Reports) != 0 || len(result.ReportsText) != 0 {
				t.Fatal("failed capture returned a partial result")
			}
		})
	}
}
