//go:build linux

package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Export the link directly from the pinned upload response. Do not scrape
// progress output, which contains untrusted provider text and is truncated.
func prepareIPQualityArchiveScript(script []byte) ([]byte, error) {
	line := []byte(`[[ $mode_lite -eq 0 && mode_privacy -eq 0 ]]&&report_link=$(curl -$2 -s -X POST https://upload.check.place -d "type=ip" --data-urlencode "json=$ipjson" --data-urlencode "content=$ip_report")`)
	if bytes.Count(script, line) != 1 {
		return nil, errors.New("IPQuality report-link export no longer matches the pinned script")
	}
	replacement := bytes.Replace(line, []byte("curl -$2 -s"), []byte("curl -$2 -s --max-time 20 --max-filesize 2048"), 1)
	replacement = append(replacement, []byte("\nprintf '%s\\t%s\\n' \"$IP\" \"$report_link\" >> \"$QCH_IPQUALITY_LINKS\"")...)
	return bytes.Replace(script, line, replacement, 1), nil
}

func readIPQualityReportLinks(directory string, result core.IPQualityResult) (core.IPQualityResult, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return core.IPQualityResult{}, err
	}
	defer root.Close()
	file, err := root.OpenFile("links.tsv", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return core.IPQualityResult{}, errors.New("IPQuality 未生成报告下载链接")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2048 {
		return core.IPQualityResult{}, errors.New("IPQuality 报告链接文件无效")
	}
	content, err := io.ReadAll(io.LimitReader(file, 2049))
	if err != nil || len(content) > 2048 {
		return core.IPQualityResult{}, errors.New("IPQuality 报告链接读取失败")
	}
	links := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
		ip, link, ok := strings.Cut(line, "\t")
		if !ok || !core.ValidIPQualityReportURL(link) || links[ip] != "" {
			return core.IPQualityResult{}, errors.New("IPQuality 上游未返回有效的 SVG 报告链接")
		}
		links[ip] = link
	}
	if len(links) != len(result.Reports) {
		return core.IPQualityResult{}, errors.New("IPQuality 报告与下载链接不匹配")
	}
	for _, report := range result.Reports {
		var value struct{ Head struct{ IP string } }
		_ = json.Unmarshal(report, &value)
		result.ReportURLs = append(result.ReportURLs, links[value.Head.IP])
	}
	return core.NormalizeIPQualityResult(&result)
}
