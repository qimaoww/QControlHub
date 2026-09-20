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
	text string
	fill string
	bg   string
	bold bool
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
	fill, bg, bold := ipQualityDefault, "", false
	var pending strings.Builder
	flush := func() {
		if pending.Len() == 0 {
			return
		}
		line = append(line, ipQualityCell{text: pending.String(), fill: fill, bg: bg, bold: bold})
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
					fill, bg, bold = ipQualityDefault, "", false
				case code == 1:
					bold = true
				case code == 22:
					bold = false
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
		x := ipQualityRenderPaddingX
		for _, cell := range line {
			cellWidth := ipQualityDisplayWidth(cell.text) * ipQualityRenderCellWidth
			if cell.bg != "" && cellWidth > 0 {
				fmt.Fprintf(&builder, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`,
					x, top, cellWidth, ipQualityRenderLineHeight, cell.bg)
			}
			if cell.text != "" {
				bold := ""
				if cell.bold {
					bold = ` font-weight="bold"`
				}
				fmt.Fprintf(&builder, `<text x="%d" y="%d" fill="%s"%s xml:space="preserve">%s</text>`,
					x, top+ipQualityRenderFontSize+2, cell.fill, bold, ipQualityEscape(cell.text))
			}
			x += cellWidth
		}
	}
	builder.WriteString(`</g></svg>`)
	return []byte(builder.String())
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
	for _, r := range text {
		switch {
		case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
			r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE6F, r >= 0xFF00 && r <= 0xFF60,
			r >= 0xFFE0 && r <= 0xFFE6, r >= 0x20000 && r <= 0x3FFFD:
			width += 2
		default:
			width++
		}
	}
	return width
}

func ipQualityEscape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(text)
}
