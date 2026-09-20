package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// The panel draws the archived report itself from the stored JSON, so a
// detection never depends on the upstream upload endpoint. Every label, column
// width and score threshold below is transcribed from upstream's ip.sh output
// so the image matches what the detector prints in a terminal.
const (
	ipQualityRenderColumns    = 72
	ipQualityRenderCellWidth  = 10
	ipQualityRenderLineHeight = 22
	ipQualityRenderFontSize   = 16
	ipQualityRenderPaddingX   = 18
	ipQualityRenderPaddingY   = 16
	ipQualityScoreBarColumns  = 64
)

const (
	// Palette sampled from the detector's terminal output.
	ipQualityColorDefault = "#d6deeb"
	ipQualityColorLabel   = "#21c7a8"
	ipQualityColorGreen   = "#22da6e"
	ipQualityColorYellow  = "#c5e478"
	ipQualityColorRed     = "#ef5350"
	ipQualityColorDim     = "#7b8794"
	ipQualityColorLink    = "#78b8eb"
	ipQualityColorWhite   = "#ffffff"
	ipQualityColorBlack   = "#011627"
	ipQualityColorBanner  = "#d6deeb"
	ipQualityBackground   = "#011627"

	ipQualityBadgeGreen   = "#22da6e"
	ipQualityBadgeYellow  = "#c5e478"
	ipQualityBadgeRed     = "#ef5350"
	ipQualityBadgeNeutral = "#3a4657"

	ipQualityBarGreen  = "#22da6e"
	ipQualityBarYellow = "#c5e478"
	ipQualityBarRed    = "#ef5350"

	ipQualityFontFamily     = "ui-monospace, SFMono-Regular, Menlo, Consolas, 'DejaVu Sans Mono', monospace"
	ipQualityProvenanceNote = "本图由 QControlHub 面板根据上游 JSON 重绘，未使用上游上传的报告链接"
)

type ipQualityRenderReport struct {
	Head struct {
		IP      string `json:"IP"`
		Command string `json:"Command"`
		Time    string `json:"Time"`
		Version string `json:"Version"`
	} `json:"Head"`
	Info struct {
		ASN          string `json:"ASN"`
		Organization string `json:"Organization"`
		DMS          string `json:"DMS"`
		Map          string `json:"Map"`
		TimeZone     string `json:"TimeZone"`
		Type         string `json:"Type"`
		City         struct {
			Name         string `json:"Name"`
			PostalCode   string `json:"PostalCode"`
			Subdivisions string `json:"Subdivisions"`
		} `json:"City"`
		Region           ipQualityRenderRegion `json:"Region"`
		Continent        ipQualityRenderRegion `json:"Continent"`
		RegisteredRegion ipQualityRenderRegion `json:"RegisteredRegion"`
	} `json:"Info"`
	Type struct {
		Usage   map[string]string `json:"Usage"`
		Company map[string]string `json:"Company"`
	} `json:"Type"`
	Score  map[string]string                     `json:"Score"`
	Factor map[string]map[string]json.RawMessage `json:"Factor"`
	Media  map[string]struct {
		Status string `json:"Status"`
		Region string `json:"Region"`
		Type   string `json:"Type"`
	} `json:"Media"`
	Mail map[string]json.RawMessage `json:"Mail"`
}

type ipQualityRenderRegion struct {
	Code string `json:"Code"`
	Name string `json:"Name"`
}

type ipQualityCell struct {
	text string
	fill string
	bg   string
	bold bool
}

type ipQualityLine []ipQualityCell

type ipQualityProviderName struct {
	key     string
	display string
}

func ipQualityRun(text, fill string) ipQualityCell {
	return ipQualityCell{text: text, fill: fill}
}

// renderIPQualitySVG renders one stored report. Content is bounded before it is
// returned so the caller can store it in ip_quality_archives.
func renderIPQualitySVG(report json.RawMessage) ([]byte, error) {
	var parsed ipQualityRenderReport
	if err := json.Unmarshal(report, &parsed); err != nil {
		return nil, errors.New("IPQuality 报告无法解析")
	}
	if parsed.Head.IP == "" {
		return nil, errors.New("IPQuality 报告缺少地址")
	}
	svg := buildIPQualitySVG(ipQualityReportLines(parsed))
	if len(svg) == 0 || len(svg) > core.MaxIPQualityArchiveBytes {
		return nil, errors.New("IPQuality 渲染结果超出大小限制")
	}
	return svg, nil
}

func ipQualityReportLines(report ipQualityRenderReport) []ipQualityLine {
	lines := ipQualityHeaderLines(report)
	lines = append(lines, ipQualityBasicLines(report)...)
	lines = append(lines, ipQualityTypeLines(report)...)
	lines = append(lines, ipQualityScoreLines(report)...)
	lines = append(lines, ipQualityFactorLines(report)...)
	lines = append(lines, ipQualityMediaLines(report)...)
	lines = append(lines, ipQualityMailLines(report)...)
	lines = append(lines, ipQualityLine{ipQualityRun(strings.Repeat("=", ipQualityRenderColumns), ipQualityColorDim)})
	lines = append(lines, ipQualityLine{ipQualityRun(ipQualityProvenanceNote, ipQualityColorDim)})
	return lines
}

// ipQualityLite reports upstream's fallback: when it cannot reach the full
// database set it only queries IPinfo, ipapi, ipregistry and DB-IP, so no risk
// score is available and at most three providers report a usage type. The JSON
// carries no lite marker, so the panel infers it from that shape.
func ipQualityLite(report ipQualityRenderReport) bool {
	for _, raw := range report.Score {
		if ipQualityClean(raw) != "" {
			return false
		}
	}
	reported := 0
	for _, value := range report.Type.Usage {
		if ipQualityClean(value) != "" {
			reported++
		}
	}
	return reported > 0 && reported <= 3
}

func ipQualityHeaderLines(report ipQualityRenderReport) []ipQualityLine {
	banner := strings.Repeat("#", ipQualityRenderColumns)
	command := report.Head.Command
	if command == "" {
		command = "bash <(curl -sL https://Check.Place) -I"
	}
	return []ipQualityLine{
		{ipQualityRun(banner, ipQualityColorBanner)},
		{ipQualityRun(ipQualityCenter(ipQualityTitle(report), ipQualityRenderColumns), ipQualityColorDefault)},
		{ipQualityRun(ipQualityCenter("https://github.com/xykt/IPQuality", ipQualityRenderColumns), ipQualityColorLink)},
		{ipQualityRun(ipQualityCenter(command, ipQualityRenderColumns), ipQualityColorDefault)},
		{ipQualityRun("        "+"报告时间："+report.Head.Time+"  "+"脚本版本："+report.Head.Version, ipQualityColorDefault)},
		{ipQualityRun(banner, ipQualityColorBanner)},
	}
}

func ipQualityTitle(report ipQualityRenderReport) string {
	if ipQualityLite(report) {
		return "IP质量体检报告(Lite)：" + report.Head.IP
	}
	return "IP质量体检报告：" + report.Head.IP
}

func ipQualityBasicLines(report ipQualityRenderReport) []ipQualityLine {
	info := report.Info
	heading := "一、基础信息（Maxmind 数据库）"
	if ipQualityLite(report) {
		heading = "一、基础信息（IPinfo 数据库）"
	}
	lines := []ipQualityLine{{ipQualityRun(heading, ipQualityColorDefault)}}
	field := func(label, value, fill string) ipQualityLine {
		return ipQualityLine{
			ipQualityRun(ipQualityPad(label, 24), ipQualityColorLabel),
			ipQualityRun(value, fill),
		}
	}
	if asn := ipQualityClean(info.ASN); asn != "" {
		lines = append(lines,
			field("自治系统号：", "AS"+asn, ipQualityColorGreen),
			field("组织：", info.Organization, ipQualityColorGreen))
	} else {
		lines = append(lines, field("自治系统号：", "未分配", ipQualityColorDefault))
	}
	if ipQualityClean(info.DMS) != "" && ipQualityClean(info.Map) != "" {
		lines = append(lines,
			field("坐标：", info.DMS, ipQualityColorGreen),
			field("地图：", info.Map, ipQualityColorLink))
	}
	if city := ipQualityJoin(info.City.Subdivisions, info.City.Name, info.City.PostalCode); city != "" {
		lines = append(lines, field("城市：", city, ipQualityColorGreen))
	}
	if location := ipQualityLocation(info.Region, info.Continent); location != "" {
		lines = append(lines, field("使用地：", location, ipQualityColorGreen))
	}
	if code := ipQualityClean(info.RegisteredRegion.Code); code != "" {
		lines = append(lines, field("注册地：", "["+code+"]"+ipQualityClean(info.RegisteredRegion.Name), ipQualityColorGreen))
	}
	if zone := ipQualityClean(info.TimeZone); zone != "" {
		lines = append(lines, field("时区：", zone, ipQualityColorGreen))
	}
	if label := ipQualityClean(info.Type); label != "" {
		bg := ipQualityBadgeRed
		if strings.Contains(label, "原生") {
			bg = ipQualityBadgeGreen
		}
		lines = append(lines, ipQualityLine{
			ipQualityRun(ipQualityPad("IP类型：", 24), ipQualityColorLabel),
			{text: " " + label + " ", fill: ipQualityColorWhite, bg: bg, bold: true},
		})
	}
	return lines
}

// ipQualityLocation matches upstream's "[cc]name, [cc]name" location line.
func ipQualityLocation(primary, secondary ipQualityRenderRegion) string {
	parts := make([]string, 0, 2)
	for _, region := range []ipQualityRenderRegion{primary, secondary} {
		if code := ipQualityClean(region.Code); code != "" {
			parts = append(parts, "["+code+"]"+ipQualityClean(region.Name))
		}
	}
	return strings.Join(parts, ", ")
}

// ipQualityTypeColumns and ipQualityTypeHeader mirror upstream's stype output:
// ten-column badge cells under an irregular italic provider list.
var ipQualityTypeColumns = []ipQualityProviderName{
	{key: "IPinfo", display: "IPinfo"},
	{key: "ipregistry", display: "ipregistry"},
	{key: "ipapi", display: "ipapi"},
	{key: "IP2LOCATION", display: "IP2Location"},
	{key: "AbuseIPDB", display: "AbuseIPDB"},
}

var ipQualityTypeColumnsLite = ipQualityTypeColumns[:3]

const (
	ipQualityTypeHeader     = "   IPinfo    ipregistry    ipapi    IP2Location   AbuseIPDB "
	ipQualityTypeHeaderLite = "   IPinfo    ipregistry    ipapi "
)

func ipQualityTypeLines(report ipQualityRenderReport) []ipQualityLine {
	columns, header := ipQualityTypeColumns, ipQualityTypeHeader
	if ipQualityLite(report) {
		columns, header = ipQualityTypeColumnsLite, ipQualityTypeHeaderLite
	}
	lines := []ipQualityLine{{ipQualityRun("二、IP类型属性", ipQualityColorDefault)}}
	lines = append(lines, ipQualityLine{
		ipQualityRun(ipQualityPad("数据库：", 11), ipQualityColorLabel),
		ipQualityRun(header, ipQualityColorDefault),
	})
	for _, row := range []struct {
		label  string
		values map[string]string
	}{{"使用类型：", report.Type.Usage}, {"公司类型：", report.Type.Company}} {
		line := ipQualityLine{ipQualityRun(ipQualityPad(row.label, 11), ipQualityColorLabel)}
		for _, provider := range columns {
			line = append(line, ipQualityUsageBadge(row.values[provider.key])...)
		}
		lines = append(lines, line)
	}
	return lines
}

// ipQualityUsageBadge renders upstream's ten-column "   机房   " cell. The
// stored value is already localized (机房/商业/家宽/...); the English enums are
// mapped only for reports produced with -E.
func ipQualityUsageBadge(value string) []ipQualityCell {
	text := ipQualityClean(value)
	if text == "" {
		return []ipQualityCell{ipQualityRun(strings.Repeat(" ", 10), ipQualityColorDim)}
	}
	text = ipQualityUsageLabel(text)
	return []ipQualityCell{
		ipQualityRun("   ", ipQualityColorDefault),
		{text: " " + ipQualityPad(text, 4) + " ", fill: ipQualityColorWhite, bg: ipQualityUsageColor(text), bold: true},
		ipQualityRun("   ", ipQualityColorDefault),
	}
}

func ipQualityUsageLabel(value string) string {
	switch strings.ToLower(value) {
	case "business", "commercial":
		return "商业"
	case "isp", "line isp":
		return "家宽"
	case "hosting", "data center/web hosting/transit", "cdn":
		return "机房"
	case "education", "university/college/school":
		return "教育"
	case "government":
		return "政府"
	case "banking":
		return "银行"
	case "organization":
		return "组织"
	case "military":
		return "军队"
	case "library":
		return "图书馆"
	case "mobile":
		return "手机"
	case "spider":
		return "蜘蛛"
	case "reserved":
		return "保留"
	default:
		return value
	}
}

func ipQualityUsageColor(text string) string {
	switch text {
	case "机房", "蜘蛛", "CDN":
		return ipQualityBadgeRed
	case "家宽", "手机":
		return ipQualityBadgeGreen
	default:
		return ipQualityBadgeYellow
	}
}

// ipQualityScoreProvider mirrors one sscore_text call: thresholds p3..p5 and
// the p6 offset that shifts the coloured bar segments.
type ipQualityScoreProvider struct {
	key        string
	display    string
	p3, p4, p5 float64
	p6         int
}

var ipQualityScoreProviders = []ipQualityScoreProvider{
	{key: "IP2LOCATION", display: "IP2Location", p3: 33, p4: 66, p5: 99, p6: 13},
	{key: "SCAMALYTICS", display: "Scamalytics", p3: 20, p4: 60, p5: 100, p6: 13},
	{key: "ipapi", display: "ipapi", p3: 85, p4: 300, p5: 10000, p6: 7},
	{key: "AbuseIPDB", display: "AbuseIPDB", p3: 25, p4: 25, p5: 100, p6: 11},
	{key: "IPQS", display: "IPQS", p3: 75, p4: 85, p5: 100, p6: 6},
	{key: "DBIP", display: "DB-IP", p3: 33, p4: 66, p5: 100, p6: 7},
}

func ipQualityScoreLines(report ipQualityRenderReport) []ipQualityLine {
	lines := []ipQualityLine{{ipQualityRun("三、风险评分", ipQualityColorDefault)}}
	lines = append(lines, ipQualityLine{
		ipQualityRun(ipQualityPad("风险等级：", 16), ipQualityColorLabel),
		{text: "极低         低 ", fill: ipQualityColorWhite, bg: ipQualityBarGreen, bold: true},
		{text: "      中等      ", fill: ipQualityColorWhite, bg: ipQualityBarYellow, bold: true},
		{text: " 高         极高", fill: ipQualityColorWhite, bg: ipQualityBarRed, bold: true},
	})
	for _, provider := range ipQualityScoreProviders {
		raw, ok := report.Score[provider.key]
		if !ok {
			continue
		}
		display := ipQualityClean(raw)
		number, err := ipQualityParseScore(display)
		if display == "" || err != nil {
			continue
		}
		if provider.key == "ipapi" {
			number *= 10000
		}
		line := ipQualityLine{ipQualityRun(provider.display+"：", ipQualityColorLabel)}
		line = append(line, ipQualityScoreBar(display, number, provider)...)
		line = append(line, ipQualityRun(ipQualityRiskLevel(number, provider), ipQualityRiskColor(number, provider)))
		lines = append(lines, line)
	}
	return lines
}

// ipQualityScoreBar transcribes upstream's sscore_text: a coloured bar whose
// length and text position follow the score.
func ipQualityScoreBar(text string, score float64, provider ipQualityScoreProvider) ipQualityLine {
	p3, p4, p5, p6 := provider.p3, provider.p4, provider.p5, float64(provider.p6)
	tmplen := 0.0
	switch {
	case score >= p4:
		tmplen = 49 + 15*(score-p4)/(p5-p4) - p6
	case score >= p3:
		tmplen = 33 + 16*(score-p3)/(p4-p3) - p6
	case score >= 0:
		tmplen = 17 + 16*score/p3 - p6
	}
	total := int(tmplen)
	if total < 0 {
		total = 0
	}
	if total > ipQualityScoreBarColumns {
		total = ipQualityScoreBarColumns
	}
	bar := []rune(strings.Repeat(" ", total))
	runes := []rune(text)
	if start := total - len(runes) - 1; start >= 0 {
		copy(bar[start:], runes)
	}
	if total > 0 {
		bar[total-1] = '|'
	}
	segment := func(from, to int) string {
		if from < 0 {
			from = 0
		}
		if to > total {
			to = total
		}
		if from >= to {
			return ""
		}
		return string(bar[from:to])
	}
	green := int(16 - p6)
	if green < 0 {
		green = 0
	}
	// Upstream colours the second segment green, the third yellow and the last
	// red; the leading segment stays on the terminal background.
	return ipQualityLine{
		{text: segment(0, green), fill: ipQualityColorWhite},
		{text: segment(green, green+16), fill: ipQualityColorWhite, bg: ipQualityBarGreen, bold: true},
		{text: segment(green+16, green+32), fill: ipQualityColorWhite, bg: ipQualityBarYellow, bold: true},
		{text: segment(green+32, total), fill: ipQualityColorWhite, bg: ipQualityBarRed, bold: true},
	}
}

func ipQualityRiskLevel(score float64, provider ipQualityScoreProvider) string {
	switch {
	case score < provider.p3:
		return "低风险"
	case score < provider.p4:
		return "中风险"
	default:
		return "高风险"
	}
}

func ipQualityRiskColor(score float64, provider ipQualityScoreProvider) string {
	switch {
	case score < provider.p3:
		return ipQualityColorGreen
	case score < provider.p4:
		return ipQualityColorYellow
	default:
		return ipQualityColorRed
	}
}

var ipQualityFactorRows = []struct{ key, label string }{
	{"CountryCode", "地区：  "},
	{"Proxy", "代理：  "},
	{"Tor", "Tor：   "},
	{"VPN", "VPN：   "},
	{"Server", "服务器："},
	{"Abuser", "滥用：  "},
	{"Robot", "机器人："},
}

var ipQualityFactorColumns = []ipQualityProviderName{
	{key: "IP2LOCATION", display: "IP2Location"},
	{key: "ipapi", display: "ipapi"},
	{key: "ipregistry", display: "ipregistry"},
	{key: "IPQS", display: "IPQS"},
	{key: "SCAMALYTICS", display: "Scamalytics"},
	{key: "ipdata", display: "ipdata"},
	{key: "IPinfo", display: "IPinfo"},
	{key: "DBIP", display: "DB-IP"},
}

var ipQualityFactorColumnsLite = []ipQualityProviderName{
	{key: "ipapi", display: "ipapi"},
	{key: "ipregistry", display: "ipregistry"},
	{key: "IPinfo", display: "IPinfo"},
	{key: "DBIP", display: "DB-IP"},
}

const (
	ipQualityFactorHeader     = "IP2Location ipapi ipregistry IPQS Scamalytics ipdata IPinfo DB-IP"
	ipQualityFactorHeaderLite = "ipapi ipregistry IPinfo DB-IP"
)

func ipQualityFactorLines(report ipQualityRenderReport) []ipQualityLine {
	columns, header := ipQualityFactorColumns, ipQualityFactorHeader
	if ipQualityLite(report) {
		columns, header = ipQualityFactorColumnsLite, ipQualityFactorHeaderLite
	}
	lines := []ipQualityLine{{ipQualityRun("四、风险因子", ipQualityColorDefault)}}
	lines = append(lines, ipQualityLine{
		ipQualityRun(ipQualityPad("库：", 8), ipQualityColorLabel),
		ipQualityRun(header, ipQualityColorDefault),
	})
	for _, factor := range ipQualityFactorRows {
		line := ipQualityLine{
			ipQualityRun(ipQualityPad(factor.label, 8), ipQualityColorLabel),
			ipQualityRun("  ", ipQualityColorDefault),
		}
		for _, provider := range columns {
			line = append(line, ipQualityFactorCell(report.Factor[factor.key][provider.key]))
		}
		lines = append(lines, line)
	}
	return lines
}

// ipQualityFactorCell renders upstream's "  <value>    " eight-column cell.
func ipQualityFactorCell(raw json.RawMessage) ipQualityCell {
	text := ipQualityClean(string(raw))
	cell := ipQualityRun(" 无 ", ipQualityColorGreen)
	switch {
	case text == "true":
		cell = ipQualityRun(" 是 ", ipQualityColorRed)
	case text == "false":
		cell = ipQualityRun(" 否 ", ipQualityColorGreen)
	case len([]rune(text)) == 2:
		cell = ipQualityRun("["+text+"]", ipQualityColorGreen)
	}
	cell.text = ipQualityPad(cell.text, 4) + "    "
	return cell
}

var ipQualityMediaServices = []struct{ key, display string }{
	{"TikTok", "TikTok"}, {"DisneyPlus", "Disney+"}, {"Netflix", "Netflix"}, {"Youtube", "Youtube"},
	{"AmazonPrimeVideo", "AmazonPV"}, {"Reddit", "Reddit"}, {"ChatGPT", "ChatGPT"},
}

func ipQualityMediaLines(report ipQualityRenderReport) []ipQualityLine {
	lines := []ipQualityLine{{ipQualityRun("五、流媒体及AI服务解锁检测", ipQualityColorDefault)}}
	header := ipQualityLine{ipQualityRun(ipQualityPad("服务商：", 9), ipQualityColorLabel)}
	for _, service := range ipQualityMediaServices {
		header = append(header, ipQualityRun(ipQualityPad(service.display, 9), ipQualityColorDefault))
	}
	statuses := ipQualityLine{ipQualityRun(ipQualityPad("状态：", 9), ipQualityColorLabel)}
	regions := ipQualityLine{ipQualityRun(ipQualityPad("地区：", 9), ipQualityColorLabel)}
	types := ipQualityLine{ipQualityRun(ipQualityPad("方式：", 9), ipQualityColorLabel)}
	for _, service := range ipQualityMediaServices {
		media := report.Media[service.key]
		statuses = append(statuses, ipQualityMediaStatus(media.Status)...)
		regions = append(regions, ipQualityRun(ipQualityRegionCell(media.Region), ipQualityColorGreen))
		types = append(types, ipQualityMediaType(media.Type)...)
	}
	return append(lines, header, statuses, regions, types)
}

// ipQualityMediaStatus mirrors upstream's smedia status strings. Each cell is
// nine columns wide with only the label filled; the value is already localized.
func ipQualityMediaStatus(status string) []ipQualityCell {
	text := ipQualityClean(status)
	if text == "" {
		return []ipQualityCell{ipQualityRun(strings.Repeat(" ", 9), ipQualityColorDim)}
	}
	label, bg, inner := ipQualityMediaStatusBadge(text)
	return ipQualityMediaCell(" "+ipQualityPad(label, inner)+" ", bg)
}

func ipQualityMediaStatusBadge(text string) (string, string, int) {
	switch text {
	case "解锁", "Yes":
		return "解锁", ipQualityBadgeGreen, 4
	case "屏蔽", "屏蔽IP", "Block", "No":
		return "屏蔽", ipQualityBadgeRed, 4
	case "失败", "Failed":
		return "失败", ipQualityBadgeRed, 4
	case "中国", "China":
		return "中国", ipQualityBadgeRed, 4
	case "禁会员", "NoPrem.":
		return "禁会员", ipQualityBadgeRed, 6
	case "待支持", "Pending":
		return "待支持", ipQualityBadgeYellow, 6
	case "仅自制", "NF.Only":
		return "仅自制", ipQualityBadgeYellow, 6
	case "仅网页", "WebOnly":
		return "仅网页", ipQualityBadgeYellow, 6
	case "仅APP", "APPOnly":
		return "仅APP", ipQualityBadgeYellow, 6
	case "机房", "IDC":
		return "机房", ipQualityBadgeYellow, 4
	default:
		return text, ipQualityBadgeNeutral, ipQualityDisplayWidth(text)
	}
}

// ipQualityMediaType mirrors upstream's smedia native/dns strings.
func ipQualityMediaType(mediaType string) []ipQualityCell {
	switch ipQualityClean(mediaType) {
	case "原生", "Native":
		return ipQualityMediaCell(" 原生 ", ipQualityBadgeGreen)
	case "DNS", "ViaDNS":
		return ipQualityMediaCell("  DNS ", ipQualityBadgeYellow)
	default:
		return []ipQualityCell{ipQualityRun(strings.Repeat(" ", 9), ipQualityColorDim)}
	}
}

func ipQualityMediaCell(badge, bg string) []ipQualityCell {
	cells := []ipQualityCell{{text: badge, fill: ipQualityColorWhite, bg: bg, bold: true}}
	if pad := 9 - ipQualityDisplayWidth(badge); pad > 0 {
		cells = append(cells, ipQualityRun(strings.Repeat(" ", pad), ipQualityColorDefault))
	}
	return cells
}

// ipQualityRegionCell mirrors upstream's nine-column "  [XX]   " region cell.
func ipQualityRegionCell(region string) string {
	if code := ipQualityClean(region); code != "" {
		return ipQualityPad("  ["+code+"]", 9)
	}
	return strings.Repeat(" ", 9)
}

func ipQualityMailLines(report ipQualityRenderReport) []ipQualityLine {
	lines := []ipQualityLine{{ipQualityRun("六、邮局连通性及黑名单检测", ipQualityColorDefault)}}
	port25 := ipQualityBool(report.Mail["Port25"])
	switch {
	case port25 == nil:
		lines = append(lines, ipQualityLine{ipQualityRun("本地25端口出站：", ipQualityColorLabel), ipQualityRun("未知", ipQualityColorDim)})
	case *port25:
		lines = append(lines, ipQualityLine{ipQualityRun("本地25端口出站：", ipQualityColorLabel), ipQualityRun("可用", ipQualityColorGreen)})
	default:
		lines = append(lines, ipQualityLine{ipQualityRun("本地25端口出站：", ipQualityColorLabel), ipQualityRun("阻断", ipQualityColorRed)})
	}
	if port25 != nil && !*port25 {
		lines = append(lines, ipQualityLine{
			ipQualityRun("通信：", ipQualityColorLabel),
			ipQualityRun("远端25端口不可达", ipQualityColorRed),
		})
		return lines
	}
	provider := ipQualityLine{ipQualityRun("通信：", ipQualityColorLabel)}
	for _, service := range []struct{ key, display string }{
		{"Gmail", "Gmail"}, {"Outlook", "Outlook"}, {"Yahoo", "Yahoo"}, {"Apple", "Apple"}, {"QQ", "QQ"},
		{"MailRU", "MailRU"}, {"AOL", "AOL"}, {"GMX", "GMX"}, {"MailCOM", "MailCOM"}, {"163", "163"},
		{"Sohu", "Sohu"}, {"Sina", "Sina"},
	} {
		state := ipQualityBool(report.Mail[service.key])
		mark, bg := "+", ipQualityBadgeGreen
		if state != nil && !*state {
			mark, bg = "-", ipQualityBadgeRed
		}
		provider = append(provider,
			ipQualityRun(mark, ipQualityColorBlack),
			ipQualityCell{text: service.display, fill: ipQualityColorWhite, bg: bg, bold: true})
	}
	lines = append(lines, provider)
	var blacklist struct {
		Total       json.RawMessage `json:"Total"`
		Clean       json.RawMessage `json:"Clean"`
		Marked      json.RawMessage `json:"Marked"`
		Blacklisted json.RawMessage `json:"Blacklisted"`
	}
	if raw, ok := report.Mail["DNSBlacklist"]; ok && json.Unmarshal(raw, &blacklist) == nil {
		lines = append(lines, ipQualityLine{
			ipQualityRun("IP地址黑名单数据库：  ", ipQualityColorLabel),
			ipQualityRun("有效 ", ipQualityColorLabel),
			ipQualityRun(ipQualityClean(string(blacklist.Total)), ipQualityColorDefault),
			ipQualityRun("   正常 ", ipQualityColorGreen),
			ipQualityRun(ipQualityClean(string(blacklist.Clean)), ipQualityColorDefault),
			ipQualityRun("   已标记 ", ipQualityColorYellow),
			ipQualityRun(ipQualityClean(string(blacklist.Marked)), ipQualityColorDefault),
			ipQualityRun("   黑名单 ", ipQualityColorRed),
			ipQualityRun(ipQualityClean(string(blacklist.Blacklisted)), ipQualityColorDefault),
		})
	}
	return lines
}

func buildIPQualitySVG(lines []ipQualityLine) []byte {
	columns := ipQualityRenderColumns
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

func ipQualityClean(value string) string {
	value = strings.TrimSpace(value)
	if value == "null" || value == `"null"` || value == `""` {
		return ""
	}
	return strings.Trim(value, `"`)
}

func ipQualityJoin(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if clean := ipQualityClean(part); clean != "" {
			kept = append(kept, clean)
		}
	}
	return strings.Join(kept, ", ")
}

func ipQualityParseScore(value string) (float64, error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(value), "%")
	var number float64
	if _, err := fmt.Sscanf(trimmed, "%f", &number); err != nil {
		return 0, err
	}
	return number, nil
}

func ipQualityBool(raw json.RawMessage) *bool {
	if len(raw) == 0 {
		return nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return &value
}

func ipQualityCenter(text string, columns int) string {
	width := ipQualityDisplayWidth(text)
	if width >= columns {
		return text
	}
	return strings.Repeat(" ", (columns-width)/2) + text
}

func ipQualityPad(text string, columns int) string {
	width := ipQualityDisplayWidth(text)
	if width >= columns {
		return text
	}
	return text + strings.Repeat(" ", columns-width)
}

// ipQualityDisplayWidth counts full-width characters as two terminal cells, so
// SVG columns line up like the upstream output.
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
