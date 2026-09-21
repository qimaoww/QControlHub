package api

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Keep the native report's compact 14px text and ANSI palette. A fixed
// half-em cell grid keeps fallback fonts aligned with the background bands.
const (
	ipQualityRenderFontSize   = 14
	ipQualityRenderCellWidth  = 7
	ipQualityRenderLineHeight = 14
	ipQualityRenderMaxText    = 64 << 10
	ipQualityBackground       = "#000000"
	ipQualityDefault          = "#bbbbbb"
	ipQualityRed              = "#bb0000"
	ipQualityGreen            = "#00bb00"
	ipQualityYellow           = "#aa9900"
	ipQualityBlue             = "#0000bb"
	ipQualityMagenta          = "#bb00bb"
	ipQualityCyan             = "#00bbbb"
	ipQualityDim              = "#555555"
	ipQualityFontFamily       = "'Sarasa Mono SC', 'Noto Sans Mono CJK SC', SimHei, Consolas, 'DejaVu Sans Mono', monospace"
)

// ipQualityANSI maps the SGR codes upstream emits onto that palette. Plain black
// stays the page colour because upstream uses it for the mail-row separators,
// which are invisible in the terminal.
var ipQualityANSI = map[int]string{
	30: ipQualityBackground, 31: ipQualityRed, 32: ipQualityGreen, 33: ipQualityYellow,
	34: ipQualityBlue, 35: ipQualityMagenta, 36: ipQualityCyan, 37: ipQualityDefault,
	40: ipQualityBackground, 41: ipQualityRed, 42: ipQualityGreen, 43: ipQualityYellow,
	44: ipQualityBlue, 45: ipQualityMagenta, 46: ipQualityCyan, 47: ipQualityDefault,
	90: ipQualityDim, 91: ipQualityRed, 92: ipQualityGreen, 93: ipQualityYellow,
	94: ipQualityBlue, 95: ipQualityMagenta, 96: ipQualityCyan, 97: ipQualityDefault,
	100: ipQualityDim, 101: ipQualityRed, 102: ipQualityGreen, 103: ipQualityYellow,
	104: ipQualityBlue, 105: ipQualityMagenta, 106: ipQualityCyan, 107: ipQualityDefault,
}

type ipQualityCell struct {
	text      string
	fill      string
	bg        string
	bold      bool
	italic    bool
	underline bool
}

type ipQualityLine []ipQualityCell

// renderIPQualitySVG converts one printed report into the archived SVG. The
// text is bounded and escaped; only SGR sequences are interpreted.
func renderIPQualitySVG(text string) ([]byte, error) {
	if len(text) > ipQualityRenderMaxText || !utf8.ValidString(text) {
		return nil, errors.New("IPQuality 报告原文超过大小限制")
	}
	lines := ipQualityStyledLines(text)
	if len(lines) == 0 {
		return nil, errors.New("IPQuality 报告原文为空")
	}
	svg := buildIPQualitySVG(lines)
	if len(svg) == 0 || len(svg) > core.MaxIPQualityArchiveBytes {
		return nil, errors.New("IPQuality 渲染结果超出大小限制")
	}
	// Valid UTF-8 can still contain characters forbidden in XML (for example
	// U+FFFE). Validate the bounded document before it can be stored as success.
	decoder := xml.NewDecoder(bytes.NewReader(svg))
	for {
		if _, err := decoder.Token(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, errors.New("IPQuality 报告包含无法生成有效 SVG 的 XML 字符")
		}
	}
	return svg, nil
}

func ipQualityStyledLines(text string) []ipQualityLine {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "")
	raw := strings.Split(text, "\n")
	for len(raw) > 0 && strings.TrimSpace(raw[len(raw)-1]) == "" {
		raw = raw[:len(raw)-1]
	}
	lines := make([]ipQualityLine, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, ipQualityStyledLine(line))
	}
	return lines
}

// ipQualityStyledLine walks one line, turning SGR sequences into style changes
// and dropping every other control character.
func ipQualityStyledLine(raw string) ipQualityLine {
	line := ipQualityLine{}
	fill, bg, bold, italic, underline := ipQualityDefault, "", false, false, false
	var pending strings.Builder
	flush := func() {
		if pending.Len() == 0 {
			return
		}
		line = append(line, ipQualityCell{
			text: pending.String(), fill: fill, bg: bg,
			bold: bold, italic: italic, underline: underline,
		})
		pending.Reset()
	}
	for index := 0; index < len(raw); {
		if raw[index] == 0x1b {
			parameters, next := ipQualityCSISequence(raw, index)
			if next < 0 {
				// A stray escape byte is not a sequence: drop it and keep the
				// text on either side.
				index++
				continue
			}
			if raw[next-1] == 'm' {
				flush()
				for _, field := range strings.Split(parameters, ";") {
					code, err := strconv.Atoi(strings.TrimSpace(field))
					if err != nil {
						continue
					}
					switch {
					case code == 0:
						fill, bg, bold, italic, underline = ipQualityDefault, "", false, false, false
					case code == 1:
						bold = true
					case code == 3:
						italic = true
					case code == 4:
						underline = true
					case code == 22:
						bold = false
					case code == 23:
						italic = false
					case code == 24:
						underline = false
					case code == 39:
						fill = ipQualityDefault
					case code == 49:
						bg = ""
					default:
						color, ok := ipQualityANSI[code]
						if !ok {
							continue
						}
						// 90-97 and 100-107 are the bright variants of the
						// foreground and background ranges; classifying by
						// "code >= 40" would paint bright foreground text as a
						// background block.
						if code >= 40 && code <= 49 || code >= 100 && code <= 107 {
							bg = color
						} else {
							fill = color
						}
					}
				}
			}
			index = next
			continue
		}
		if character := raw[index]; character >= 0x20 || character == '\t' {
			pending.WriteByte(character)
		}
		index++
	}
	flush()
	return line
}

// ipQualityCSISequence reads the CSI sequence starting at index and returns its
// parameter bytes with the index just past it. The index is -1 when the bytes
// are not a complete sequence. Only the parameters are returned, so a caller can
// skip a sequence it does not understand without dropping the text after it.
func ipQualityCSISequence(raw string, index int) (string, int) {
	if index+2 > len(raw) || raw[index] != 0x1b || raw[index+1] != '[' {
		return "", -1
	}
	for offset := index + 2; offset < len(raw); offset++ {
		switch character := raw[offset]; {
		case character >= 0x40 && character <= 0x7e:
			return raw[index+2 : offset], offset + 1
		case character >= 0x20 && character <= 0x3f:
		default:
			return "", -1
		}
	}
	// The text ends mid-sequence, which a bounded capture can do: drop the
	// partial sequence instead of leaking its parameters as report text.
	return raw[index+2:], len(raw)
}

func buildIPQualitySVG(lines []ipQualityLine) []byte {
	columns := 0
	for _, line := range lines {
		if width := ipQualityLineWidth(line); width > columns {
			columns = width
		}
	}
	var builder strings.Builder
	// A fixed grid makes the archive independent of the client font metrics.
	width, height := (columns+2)*ipQualityRenderCellWidth, len(lines)*ipQualityRenderLineHeight
	fmt.Fprintf(&builder, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="%s" font-size="%d" font-style="normal" role="img" xml:space="preserve">`, width, height, width, height, ipQualityFontFamily, ipQualityRenderFontSize)
	fmt.Fprintf(&builder, `<title>IPQuality report</title><rect width="100%%" height="100%%" fill="%s"/>`, ipQualityBackground)
	builder.WriteString(`<g>`)
	// Native reports draw all backgrounds before text. Never let a later
	// rectangle erase an italic glyph or an adjacent row's ascender.
	for row, line := range lines {
		column := 1
		for _, cell := range line {
			width := ipQualityDisplayWidth(cell.text)
			if cell.bg != "" && width > 0 {
				fmt.Fprintf(&builder, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`, column*ipQualityRenderCellWidth, row*ipQualityRenderLineHeight, width*ipQualityRenderCellWidth, ipQualityRenderLineHeight, cell.bg)
			}
			column += width
		}
	}
	builder.WriteString(`</g><g dominant-baseline="central">`)
	for row, line := range lines {
		x := ipQualityRenderCellWidth
		y := row*ipQualityRenderLineHeight + ipQualityRenderLineHeight/2
		for _, cell := range line {
			for _, run := range ipQualityRuns(cell.text) {
				runWidth := run.cells * ipQualityRenderCellWidth
				length := ""
				if runWidth > 0 {
					length = fmt.Sprintf(` textLength="%d" lengthAdjust="spacingAndGlyphs"`, runWidth)
				}
				fmt.Fprintf(&builder, `<text x="%s" y="%d" fill="%s"%s%s xml:space="preserve">%s</text>`, ipQualityRunPositions(run.text, x), y, cell.fill, ipQualityStyleAttributes(cell), length, ipQualityEscape(run.text))
				x += runWidth
			}
		}
	}
	builder.WriteString(`</g></svg>`)
	return []byte(builder.String())
}

// Pin repeated glyph origins as well, since fallback fonts may kern pairs.
func ipQualityRunPositions(text string, x int) string {
	for _, character := range text {
		if ipQualityRuneWidth(character) == 0 {
			return strconv.Itoa(x)
		}
	}
	var positions strings.Builder
	for _, character := range text {
		if positions.Len() > 0 {
			positions.WriteByte(' ')
		}
		positions.WriteString(strconv.Itoa(x))
		x += ipQualityRuneWidth(character) * ipQualityRenderCellWidth
	}
	return positions.String()
}

// Reports use upright text throughout; bold and underline still carry emphasis.
func ipQualityStyleAttributes(cell ipQualityCell) string {
	attributes := ""
	if cell.bold {
		attributes += ` font-weight="bold"`
	}
	if cell.underline {
		attributes += ` text-decoration="underline"`
	}
	return attributes
}

// ipQualityRun contains a shaping cluster or repeated identical glyphs.
type ipQualityRun struct {
	text  string
	cells int
}

// Fit each glyph independently: even "monospace" can resolve to a proportional
// font on a minimal system. Group repeated characters (especially padding) to
// keep archives compact, and retain combining marks with their base glyph.
func ipQualityRuns(text string) []ipQualityRun {
	runs := make([]ipQualityRun, 0, 4)
	start, width := 0, 0
	var previous rune
	for index, character := range text {
		cells := ipQualityRuneWidth(character)
		if index > start && character != previous && cells != 0 {
			// Slice the original UTF-8 bytes once per run instead of copying
			// the growing prefix for every character. Combining marks remain
			// attached to the preceding run without adding any cells.
			runs = append(runs, ipQualityRun{text: text[start:index], cells: width})
			start, width = index, 0
		}
		width += cells
		previous = character
	}
	if start < len(text) {
		runs = append(runs, ipQualityRun{text: text[start:], cells: width})
	}
	return runs
}

func ipQualityLineWidth(line ipQualityLine) int {
	width := 0
	for _, cell := range line {
		width += ipQualityDisplayWidth(cell.text)
	}
	return width
}

// ipQualityDisplayWidth counts full-width characters as two terminal cells, so
// SVG columns line up like the terminal output.
func ipQualityDisplayWidth(text string) int {
	width := 0
	for _, character := range text {
		width += ipQualityRuneWidth(character)
	}
	return width
}

// ipQualityRuneWidth is the character's terminal cell count: zero for
// zero-width joiners and combining marks, two for full-width characters.
func ipQualityRuneWidth(character rune) int {
	switch {
	case character == 0x200B, character == 0x200C, character == 0x200D, character == 0xFEFF:
		return 0
	case character >= 0x0300 && character <= 0x036F:
		return 0
	case character >= 0x1100 && character <= 0x115F, character >= 0x2E80 && character <= 0xA4CF,
		character >= 0xAC00 && character <= 0xD7A3, character >= 0xF900 && character <= 0xFAFF,
		character >= 0xFE30 && character <= 0xFE6F, character >= 0xFF00 && character <= 0xFF60,
		character >= 0xFFE0 && character <= 0xFFE6, character >= 0x20000 && character <= 0x3FFFD:
		return 2
	default:
		return 1
	}
}

func ipQualityEscape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(text)
}
