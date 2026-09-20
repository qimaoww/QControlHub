package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// ipQualityPrintedFixture mirrors what ip.sh prints, ANSI colours included.
const ipQualityPrintedFixture = "\x1b[36m########################################################################\x1b[0m\n" +
	"                 \x1b[1mIP质量体检报告：\x1b[36m203.0.113.1\x1b[0m\n" +
	"                   https://github.com/xykt/IPQuality\n" +
	"                bash <(curl -sL https://Check.Place) -I\n" +
	"        报告时间：2026-09-19 13:56:33 CST  脚本版本：v2026-09-16\n" +
	"\x1b[36m########################################################################\x1b[0m\n" +
	"一、基础信息（Maxmind 数据库）\n" +
	"\x1b[36m自治系统号：            \x1b[32mAS210634\x1b[0m\n" +
	"\x1b[36mIP类型：                \x1b[41m\x1b[37m\x1b[1m 广播IP \x1b[0m\n" +
	"三、风险评分\n" +
	"\x1b[36mIP2Location：\x1b[37m\x1b[1m  3\x1b[42m              31|\x1b[0m\x1b[32m低风险\x1b[0m\n" +
	"\x1b[36mipapi：\x1b[37m\x1b[1m    0.00%\x1b[41m 5.47%|\x1b[0m\x1b[31m极低风险\x1b[0m\n" +
	"五、流媒体及AI服务解锁检测\n" +
	"状态：   \x1b[42m\x1b[37m 解锁 \x1b[0m  \x1b[41m\x1b[37m 屏蔽 \x1b[0m  \x1b[43m\x1b[37m 仅APP \x1b[0m\n" +
	"========================================================================\n" +
	"今日IP检测量：565；总检测量：2204309。感谢使用xy系列脚本！\n"

func TestIPQualityRenderDrawsPrintedReport(t *testing.T) {
	reports := []json.RawMessage{
		json.RawMessage(`{"Head":{"IP":"203.0.113.1"},"Info":{},"Type":{},"Score":{},"Factor":{},"Media":{},"Mail":{}}`),
	}
	archives, err := renderIPQualityArchives(&core.IPQualityResult{Reports: reports, ReportsText: []string{ipQualityPrintedFixture}})
	if err != nil || len(archives) != 1 {
		t.Fatalf("render: %+v %v", archives, err)
	}
	archive := archives[0]
	if archive.Family != 4 || archive.Size != len(archive.Content) || len(archive.SHA256) != 64 || archive.RenderedAt.IsZero() {
		t.Fatalf("incomplete archive metadata: %+v", archive)
	}
	digest := sha256.Sum256(archive.Content)
	if archive.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("archive digest does not match its content")
	}
	content := string(archive.Content)
	for _, want := range []string{
		"<svg", "IP质量体检报告：", "203.0.113.1", "AS210634", "广播IP", "低风险", "极低风险",
		"今日IP检测量：565；总检测量：2204309", ipQualityGreen, ipQualityRed, ipQualityYellow,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered report is missing %q", want)
		}
	}
	if strings.Contains(content, "\x1b") {
		t.Fatal("ANSI escapes leaked into the SVG")
	}
	if err := validateRenderedSVG(archive.Content); err != nil {
		t.Fatalf("rendered report is not a single SVG document: %v", err)
	}
}

func TestIPQualityRenderEscapesAndRejectsBadText(t *testing.T) {
	hostile := "报告：<script>alert(1)</script> & <b>\x07\x1b[31m红色\x1b[0m"
	svg, err := renderIPQualitySVG(hostile)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(svg)
	if strings.Contains(rendered, "<script>") || strings.Contains(rendered, "& <b>") {
		t.Fatal("report text was not escaped")
	}
	if !strings.Contains(rendered, "&lt;script&gt;") || !strings.Contains(rendered, "&amp;") {
		t.Fatal("expected escaped markup in the SVG")
	}
	if err := validateRenderedSVG(svg); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "   \n  ", strings.Repeat("x", ipQualityRenderMaxText+1), string([]byte{0xff, 0xfe})} {
		if _, err := renderIPQualitySVG(bad); err == nil {
			t.Fatalf("accepted invalid report text %q", bad)
		}
	}
}

func TestIPQualityRenderAssignsAddressFamilies(t *testing.T) {
	reports := []json.RawMessage{
		json.RawMessage(`{"Head":{"IP":"203.0.113.1"},"Info":{},"Type":{},"Score":{},"Factor":{},"Media":{},"Mail":{}}`),
		json.RawMessage(`{"Head":{"IP":"2001:db8::1"},"Info":{},"Type":{},"Score":{},"Factor":{},"Media":{},"Mail":{}}`),
	}
	archives, err := renderIPQualityArchives(&core.IPQualityResult{
		Reports: reports, ReportsText: []string{"报告一\n", "报告二\n"},
	})
	if err != nil || len(archives) != 2 {
		t.Fatalf("render: %+v %v", archives, err)
	}
	if archives[0].Family != 4 || archives[1].Family != 6 {
		t.Fatalf("families = %d, %d", archives[0].Family, archives[1].Family)
	}
	if _, err := renderIPQualityArchives(&core.IPQualityResult{Reports: reports}); err == nil {
		t.Fatal("accepted a result without the printed report")
	}
}

// validateRenderedSVG mirrors what a browser needs: exactly one SVG root.
func validateRenderedSVG(content []byte) error {
	decoder := xml.NewDecoder(strings.NewReader(string(content)))
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) && roots == 1 && depth == 0 {
			return nil
		}
		if err != nil {
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || token.Name.Local != "svg" || token.Name.Space != "http://www.w3.org/2000/svg" {
					return errors.New("not a single svg root")
				}
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
}
