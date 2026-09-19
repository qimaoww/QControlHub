//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestIPQualityLinkExportFromPinnedUpload(t *testing.T) {
	// The upstream JSON output flag suppresses the printed report link. Export
	// it from the upload response without parsing ANSI output or changing JSON.
	script := `mode_lite=0; mode_privacy=0; IP=203.0.113.1
ipjson='` + agentQualityReport("203.0.113.1", "fixture") + `'
ip_report='fixture'
curl() { printf 'https://Report.Check.Place/ip/fixture.svg'; }
[[ $mode_lite -eq 0 && mode_privacy -eq 0 ]]&&report_link=$(curl -$2 -s -X POST https://upload.check.place -d "type=ip" --data-urlencode "json=$ipjson" --data-urlencode "content=$ip_report")
printf '%s' "$ipjson" > "$6"
`
	prepared, err := prepareIPQualityArchiveScript([]byte(script))
	if err != nil {
		t.Fatal(err)
	}
	result, err := executeIPQualityScript(context.Background(), "/bin/bash", prepared)
	if err != nil || len(result.ReportURLs) != 1 || result.ReportURLs[0] != "https://Report.Check.Place/ip/fixture.svg" {
		t.Fatalf("link export: %+v %v", result, err)
	}
	if _, err := prepareIPQualityArchiveScript([]byte("changed upstream")); err == nil {
		t.Fatal("changed script was silently accepted")
	}
}

func TestIPQualityReportLinkValidation(t *testing.T) {
	result, err := core.ParseIPQualityReports([]byte(agentQualityReport("203.0.113.1", "fixture")))
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"", "203.0.113.1\thttp://127.0.0.1/a.svg\n", "203.0.113.2\thttps://Report.Check.Place/IP/a.svg\n",
		"203.0.113.1\thttps://Report.Check.Place/IP/a.svg\n203.0.113.1\thttps://Report.Check.Place/IP/b.svg\n", strings.Repeat("x", 2049)} {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "links.tsv"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readIPQualityReportLinks(directory, result); err == nil {
			t.Fatalf("accepted invalid link file %q", content)
		}
	}
}
