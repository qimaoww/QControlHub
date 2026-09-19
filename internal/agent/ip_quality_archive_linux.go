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
//
// Upstream skips the upload once it cannot reach ipinfo.check.place and falls
// back to its lite report. That fallback is a data-source downgrade, not a
// privacy choice, so drop only the mode_lite guard: a structurally valid lite
// report still deserves a link while the upload endpoint is reachable.
// mode_privacy still disables the upload.
//
// The link stays mandatory, because the panel archives the SVG next to the
// report. A row is written only for a non-empty link and curl fails without a
// body on HTTP errors, so an unreachable or rejected upload surfaces as a
// missing link with an explicit upstream message instead of an invalid one.
func prepareIPQualityArchiveScript(script []byte) ([]byte, error) {
	line := []byte(`[[ $mode_lite -eq 0 && mode_privacy -eq 0 ]]&&report_link=$(curl -$2 -s -X POST https://upload.check.place -d "type=ip" --data-urlencode "json=$ipjson" --data-urlencode "content=$ip_report")`)
	if bytes.Count(script, line) != 1 {
		return nil, errors.New("IPQuality report-link export no longer matches the pinned script")
	}
	replacement := bytes.Replace(line,
		[]byte(`[[ $mode_lite -eq 0 && mode_privacy -eq 0 ]]`),
		[]byte(`[[ $mode_privacy -eq 0 ]]`), 1)
	replacement = bytes.Replace(replacement, []byte("curl -$2 -s"), []byte("curl -$2 -s -f --max-time 20 --max-filesize 2048"), 1)
	replacement = append(replacement, []byte("\n[[ -n $report_link ]]&&printf '%s\\t%s\\n' \"$IP\" \"$report_link\" >> \"$QCH_IPQUALITY_LINKS\"")...)
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
		return core.IPQualityResult{}, errors.New("IPQuality 上游上传未返回报告链接：节点无法访问 upload.check.place，或请求被 Cloudflare 等防护拦截；请检查节点出站网络后重试")
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
			return core.IPQualityResult{}, errors.New("IPQuality 上游返回的报告链接无效：上传接口返回了非 SVG 内容，可能被 Cloudflare 等防护拦截")
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
