package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"regexp"
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
	if !strings.Contains(content, "<svg") {
		t.Fatal("rendered report is not an svg")
	}
	for _, want := range []string{
		"IP质量体检报告：203.0.113.1", "AS210634", "广播IP", "低风险", "极低风险",
		"今日IP检测量：565；总检测量：2204309",
	} {
		if rendered := strings.Join(ipQualityRenderedLines(content), "\n"); !strings.Contains(rendered, want) {
			t.Fatalf("rendered report is missing %q", want)
		}
	}
	for _, want := range []string{ipQualityGreen, ipQualityRed, ipQualityYellow} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered report is missing the %s colour", want)
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

// ipQualityAlignedFixture reproduces the lines that exposed the column drift:
// a wide header row, the badge row beneath it, and a run of full-width text.
const ipQualityAlignedFixture = "服务商： \x1b[3m TikTok   Disney+  Netflix Youtube  AmazonPV  Reddit   ChatGPT \x1b[0m\n" +
	"状态：    \x1b[42m\x1b[37m 解锁 \x1b[0m   \x1b[42m\x1b[37m 解锁 \x1b[0m   \x1b[42m\x1b[37m 解锁 \x1b[0m   " +
	"\x1b[42m\x1b[37m 解锁 \x1b[0m   \x1b[42m\x1b[37m 解锁 \x1b[0m   \x1b[42m\x1b[37m 解锁 \x1b[0m   " +
	"\x1b[43m\x1b[37m 仅APP \x1b[0m \x1b[0m\n" +
	"四、风险因子\n" +
	"库：     ipapi ipregistry IPinfo DB-IP\n"

var ipQualityTextElement = regexp.MustCompile(`<text[^>]*>(.*?)</text>`)
var ipQualitySpanTags = regexp.MustCompile(`<[^>]+>`)

func ipQualityRenderedLines(svg string) []string {
	var lines []string
	for _, match := range ipQualityTextElement.FindAllStringSubmatch(svg, -1) {
		lines = append(lines, html.UnescapeString(ipQualitySpanTags.ReplaceAllString(match[1], "")))
	}
	return lines
}

func mustRenderIPQualitySVG(t *testing.T, text string) string {
	t.Helper()
	svg, err := renderIPQualitySVG(text)
	if err != nil {
		t.Fatal(err)
	}
	return string(svg)
}

func ipQualityNativeFixture(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile("testdata/ip_quality_native.ansi")
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestIPQualityRenderMatchesNativePresentation(t *testing.T) {
	svg := mustRenderIPQualitySVG(t, ipQualityNativeFixture(t))
	for _, want := range []string{
		`width="74ch" height="47em"`, `font-size="14"`,
		`font-family="SimHei, Consolas, DejaVu Sans Mono, SF Mono, monospace"`,
		`fill="#000000"`, `fill="#bbbbbb"`, `fill="#00bbbb"`,
		`fill="#00bb00"`, `fill="#bb0000"`, `fill="#aa9900"`,
		`dominant-baseline:central`, `white-space:pre`, `font-variant-ligatures:none`,
		`<text x="1ch" y="46.5em">`,
		`<rect x="17ch" y="24em" width="16ch" height="1em" fill="#00bb00"/>`,
		`<rect x="33ch" y="24em" width="16ch" height="1em" fill="#aa9900"/>`,
		`<rect x="49ch" y="24em" width="1ch" height="1em" fill="#bb0000"/>`,
	} {
		if !strings.Contains(svg, want) {
			t.Fatalf("native presentation lost %s", want)
		}
	}
	for _, forbidden := range []string{"textLength=", "lengthAdjust=", "viewBox=", `<tspan x=`} {
		if strings.Contains(svg, forbidden) {
			t.Fatalf("native text flow was overridden by %s", forbidden)
		}
	}
	if strings.LastIndex(svg, "<rect") > strings.Index(svg, "<text") {
		t.Fatal("a following background can erase text")
	}
	lines := ipQualityRenderedLines(svg)
	printed := strings.Split(strings.TrimSuffix(ipQualityNativeFixture(t), "\n"), "\n")
	if len(lines) != 47 {
		t.Fatalf("rendered %d rows, want the complete 47-row report", len(lines))
	}
	for row, text := range printed {
		if want := ipQualityANSISequence.ReplaceAllString(text, ""); lines[row] != want {
			t.Fatalf("row %d changed: %q, want %q", row, lines[row], want)
		}
	}
	for _, section := range []string{"一、基础信息", "二、IP类型属性", "三、风险评分", "四、风险因子", "五、流媒体", "六、邮局", "今日IP检测量"} {
		if !strings.Contains(strings.Join(lines, "\n"), section) {
			t.Fatalf("missing section %q", section)
		}
	}
}

func TestIPQualityRenderKeepsTerminalStyles(t *testing.T) {
	svg := mustRenderIPQualitySVG(t, "\x1b[1;3;4mMaxmind 数据库\x1b[0m普通文字")
	want := `<tspan fill="#bbbbbb" font-weight="bold" font-style="italic" text-decoration="underline">Maxmind 数据库</tspan><tspan fill="#bbbbbb">普通文字</tspan>`
	if !strings.Contains(svg, want) {
		t.Fatalf("SGR styles were changed or leaked after reset: %s", svg)
	}
}

func TestIPQualityRenderKeepsCombiningTextTogether(t *testing.T) {
	text := "a\u0301报告b\u200d远端\u200b25"
	svg := mustRenderIPQualitySVG(t, text)
	if !strings.Contains(svg, ">"+text+"</tspan>") {
		t.Fatal("text shaping was split into individual glyphs")
	}
}

// Keep real browser coverage tied to the complete renderer output.
func TestIPQualityBrowserFixtureMatchesRenderer(t *testing.T) {
	for _, family := range []int{4, 6} {
		text := ipQualityNativeFixture(t)
		path := "../../frontend/testdata/ip-quality-report.svg"
		if family == 6 {
			text = strings.ReplaceAll(text, "203.0.113.1", "2001:db8:1234:5678:90ab:cdef:1234:5678")
			path = "../../frontend/testdata/ip-quality-report-v6.svg"
		}
		want := mustRenderIPQualitySVG(t, text)
		if os.Getenv("QCH_UPDATE_IP_QUALITY_FIXTURE") == "1" {
			if err := os.WriteFile(path, []byte(want), 0644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatal("browser fixture is stale; run QCH_UPDATE_IP_QUALITY_FIXTURE=1 go test ./internal/api -run TestIPQualityBrowserFixtureMatchesRenderer")
		}
	}
}

func BenchmarkIPQualityRenderLongLine(b *testing.B) {
	for _, size := range []int{32 << 10, ipQualityRenderMaxText} {
		b.Run(fmt.Sprintf("%d-bytes", size), func(b *testing.B) {
			text := strings.Repeat("a", size)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				if _, err := renderIPQualitySVG(text); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestIPQualityRenderRejectsInvalidXMLCharacters(t *testing.T) {
	for _, character := range []rune{0xfffe, 0xffff} {
		result, err := core.NormalizeIPQualityResult(&core.IPQualityResult{
			Reports:     []json.RawMessage{json.RawMessage(`{"Head":{"IP":"203.0.113.1"},"Info":{},"Type":{},"Score":{},"Factor":{},"Media":{},"Mail":{}}`)},
			ReportsText: []string{"IP质量体检报告：203.0.113.1\n运营商：bad" + string(character) + "value"},
		})
		if err != nil {
			t.Fatal(err)
		}
		archives, err := renderIPQualityArchives(&result)
		if err == nil || !strings.Contains(err.Error(), "XML") || len(archives) != 0 {
			t.Fatalf("U+%04X produced an archive: %v", character, err)
		}
	}
	// Keep valid boundary characters, astral text, escaped markup and ANSI
	// styling renderable while rejecting only invalid documents.
	text := "\x1b[32m报告 & < > \" '\t\ud7ff\ue000\ufffd\U00010000\U0010ffff\x1b[0m"
	svg, err := renderIPQualitySVG(text)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRenderedSVG(svg); err != nil {
		t.Fatal(err)
	}
}

func TestIPQualityDisplayWidthCountsTerminalCells(t *testing.T) {
	for text, want := range map[string]int{
		"AS210634":     8,
		"报告":           4,
		"（IPinfo 数据库）": 17,
		"远端\u200b25":   6,
		"114°10′29″E":  11,
		"仅APP":         5,
	} {
		if got := ipQualityDisplayWidth(text); got != want {
			t.Fatalf("ipQualityDisplayWidth(%q) = %d, want %d", text, got, want)
		}
	}
}

// ipQualityANSISequence matches the SGR sequences the detector emits.
var ipQualityANSISequence = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestIPQualityRenderRebuildsThePrintedLines asserts the panel draws exactly
// the lines the detector printed, in order, without dropping or reordering a
// single cell of text.
func TestIPQualityRenderRebuildsThePrintedLines(t *testing.T) {
	svg := mustRenderIPQualitySVG(t, ipQualityPrintedFixture)
	printed := strings.Split(strings.TrimRight(ipQualityPrintedFixture, "\n"), "\n")
	rebuilt := ipQualityRenderedLines(svg)
	if len(rebuilt) != len(printed) {
		t.Fatalf("rebuilt %d lines, want %d: %q", len(rebuilt), len(printed), rebuilt)
	}
	for index, line := range printed {
		if want := ipQualityANSISequence.ReplaceAllString(line, ""); rebuilt[index] != want {
			t.Fatalf("line %d = %q, want %q", index, rebuilt[index], want)
		}
	}
}

// TestIPQualityRenderKeepsBrightForegroundAsText guards the SGR classification:
// 90-97 are the bright foreground range, not backgrounds.
func TestIPQualityRenderKeepsBrightForegroundAsText(t *testing.T) {
	svg := mustRenderIPQualitySVG(t, "\x1b[92m明亮绿\x1b[0m")
	if strings.Contains(svg, "<rect x=") {
		t.Fatalf("bright foreground painted a background block: %s", svg)
	}
	if !strings.Contains(svg, `fill="`+ipQualityGreen+`"`) {
		t.Fatalf("bright green was not applied to the text: %s", svg)
	}
	dim := mustRenderIPQualitySVG(t, "\x1b[90m暗灰\x1b[0m")
	if !strings.Contains(dim, `fill="`+ipQualityDim+`"`) {
		t.Fatalf("bright black did not become a visible foreground: %s", dim)
	}
	background := mustRenderIPQualitySVG(t, "\x1b[102m绿底\x1b[0m")
	want := `<rect x="1ch" y="0em" width="4ch" height="1em" fill="` + ipQualityGreen + `"/>`
	if !strings.Contains(background, want) {
		t.Fatalf("bright background was not painted: %s", background)
	}
}

// TestIPQualityRenderSkipsUnknownControlSequences guards against a control
// sequence the renderer does not understand swallowing the text after it.
func TestIPQualityRenderSkipsUnknownControlSequences(t *testing.T) {
	svg := mustRenderIPQualitySVG(t, "\x1b[2KIPinfo\x1b[0m 数据库\x1b[?25l\x1b[")
	if strings.Contains(svg, "\x1b") {
		t.Fatal("ANSI escapes leaked into the SVG")
	}
	if rebuilt := strings.Join(ipQualityRenderedLines(svg), "\n"); rebuilt != "IPinfo 数据库" {
		t.Fatalf("rebuilt %q, want %q", rebuilt, "IPinfo 数据库")
	}
}
