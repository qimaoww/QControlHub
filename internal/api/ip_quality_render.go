package api

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// The panel renders the detector's own printed report, so the archived image is
// exactly what ip.sh prints in a terminal: no layout is re-derived from the JSON
// and nothing can drift when upstream changes its output. Only the ANSI colour
// codes are translated, using the palette the reference terminal shows.
const (
	ipQualityRenderCellWidth  = 10
	ipQualityRenderLineHeight = 22
	ipQualityRenderFontSize   = 16
	ipQualityRenderPaddingX   = 18
	ipQualityRenderPaddingY   = 16
	ipQualityRenderMaxText    = 64 << 10
)

const (
	ipQualityBackground = "#011627"
	ipQualityDefault    = "#d6deeb"
	ipQualityRed        = "#ef5350"
	ipQualityGreen      = "#22da6e"
	ipQualityYellow     = "#c5e478"
	ipQualityBlue       = "#78b8eb"
	ipQualityMagenta    = "#c586c0"
	ipQualityCyan       = "#21c7a8"
	ipQualityFontFamily = "ui-monospace, SFMono-Regular, Menlo, Consolas, 'DejaVu Sans Mono', monospace"
)

// ipQualityANSI maps the SGR codes upstream emits onto that palette.
var ipQualityANSI = map[int]string{
	30: ipQualityBackground, 31: ipQualityRed, 32: ipQualityGreen, 33: ipQualityYellow,
	34: ipQualityBlue, 35: ipQualityMagenta, 36: ipQualityCyan, 37: ipQualityDefault,
	40: ipQualityBackground, 41: ipQualityRed, 42: ipQualityGreen, 43: ipQualityYellow,
	44: ipQualityBlue, 45: ipQualityMagenta, 46: ipQualityCyan, 47: ipQualityDefault,
	90: ipQualityBackground, 91: ipQualityRed, 92: ipQualityGreen, 93: ipQualityYellow,
	94: ipQualityBlue, 95: ipQualityMagenta, 96: ipQualityCyan, 97: ipQualityDefault,
	100: ipQualityBackground, 101: ipQualityRed, 102: ipQualityGreen, 103: ipQualityYellow,
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
		if raw[index] == 0x1b && index+1 < len(raw) && raw[index+1] == '[' {
			end := strings.IndexByte(raw[index+2:], 'm')
			if end < 0 {
				break
			}
			flush()
			for _, field := range strings.Split(raw[index+2:index+2+end], ";") {
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
					if code >= 40 {
						bg = color
					} else {
						fill = color
					}
				}
			}
			index += 2 + end + 1
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

func buildIPQualitySVG(lines []ipQualityLine) []byte {
	columns := 0
	for _, line := range lines {
		if width := ipQualityLineWidth(line); width > columns {
			columns = width
		}
	}
	width := ipQualityRenderPaddingX*2 + columns*ipQualityRenderCellWidth
	height := ipQualityRenderPaddingY*2 + len(lines)*ipQualityRenderLineHeight
	var builder strings.Builder
	fmt.Fprintf(&builder, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img">`, width, height, width, height)
	fmt.Fprintf(&builder, `<title>IPQuality report</title><rect width="100%%" height="100%%" fill="%s"/>`, ipQualityBackground)
	fmt.Fprintf(&builder, `<g font-family="%s" font-size="%d">`, ipQualityFontFamily, ipQualityRenderFontSize)
	for index, line := range lines {
		top := ipQualityRenderPaddingY + index*ipQualityRenderLineHeight
		baseline := top + ipQualityRenderFontSize + 2
		x := ipQualityRenderPaddingX
		for _, cell := range line {
			cellWidth := ipQualityDisplayWidth(cell.text) * ipQualityRenderCellWidth
			if cell.bg != "" && cellWidth > 0 {
				fmt.Fprintf(&builder, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`,
					x, top, cellWidth, ipQualityRenderLineHeight, cell.bg)
			}
			for _, run := range ipQualityRuns(cell.text) {
				runWidth := run.cells * ipQualityRenderCellWidth
				if run.text != "" {
					fmt.Fprintf(&builder, `<text x="%d" y="%d" fill="%s"%s%s xml:space="preserve">%s</text>`,
						x, baseline, cell.fill, ipQualityStyleAttributes(cell), ipQualityLengthAttributes(runWidth),
						ipQualityEscape(run.text))
				}
				x += runWidth
			}
		}
	}
	builder.WriteString(`</g></svg>`)
	return []byte(builder.String())
}

// ipQualityStyleAttributes renders the SGR state a run inherits.
func ipQualityStyleAttributes(cell ipQualityCell) string {
	attributes := ""
	if cell.bold {
		attributes += ` font-weight="bold"`
	}
	if cell.italic {
		attributes += ` font-style="italic"`
	}
	if cell.underline {
		attributes += ` text-decoration="underline"`
	}
	return attributes
}

// ipQualityLengthAttributes pins a run to the terminal grid. A browser picks a
// monospace face whose advance is not exactly one cell, so a run drawn at its
// natural advance would creep left of the columns below it; textLength forces
// every character to keep its cell.
func ipQualityLengthAttributes(width int) string {
	if width <= 0 {
		return ""
	}
	return fmt.Sprintf(` textLength="%d" lengthAdjust="spacingAndGlyphs"`, width)
}

// ipQualityRun is a maximal slice of one styled cell whose characters all
// occupy the same number of terminal cells, so it can be pinned as a unit.
type ipQualityRun struct {
	text  string
	cells int
}

// ipQualityRuns splits a cell so that narrow and wide characters are pinned
// separately: one textLength can only scale a run uniformly, and scaling a
// mixed run would still leave its wide characters off the grid.
func ipQualityRuns(text string) []ipQualityRun {
	runs := make([]ipQualityRun, 0, 4)
	unit := 0
	for _, character := range text {
		cells := ipQualityRuneWidth(character)
		switch {
		case len(runs) == 0:
			runs = append(runs, ipQualityRun{text: string(character), cells: cells})
			unit = cells
		case cells == unit:
			last := &runs[len(runs)-1]
			last.text += string(character)
			last.cells += cells
		case cells == 0:
			runs[len(runs)-1].text += string(character)
		default:
			runs = append(runs, ipQualityRun{text: string(character), cells: cells})
			unit = cells
		}
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
