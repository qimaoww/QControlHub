package core

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SystemBBRStatus reports kernel defaults, not the algorithm of every existing
// socket. Qdiscs are actual interface queues; DefaultQdisc is only a default.
type SystemBBRStatus struct {
	CollectedAt          time.Time         `json:"collected_at"`
	Available            bool              `json:"available"`
	KernelRelease        string            `json:"kernel_release"`
	CongestionControl    string            `json:"congestion_control"`
	AvailableAlgorithms  []string          `json:"available_algorithms"`
	DefaultQdisc         string            `json:"default_qdisc"`
	Parameters           map[string]string `json:"parameters"`
	Qdiscs               []SystemQdisc     `json:"qdiscs"`
	QdiscError           string            `json:"qdisc_error,omitempty"`
	Persistence          string            `json:"persistence"`
	ConfiguredAlgorithm  string            `json:"configured_algorithm,omitempty"`
	ConfiguredQdisc      string            `json:"configured_qdisc,omitempty"`
	ConfiguredParameters map[string]string `json:"configured_parameters,omitempty"`
	Error                string            `json:"error,omitempty"`
}

type TCPSettings map[string]string

// TCPParameterRules is the same bounded vocabulary used by the API, Agent and
// UI. No arbitrary sysctl key, executable, path, or kernel module is accepted.
type TCPParameterRule struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Min     int64    `json:"min,omitempty"`
	Max     int64    `json:"max,omitempty"`
	Tuple   bool     `json:"tuple,omitempty"`
	Choices []string `json:"choices,omitempty"`
}

func TCPParameterRules() []TCPParameterRule {
	return []TCPParameterRule{
		{Key: "net.ipv4.tcp_congestion_control", Label: "拥塞控制算法", Choices: []string{"bbr", "bbr2", "bbr3", "cubic", "reno", "dctcp", "htcp", "westwood", "vegas"}},
		{Key: "net.core.default_qdisc", Label: "默认队列算法", Choices: []string{"fq", "fq_codel", "fq_pie", "pfifo_fast", "sfq", "cake"}},
		{Key: "net.ipv4.tcp_rmem", Label: "TCP 接收缓冲区：最小 / 默认 / 最大（字节）", Min: 1, Max: 1 << 30, Tuple: true},
		{Key: "net.ipv4.tcp_wmem", Label: "TCP 发送缓冲区：最小 / 默认 / 最大（字节）", Min: 1, Max: 1 << 30, Tuple: true},
		{Key: "net.core.rmem_max", Label: "接收缓冲区上限（字节）", Min: 4096, Max: 1 << 30},
		{Key: "net.core.wmem_max", Label: "发送缓冲区上限（字节）", Min: 4096, Max: 1 << 30},
		{Key: "net.core.somaxconn", Label: "监听连接队列上限", Min: 128, Max: 65535},
		{Key: "net.core.netdev_max_backlog", Label: "网卡接收积压队列上限", Min: 64, Max: 1000000},
		{Key: "net.ipv4.tcp_max_syn_backlog", Label: "TCP SYN 队列上限", Min: 128, Max: 1000000},
		{Key: "net.ipv4.tcp_mtu_probing", Label: "MTU 探测（0 关闭 / 1 黑洞检测 / 2 始终）", Max: 2},
		{Key: "net.ipv4.tcp_ecn", Label: "ECN（0 关闭 / 1 主动协商 / 2 被动接受）", Max: 2},
		{Key: "net.ipv4.tcp_fastopen", Label: "TCP Fast Open（0 关闭 / 1 客户端 / 2 服务端 / 3 双向）", Max: 3},
		{Key: "net.ipv4.tcp_sack", Label: "选择性确认 SACK（0 / 1）", Max: 1},
		{Key: "net.ipv4.tcp_window_scaling", Label: "窗口缩放（0 / 1）", Max: 1},
	}
}

func NormalizeTCPSettings(input TCPSettings) (TCPSettings, error) {
	if len(input) == 0 || len(input) > len(TCPParameterRules()) {
		return nil, fmt.Errorf("select between 1 and %d TCP parameters", len(TCPParameterRules()))
	}
	rules := map[string]TCPParameterRule{}
	for _, rule := range TCPParameterRules() {
		rules[rule.Key] = rule
	}
	result := TCPSettings{}
	for key, raw := range input {
		rule, ok := rules[key]
		if !ok {
			return nil, fmt.Errorf("unsupported TCP parameter %q", key)
		}
		if len(raw) > 100 || strings.ContainsAny(raw, "\r\n\x00") {
			return nil, fmt.Errorf("invalid value for %s", key)
		}
		value := strings.Join(strings.Fields(raw), " ")
		if len(rule.Choices) > 0 {
			if !slices.Contains(rule.Choices, value) {
				return nil, fmt.Errorf("unsupported value for %s", key)
			}
		} else {
			parts := strings.Fields(value)
			want := 1
			if rule.Tuple {
				want = 3
			}
			if len(parts) != want {
				return nil, fmt.Errorf("%s requires %d integers", key, want)
			}
			var previous int64
			for i, part := range parts {
				n, err := strconv.ParseInt(part, 10, 64)
				if err != nil || strings.Trim(part, "0123456789") != "" || n < rule.Min || n > rule.Max || (i > 0 && n < previous) {
					return nil, fmt.Errorf("%s requires ordered integers in [%d, %d]", key, rule.Min, rule.Max)
				}
				parts[i], previous = strconv.FormatInt(n, 10), n
			}
			value = strings.Join(parts, " ")
		}
		result[key] = value
	}
	return result, nil
}

type SystemQdisc struct {
	Device string `json:"device"`
	Kind   string `json:"kind"`
	Handle string `json:"handle,omitempty"`
	Parent string `json:"parent,omitempty"`
	Root   bool   `json:"root"`
}
