package api

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

type ipQualityRenderScenario struct{ name, ip, kind string }

var ipQualityRenderScenarios = []ipQualityRenderScenario{
	{"ip-quality-report", "203.0.113.1", "mixed"},
	{"ip-quality-report-v6", "2001:0db8:1234:5678:90ab:cdef:1234:5678", "mixed"},
	{"ip-quality-clean", "192.0.2.1", "clean"},
	{"ip-quality-medium", "198.51.100.25", "medium"},
	{"ip-quality-high", "203.0.113.254", "high"},
	{"ip-quality-missing", "203.0.113.2", "missing"},
	{"ip-quality-v6-short", "2001:db8::1", "clean"},
	{"ip-quality-v6-missing", "2001:db8::2", "missing"},
}

// Match upstream show_head/calc_padding: center the actual title and address
// in the 72-column report. Replacing an IPv4 literal without recentering is not
// a valid IPv6 report and hides address overflow bugs behind a wider canvas.
func ipQualityScenarioText(t *testing.T, scenario ipQualityRenderScenario) string {
	rows := strings.Split(strings.TrimSuffix(ipQualityNativeFixture(t), "\n"), "\n")
	title := "IP质量体检报告："
	padding := (72 - ipQualityDisplayWidth(title) - len(scenario.ip)) / 2
	rows[1] = strings.Repeat(" ", max(0, padding)) + "\x1b[1m" + title + "\x1b[36m" + scenario.ip + "\x1b[0m"
	if scenario.kind != "mixed" {
		labels := []string{"IP2Location", "Scamalytics", "ipapi", "AbuseIPDB", "IPQS"}
		for i, label := range labels {
			switch scenario.kind {
			case "clean":
				value := "0"
				if label == "ipapi" {
					value = "0.00%"
				}
				rows[22+i] = ipQualityScenarioScore(label, value, "极低风险", 32, 16)
			case "medium":
				value := "45"
				if label == "ipapi" {
					value = "2.00%"
				}
				rows[22+i] = ipQualityScenarioScore(label, value, "中风险", 33, 38)
			case "high":
				value := "95"
				if label == "ipapi" {
					value = "99.99%"
				}
				rows[22+i] = ipQualityScenarioScore(label, value, "极高风险", 31, 63)
			case "missing":
				rows[22+i] = "\x1b[36m" + label + "：\x1b[90m    无数据\x1b[0m"
			}
		}
		for row := 29; row <= 35; row++ {
			if scenario.kind == "high" {
				rows[row] = strings.ReplaceAll(strings.ReplaceAll(rows[row], "否", "是"), "无", "是")
				rows[row] = strings.ReplaceAll(rows[row], "\x1b[0;1;32m", "\x1b[0;1;31m")
			}
			if scenario.kind == "missing" {
				rows[row] = strings.ReplaceAll(strings.ReplaceAll(rows[row], "否", "无"), "是", "无")
				rows[row] = strings.ReplaceAll(rows[row], "\x1b[0;1;31m", "\x1b[0;1;32m")
			}
		}
		status, color := "解锁", 42
		if scenario.kind == "high" {
			status, color = "屏蔽", 41
		}
		if scenario.kind == "medium" {
			status, color = "仅APP", 43
		}
		if scenario.kind == "missing" {
			status, color = "待支持", 43
		}
		rows[38] = "\x1b[36m状态：   \x1b[0m" + strings.Repeat(ipQualityScenarioBadge(status, color), 7)
		if scenario.kind == "clean" {
			rows[39] = "\x1b[36m地区：   \x1b[32m" + strings.Repeat("  [HK]   ", 7) + "\x1b[0m"
			rows[40] = "\x1b[36m方式：   \x1b[0m" + strings.Repeat(ipQualityScenarioBadge("原生", 42), 7)
		}
		if scenario.kind == "high" || scenario.kind == "missing" {
			rows[39] = "\x1b[36m地区：   \x1b[0m" + strings.Repeat("         ", 7)
			rows[40] = "\x1b[36m方式：   \x1b[0m" + strings.Repeat("         ", 7)
		}
	}
	if strings.Contains(scenario.ip, ":") {
		// show_mail emits the DNS blacklist summary only for IPv4.
		rows = append(rows[:44], rows[45:]...)
	}
	return strings.Join(rows, "\n") + "\n"
}

func ipQualityScenarioScore(label, value, risk string, color, marker int) string {
	prefix := label + "："
	column := ipQualityDisplayWidth(prefix)
	content := strings.Repeat(" ", marker-column-len(value)) + value + "|"
	var line strings.Builder
	line.WriteString("\x1b[36m" + prefix + "\x1b[37;1m")
	for _, c := range content {
		switch column {
		case 16:
			line.WriteString("\x1b[42m")
		case 32:
			line.WriteString("\x1b[43m")
		case 48:
			line.WriteString("\x1b[41m")
		}
		line.WriteRune(c)
		column++
	}
	fmt.Fprintf(&line, "\x1b[0;%dm%s\x1b[0m", color, risk)
	return line.String()
}

func ipQualityScenarioBadge(label string, color int) string {
	width := ipQualityDisplayWidth(label) + 2
	left := (9 - width) / 2
	return strings.Repeat(" ", left) + fmt.Sprintf("\x1b[%d;37m %s \x1b[0m", color, label) + strings.Repeat(" ", 9-width-left)
}

func TestIPQualityBrowserFixtureMatchesRenderer(t *testing.T) {
	for _, scenario := range ipQualityRenderScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			text := ipQualityScenarioText(t, scenario)
			want := mustRenderIPQualitySVG(t, text)
			lines := ipQualityRenderedLines(want)
			header := lines[1]
			left := len(header) - len(strings.TrimLeft(header, " "))
			right := 72 - ipQualityDisplayWidth(header)
			if right < 0 || right-left > 1 || left > right {
				t.Fatalf("address title is not centered: %q, margins %d/%d", header, left, right)
			}
			if !strings.Contains(header, scenario.ip) {
				t.Fatal("title lost the address")
			}
			if strings.Contains(scenario.ip, ":") && strings.Contains(strings.Join(lines, "\n"), "黑名单数据库：") {
				t.Fatal("IPv6 contains IPv4-only DNS blacklist data")
			}
			path := "../../frontend/testdata/" + scenario.name + ".svg"
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
		})
	}
}
