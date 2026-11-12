package core

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"time"
)

const AgentFeatureNetworkDiagnostics = "network-diagnostics-v1"
const WireDiagnostics = "diagnostics"
const WireDiagnosticResult = "diagnostic_result"

// DiagnosticParameters are deliberately a closed command vocabulary.
// Size is ICMP payload for ping, total packet length for trace/MTR and
// application buffer/datagram length for iperf3.
type DiagnosticParameters struct {
	Tool       string `json:"tool"`
	Protocol   string `json:"protocol"`
	Provider   string `json:"provider"`
	Count      int    `json:"count"`
	IntervalMS int    `json:"interval_ms"`
	TimeoutMS  int    `json:"timeout_ms"`
	MaxHops    int    `json:"max_hops"`
	Size       int    `json:"size"`
	Port       int    `json:"port"`
	Duration   int    `json:"duration"`
	Streams    int    `json:"streams"`
	Bandwidth  int64  `json:"bandwidth"`
}

func (p DiagnosticParameters) Validate(ipv6 bool) error {
	bad := func(s string) error { return fmt.Errorf("网络诊断参数无效：%s", s) }
	if p.Tool != "ping" && p.Tool != "traceroute" && p.Tool != "mtr" && p.Tool != "iperf3" {
		return bad("工具")
	}
	if p.Protocol != "icmp" && p.Protocol != "tcp" && p.Protocol != "udp" {
		return bad("协议")
	}
	if p.Tool == "ping" && p.Protocol != "icmp" || p.Tool == "iperf3" && p.Protocol == "icmp" {
		return bad("工具不支持所选协议")
	}
	if p.Count < 1 {
		return bad("次数或轮数必须为正整数")
	}
	if p.Tool != "mtr" && p.Count > 100 {
		return bad("探测次数范围 1–100")
	}
	if p.IntervalMS < 200 || p.IntervalMS > 5000 || p.TimeoutMS < 100 || p.TimeoutMS > 5000 || p.MaxHops < 1 || p.MaxHops > 64 {
		return bad("间隔 200–5000 ms、超时 100–5000 ms、跳数 1–64")
	}
	if p.Port < 1 || p.Port > 65535 || p.Duration < 1 || p.Duration > 60 || p.Streams < 1 || p.Streams > 4 || p.Bandwidth < 0 || p.Bandwidth > 100000000 || p.Bandwidth > 0 && p.Bandwidth < 1000 {
		return bad("端口 1–65535、时长 1–60 s、并发流 1–4、带宽为默认值或 1 Kbit/s–100 Mbit/s")
	}
	if p.Tool == "iperf3" && p.Port < 1024 {
		return bad("测速端口范围 1024–65535")
	}
	if p.Size < 0 {
		return bad("不支持随机包长")
	}
	min, max := 0, 1400
	switch p.Tool {
	case "traceroute", "mtr":
		min = 28
		if p.Protocol == "tcp" {
			min = 44 // NextTrace v1.7.6 also requires its four-byte TCP probe tag
		}
		if ipv6 {
			min += 20
			if p.Protocol == "udp" {
				min += 2 // v1.7.6 IPv6 UDP probe tag
			}
		}
		max = 1500
	case "iperf3":
		min = 16 // iperf3 UDP timestamps and sequence header
		if p.Protocol == "tcp" {
			min = 1
			max = 1048576
		}
	}
	if p.Size < min || p.Size > max {
		return bad(fmt.Sprintf("包长范围 %d–%d 字节", min, max))
	}
	switch p.Provider {
	case "NextTrace-API", "IP.SB", "IPInfo", "IPInsight", "IPAPI.com", "IPInfoLocal", "ipdb.one", "chunzhen", "DN42", "disable-geoip":
	default:
		return bad("IP 查询提供方")
	}
	return nil
}

// TrafficReservation preserves conservative shared accounting. Default TCP
// has no bitrate target; shared execution also gets a byte ceiling, below.
func (p DiagnosticParameters) TrafficReservation() int64 {
	rate := p.Bandwidth
	if rate == 0 {
		if p.Protocol == "udp" {
			rate = 1000000
		} else {
			rate = 100000000
		}
	}
	return rate * int64(p.Streams) * int64(p.Duration) / 8 * 2
}

type DiagnosticGrant struct {
	Enabled  bool  `json:"enabled"`
	Revision int64 `json:"revision"`
}

type DiagnosticTool struct {
	AutoInstall bool   `json:"auto_install,omitempty"`
	Version     string `json:"version"`
	Error       string `json:"error,omitempty"`
}
type DiagnosticCapabilities struct {
	Tools     map[string]DiagnosticTool `json:"tools"`
	Providers map[string]string         `json:"providers"` // empty reason means locally prepared
	Addresses []string                  `json:"addresses"`
}
type DiagnosticEndpoint struct {
	AgentID    string `json:"agent_id"`
	InstanceID string `json:"instance_id"`
	Name       string `json:"name"`
	Address    string `json:"address"`
}
type DiagnosticNode struct {
	AgentID      string                 `json:"agent_id"`
	InstanceID   string                 `json:"instance_id"`
	Name         string                 `json:"name"`
	Online       bool                   `json:"online"`
	Context      string                 `json:"context"`
	Capabilities DiagnosticCapabilities `json:"capabilities"`
}
type DiagnosticRequest struct {
	TargetMode    string               `json:"target_mode,omitempty"` // empty: managed node; ip: numeric custom target
	RequestID     string               `json:"request_id"`
	Node1         DiagnosticEndpoint   `json:"node1"`
	Node2         DiagnosticEndpoint   `json:"node2"`
	Parameters    DiagnosticParameters `json:"parameters"`
	Bidirectional bool                 `json:"bidirectional"`
}

func DiagnosticAddress(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" || !a.IsGlobalUnicast() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.Is4In6() {
		return netip.Addr{}, fmt.Errorf("目标必须是节点已分配的 IPv4/IPv6 单播地址")
	}
	return a, nil
}

type DiagnosticCommand struct {
	ByteLimit  int64                `json:"byte_limit,omitempty"`
	ID         string               `json:"id"`
	SessionID  string               `json:"session_id"`
	Role       string               `json:"role"` // probe, server, client, stop
	Source     DiagnosticEndpoint   `json:"source"`
	Target     DiagnosticEndpoint   `json:"target"`
	Parameters DiagnosticParameters `json:"parameters"`
	Deadline   time.Time            `json:"deadline"`
}
type DiagnosticResult struct {
	Stage    string          `json:"stage,omitempty"`
	Sequence uint64          `json:"sequence,omitempty"`
	ID       string          `json:"id"`
	State    string          `json:"state"` // running, ready, succeeded, failed, canceled
	Output   json.RawMessage `json:"output,omitempty"`
	Stderr   string          `json:"stderr,omitempty"`
	Error    string          `json:"error,omitempty"`
	ExitCode int             `json:"exit_code"`
	Cleaned  bool            `json:"cleaned"`
	At       time.Time       `json:"at"`
}
type DiagnosticSession struct {
	ByteLimit int64                       `json:"byte_limit,omitempty"`
	ID        string                      `json:"id"`
	OwnerID   string                      `json:"owner_id"`
	Request   DiagnosticRequest           `json:"request"`
	State     string                      `json:"state"`
	Results   map[string]DiagnosticResult `json:"results"`
	CreatedAt time.Time                   `json:"created_at"`
	Deadline  time.Time                   `json:"deadline"`
}

func (r DiagnosticResult) Terminal() bool {
	return r.State == "succeeded" || r.State == "failed" || r.State == "canceled"
}
