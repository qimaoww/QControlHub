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
	"strconv"
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

// ipQualityTextElement captures one drawn run with the grid pinning that keeps
// it in its terminal cells.
var ipQualityTextElement = regexp.MustCompile(`<text x="([\d ]+)" y="(\d+)" fill="[^"]*"([^>]*) xml:space="preserve">(.*?)</text>`)

var ipQualitySpanTags = regexp.MustCompile(`<[^>]+>`)

var ipQualityBadgeRect = regexp.MustCompile(`<rect x="(\d+)" y="(\d+)" width="(\d+)" height="(\d+)" fill="([^"]+)"/>`)

func ipQualityDrawnRuns(svg string) []ipQualityDrawnRun {
	matches := ipQualityTextElement.FindAllStringSubmatch(svg, -1)
	runs := make([]ipQualityDrawnRun, 0, len(matches))
	for _, match := range matches {
		positions := []int{}
		for _, value := range strings.Fields(match[1]) {
			position, _ := strconv.Atoi(value)
			positions = append(positions, position)
		}
		x := positions[0]
		y, _ := strconv.Atoi(match[2])

		runs = append(runs, ipQualityDrawnRun{
			x: x, y: y, positions: positions,
			content: html.UnescapeString(ipQualitySpanTags.ReplaceAllString(match[4], "")), attributes: match[3],
		})
	}
	return runs
}

type ipQualityDrawnRun struct {
	x, y       int
	positions  []int
	content    string
	attributes string
}

// ipQualityRenderedLines rebuilds the printed lines from the drawn runs: runs
// on one baseline are one line, so the reconstruction can be compared with the
// text the detector printed.
func ipQualityRenderedLines(svg string) []string {
	lines := make([]string, 0, 64)
	baseline := -1
	for _, run := range ipQualityDrawnRuns(svg) {
		if run.y != baseline {
			lines = append(lines, "")
			baseline = run.y
		}
		lines[len(lines)-1] += run.content
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

// Verify every glyph origin, including columns inside long provider headers.
// Merely checking a run's total textLength cannot detect interior column drift.
func TestIPQualityRenderPinsRunsToTheGrid(t *testing.T) {
	svg := mustRenderIPQualitySVG(t, ipQualityPrintedFixture+ipQualityAlignedFixture)
	if strings.Contains(svg, "textLength=") || strings.Contains(svg, "lengthAdjust=") {
		t.Fatal("glyphs still depend on font-dependent length adjustment")
	}
	runs := ipQualityDrawnRuns(svg)
	expectedX, expectedY := 0, 0
	for _, run := range runs {
		if run.y != expectedY {
			expectedX, expectedY = ipQualityRenderPaddingX, run.y
		}
		if len(run.positions) != len([]rune(run.content)) {
			t.Fatalf("run %q does not position every character: %v", run.content, run.positions)
		}
		for index, character := range []rune(run.content) {
			if run.positions[index] != expectedX {
				t.Fatalf("run %q character %d starts at x=%d, want %d", run.content, index, run.positions[index], expectedX)
			}
			expectedX += ipQualityRuneWidth(character) * ipQualityRenderCellWidth
		}
	}
}

func TestIPQualityRenderPositionsSingleGlyphsAndCombiningMarks(t *testing.T) {
	for _, test := range []struct{ text, positions, content string }{
		{"A", "18", "A"},
		{"否", "18", "否"},
		{"数据库", "18 38 58", "数据库"},
		{"a\U00020000b", "18 28 48", "a\U00020000b"},
		{"a\u0301b", "18", `<tspan x="18">á</tspan><tspan x="28">b</tspan>`},
		{"远端\u200b", "18", `<tspan x="18">远</tspan><tspan x="38">端​</tspan>`},
		{"<&", "18 28", "&lt;&amp;"},
	} {
		positions, content := ipQualityPositionedText(test.text, 18)
		if positions != test.positions || content != test.content {
			t.Fatalf("positioned %q = (%q, %q), want (%q, %q)", test.text, positions, content, test.positions, test.content)
		}
	}
}

func TestIPQualityRenderPaintsBackgroundsBeforeText(t *testing.T) {
	svg := mustRenderIPQualitySVG(t, "\x1b[3m9|\x1b[42m 低 \x1b[43m 中 \x1b[0m")
	if strings.LastIndex(svg, "<rect") > strings.Index(svg, "<text") {
		t.Fatal("a following background can erase preceding glyphs")
	}
}

// The browser fixture must exercise production SVG output, not hand-written
// artwork that can pass while the actual report renderer regresses.
func TestIPQualityBrowserFixtureMatchesRenderer(t *testing.T) {
	for _, family := range []int{4, 6} {
		text := ipQualityPrintedFixture + ipQualityAlignedFixture
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

func TestIPQualityRenderAlignsHeadersWithBadges(t *testing.T) {
	svg := mustRenderIPQualitySVG(t, ipQualityAlignedFixture)
	lines := ipQualityRenderedLines(svg)
	if len(lines) != 4 {
		t.Fatalf("rebuilt %d lines, want 4: %q", len(lines), lines)
	}
	headerColumn := ipQualityCellColumn(lines[0], "ChatGPT")
	labelColumn := ipQualityCellColumn(lines[1], "仅APP")
	if headerColumn != labelColumn-1 {
		t.Fatalf("ChatGPT sits at cell %d but its badge label sits at cell %d", headerColumn, labelColumn)
	}
	badgeX := -1
	for _, match := range ipQualityBadgeRect.FindAllStringSubmatch(svg, -1) {
		if match[5] == ipQualityYellow {
			badgeX, _ = strconv.Atoi(match[1])
		}
	}
	if want := ipQualityRenderPaddingX + headerColumn*ipQualityRenderCellWidth; badgeX != want {
		t.Fatalf("badge starts at x=%d, want %d", badgeX, want)
	}
	runs := ipQualityDrawnRuns(svg)
	if run := ipQualityRunStartingAt(runs, badgeX+ipQualityRenderCellWidth); run == nil || !strings.HasPrefix(run.content, "仅") {
		t.Fatalf("no badge label drawn at x=%d: %+v", badgeX+ipQualityRenderCellWidth, runs)
	}
}

// ipQualityCellColumn is the terminal cell a token starts at within a line.
func ipQualityCellColumn(line, token string) int {
	at := strings.Index(line, token)
	if at < 0 {
		return -1
	}
	return ipQualityDisplayWidth(line[:at])
}

func ipQualityRunStartingAt(runs []ipQualityDrawnRun, x int) *ipQualityDrawnRun {
	for index := range runs {
		if runs[index].x == x {
			return &runs[index]
		}
	}
	return nil
}

func TestIPQualityRenderKeepsTerminalStyles(t *testing.T) {
	svg := mustRenderIPQualitySVG(t, "\x1b[1m\x1b[3m\x1b[4m报告\x1b[0m")
	for _, want := range []string{`font-weight="bold"`, `text-decoration="underline"`} {
		if !strings.Contains(svg, want) {
			t.Fatalf("rendered report is missing %s", want)
		}
	}
	if strings.Contains(svg, `font-style="italic"`) {
		t.Fatal("a full-width run was drawn with a synthetic oblique")
	}
}

// Full-width runs stay upright even when a fallback font synthesizes italic;
// Latin runs retain the upstream emphasis.
func TestIPQualityRenderKeepsItalicOnLatinRunsOnly(t *testing.T) {
	svg := mustRenderIPQualitySVG(t, "\x1b[3mMaxmind 数据库\x1b[0m\x1b[3mIPinfo\x1b[0m")
	runs := ipQualityDrawnRuns(svg)
	italic := map[string]bool{}
	for _, run := range runs {
		italic[run.content] = strings.Contains(run.attributes, `font-style="italic"`)
	}
	for _, latin := range []string{"Maxmind ", "IPinfo"} {
		applied, drawn := italic[latin]
		if !drawn {
			t.Fatalf("Latin run %q is missing from %+v", latin, runs)
		}
		if !applied {
			t.Fatalf("Latin run %q lost the italic upstream asked for: %+v", latin, runs)
		}
	}
	applied, drawn := italic["数据库"]
	if !drawn {
		t.Fatalf("full-width run is missing from %+v", runs)
	}
	if applied {
		t.Fatalf("full-width run was drawn with a synthetic oblique: %+v", runs)
	}
}

func TestIPQualityRunsSplitWideAndNarrowCharacters(t *testing.T) {
	for _, test := range []struct {
		text string
		want []ipQualityRun
	}{
		{"库：ipapi", []ipQualityRun{{"库：", 4}, {"ipapi", 5}}},
		{"远端\u200b25", []ipQualityRun{{"远端\u200b", 4}, {"25", 2}}},
		{"\u200b报告", []ipQualityRun{{"\u200b", 0}, {"报告", 4}}},
		{"a\u0301报告b\u200d", []ipQualityRun{{"a\u0301", 1}, {"报告", 4}, {"b\u200d", 1}}},
		{"", nil},
	} {
		got := ipQualityRuns(test.text)
		if len(got) != len(test.want) {
			t.Fatalf("ipQualityRuns(%q) = %+v, want %+v", test.text, got, test.want)
		}
		for index := range got {
			if got[index] != test.want[index] {
				t.Fatalf("ipQualityRuns(%q) = %+v, want %+v", test.text, got, test.want)
			}
		}
	}
}

func TestIPQualityRunsLongTextAllocationBudget(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("a", ipQualityRenderMaxText),
		strings.Repeat("a\u0301", ipQualityRenderMaxText/3),
		strings.Repeat("报", ipQualityRenderMaxText/3),
	} {
		var runs []ipQualityRun
		allocations := testing.AllocsPerRun(5, func() { runs = ipQualityRuns(text) })
		if len(runs) != 1 || runs[0].text != text || runs[0].cells != ipQualityDisplayWidth(text) {
			t.Fatal("long run lost text or terminal cells")
		}
		// A uniform run needs a constant number of allocations, even when it
		// contains thousands of combining marks or multi-byte characters.
		if allocations > 8 {
			t.Fatalf("long run allocated %.0f objects; want at most 8", allocations)
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
	want := fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`,
		ipQualityRenderPaddingX, ipQualityRenderPaddingY,
		ipQualityDisplayWidth("绿底")*ipQualityRenderCellWidth, ipQualityRenderLineHeight, ipQualityGreen)
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
