package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DashboardProbeMaxPairsPerOwner = 24
	DashboardProbeMaxHops          = 10
	DashboardProbeHistoryLimit     = 20
	DashboardProbeMinIntervalSec   = 15
	DashboardProbeMaxIntervalSec   = 3600
	DashboardProbeDefaultCount     = 5
	DashboardProbeDefaultInterval  = 200
	DashboardProbeDefaultTimeout   = 1000
	DashboardProbeDefaultSize      = 56
)

// DashboardProbeDirectionMetrics holds parsed latency and packet-loss counters
// for one probe direction (A -> B or B -> A).
type DashboardProbeDirectionMetrics struct {
	State       string   `json:"state"`
	Sent        int      `json:"sent,omitempty"`
	Received    int      `json:"received,omitempty"`
	LossPercent float64  `json:"loss_percent"`
	MinMS       *float64 `json:"min_ms,omitempty"`
	AvgMS       *float64 `json:"avg_ms,omitempty"`
	MaxMS       *float64 `json:"max_ms,omitempty"`
	StdevMS     *float64 `json:"stdev_ms,omitempty"`
	Reached     bool     `json:"reached"`
	Error       string   `json:"error,omitempty"`
}

// DashboardProbeHopMetrics holds per-segment forward and reverse latency/loss
// metrics for one hop (Nodes[hop_index] <-> Nodes[hop_index+1]).
type DashboardProbeHopMetrics struct {
	HopIndex int                             `json:"hop_index"`
	From     string                          `json:"from"`
	To       string                          `json:"to"`
	Forward  *DashboardProbeDirectionMetrics `json:"forward,omitempty"`
	Reverse  *DashboardProbeDirectionMetrics `json:"reverse,omitempty"`
}

// DashboardProbeNodeStatus carries live resolved name, region, and probe
// readiness for each hop node in an ordered monitoring chain.
type DashboardProbeNodeStatus struct {
	AgentID    string `json:"agent_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Region     string `json:"region,omitempty"`
	Address    string `json:"address"`
	Online     bool   `json:"online"`
	Granted    bool   `json:"granted"`
	Ping       bool   `json:"ping"`
}

// DashboardProbeSample summarizes a single completed chain/pair probe run.
type DashboardProbeSample struct {
	ID        string                          `json:"id"`
	SessionID string                          `json:"session_id"`
	State     string                          `json:"state"`
	Forward   *DashboardProbeDirectionMetrics `json:"forward,omitempty"`
	Reverse   *DashboardProbeDirectionMetrics `json:"reverse,omitempty"`
	Hops      []DashboardProbeHopMetrics      `json:"hops,omitempty"`
	Error     string                          `json:"error,omitempty"`
	SampledAt time.Time                       `json:"sampled_at"`
}

// DashboardProbeAvailability describes live execution readiness for a saved
// A/B probe pair without mutating stored configuration.
type DashboardProbeAvailability struct {
	Ready        bool   `json:"ready"`
	Reason       string `json:"reason,omitempty"`
	Running      bool   `json:"running,omitempty"`
	Node1Online  bool   `json:"node1_online"`
	Node1Ping    bool   `json:"node1_ping"`
	Node1Granted bool   `json:"node1_granted"`
	Node2Online  bool   `json:"node2_online,omitempty"`
	Node2Ping    bool   `json:"node2_ping,omitempty"`
	Node2Granted bool   `json:"node2_granted,omitempty"`
}

// DashboardProbePair represents one persistent dashboard latency/loss probe
// target pair or multi-hop chain (A-B-C-D...) with latest result, history, and
// availability metadata.
type DashboardProbePair struct {
	ID                  string                     `json:"id"`
	Name                string                     `json:"name"`
	GroupName           string                     `json:"group_name,omitempty"`
	TargetMode          string                     `json:"target_mode"`
	Node1               DiagnosticEndpoint         `json:"node1"`
	Node2               DiagnosticEndpoint         `json:"node2"`
	Nodes               []DiagnosticEndpoint       `json:"nodes,omitempty"`
	Chain               []DashboardProbeNodeStatus `json:"chain,omitempty"`
	Node1Name           string                     `json:"node1_name,omitempty"`
	Node1Region         string                     `json:"node1_region,omitempty"`
	Node2Name           string                     `json:"node2_name,omitempty"`
	Node2Region         string                     `json:"node2_region,omitempty"`
	Bidirectional       bool                       `json:"bidirectional"`
	Parameters          DiagnosticParameters       `json:"parameters"`
	AutoEnabled         bool                       `json:"auto_enabled"`
	AutoIntervalSeconds int                        `json:"auto_interval_seconds"`
	LastSessionID       string                     `json:"last_session_id,omitempty"`
	ActiveState         string                     `json:"active_state,omitempty"`
	LastRunAt           *time.Time                 `json:"last_run_at,omitempty"`
	NextRunAt           *time.Time                 `json:"next_run_at,omitempty"`
	Revision            int64                      `json:"revision"`
	CreatedAt           time.Time                  `json:"created_at"`
	UpdatedAt           time.Time                  `json:"updated_at"`
	Latest              *DashboardProbeSample      `json:"latest,omitempty"`
	History             []DashboardProbeSample     `json:"history,omitempty"`
	Availability        DashboardProbeAvailability `json:"availability"`
}

// DashboardProbeListResponse wraps the caller's dashboard A/B probe pairs.
type DashboardProbeListResponse struct {
	Pairs []DashboardProbePair `json:"pairs"`
}

// DashboardProbePairRequest validates dashboard A/B or multi-hop chain creation/
// update payloads using the same address and parameter constraints as network_diagnostics.
type DashboardProbePairRequest struct {
	Name                string               `json:"name"`
	GroupName           string               `json:"group_name,omitempty"`
	TargetMode          string               `json:"target_mode"`
	Node1               DiagnosticEndpoint   `json:"node1"`
	Node2               DiagnosticEndpoint   `json:"node2"`
	Nodes               []DiagnosticEndpoint `json:"nodes,omitempty"`
	Bidirectional       bool                 `json:"bidirectional"`
	Parameters          DiagnosticParameters `json:"parameters"`
	AutoEnabled         bool                 `json:"auto_enabled"`
	AutoIntervalSeconds int                  `json:"auto_interval_seconds"`
	Revision            int64                `json:"revision,omitempty"`
}

// NormalizeAndValidate fills default Ping/interval bounds and validates every
// address, family, chain uniqueness, and target constraint cleanly.
func (r *DashboardProbePairRequest) NormalizeAndValidate() error {
	r.Name = strings.TrimSpace(r.Name)
	if utf8.RuneCountInString(r.Name) > 80 {
		return errors.New("探测名称不能超过 80 个字符")
	}
	r.GroupName = strings.TrimSpace(r.GroupName)
	if utf8.RuneCountInString(r.GroupName) > 40 {
		return errors.New("监测分组名称不能超过 40 个字符")
	}
	r.TargetMode = strings.TrimSpace(r.TargetMode)
	if r.TargetMode != "" && r.TargetMode != "ip" {
		return errors.New("目标模式无效")
	}

	var firstIs6 bool
	if len(r.Nodes) > 0 {
		if len(r.Nodes) < 2 {
			return errors.New("全程监测至少需要 2 个节点")
		}
		if len(r.Nodes) > DashboardProbeMaxHops {
			return fmt.Errorf("单条全程监测链路最多支持 %d 个节点", DashboardProbeMaxHops)
		}
		if r.TargetMode == "ip" && len(r.Nodes) != 2 {
			return errors.New("自定义 IP 目标仅支持单跳探测")
		}
		seenAgents := map[string]bool{}
		for i := range r.Nodes {
			ep := &r.Nodes[i]
			ep.AgentID = strings.TrimSpace(ep.AgentID)
			ep.InstanceID = strings.TrimSpace(ep.InstanceID)
			ep.Address = strings.TrimSpace(ep.Address)
			ep.Name = strings.TrimSpace(ep.Name)
			ip, err := DiagnosticAddress(ep.Address)
			if err != nil {
				return fmt.Errorf("第 %d 跳节点地址无效: %w", i+1, err)
			}
			ep.Address = ip.String()
			if i == 0 {
				firstIs6 = ip.Is6()
			} else if ip.Is6() != firstIs6 {
				return errors.New("全链路节点必须使用相同 IP 地址族")
			}
			isLastCustomIP := r.TargetMode == "ip" && i == len(r.Nodes)-1
			if isLastCustomIP {
				if r.Bidirectional || ep.AgentID != "" || ep.InstanceID != "" {
					return errors.New("自定义 IP 目标仅支持单向探测且不可绑定托管目标节点")
				}
				if r.Nodes[0].Address == ep.Address {
					return errors.New("目标 IP 不能与源地址相同")
				}
			} else {
				if ep.AgentID == "" {
					return fmt.Errorf("请选择第 %d 跳托管节点", i+1)
				}
				if seenAgents[ep.AgentID] {
					return errors.New("同一监测链路中不可重复选择相同节点")
				}
				seenAgents[ep.AgentID] = true
			}
		}
		r.Node1 = r.Nodes[0]
		r.Node2 = r.Nodes[len(r.Nodes)-1]
	} else {
		r.Node1.AgentID = strings.TrimSpace(r.Node1.AgentID)
		r.Node1.InstanceID = strings.TrimSpace(r.Node1.InstanceID)
		r.Node1.Address = strings.TrimSpace(r.Node1.Address)
		r.Node2.AgentID = strings.TrimSpace(r.Node2.AgentID)
		r.Node2.InstanceID = strings.TrimSpace(r.Node2.InstanceID)
		r.Node2.Address = strings.TrimSpace(r.Node2.Address)

		if r.Node1.AgentID == "" {
			return errors.New("请选择源节点 A")
		}
		a, err := DiagnosticAddress(r.Node1.Address)
		if err != nil {
			return fmt.Errorf("源节点地址无效: %w", err)
		}
		b, err := DiagnosticAddress(r.Node2.Address)
		if err != nil {
			return fmt.Errorf("目标地址无效: %w", err)
		}
		firstIs6 = a.Is6()
		r.Node1.Address = a.String()
		r.Node2.Address = b.String()
		if a.Is6() != b.Is6() {
			return errors.New("源地址与目标地址必须使用相同 IP 地址族")
		}
		if r.TargetMode == "ip" && r.Node1.Address == r.Node2.Address {
			return errors.New("目标 IP 不能与源地址相同")
		}
		if r.TargetMode == "ip" {
			if r.Bidirectional || r.Node2.AgentID != "" || r.Node2.InstanceID != "" {
				return errors.New("自定义 IP 目标仅支持单向探测且不可绑定托管目标节点")
			}
		} else {
			if r.Node2.AgentID == "" {
				return errors.New("请选择目标节点 B")
			}
			if r.Node1.AgentID == r.Node2.AgentID {
				return errors.New("源节点与目标节点不能相同")
			}
		}
		r.Nodes = []DiagnosticEndpoint{r.Node1, r.Node2}
	}

	if r.Parameters.Tool == "" {
		r.Parameters.Tool = "ping"
	}
	if r.Parameters.Tool != "ping" {
		return errors.New("仪表盘延迟探测仅支持 ping 工具")
	}
	if r.Parameters.Protocol == "" {
		r.Parameters.Protocol = "icmp"
	}
	if r.Parameters.Provider == "" {
		r.Parameters.Provider = "disable-geoip"
	}
	if r.Parameters.Count == 0 {
		r.Parameters.Count = DashboardProbeDefaultCount
	}
	if r.Parameters.IntervalMS == 0 {
		r.Parameters.IntervalMS = DashboardProbeDefaultInterval
	}
	if r.Parameters.TimeoutMS == 0 {
		r.Parameters.TimeoutMS = DashboardProbeDefaultTimeout
	}
	if r.Parameters.MaxHops == 0 {
		r.Parameters.MaxHops = 30
	}
	if r.Parameters.Size == 0 {
		r.Parameters.Size = DashboardProbeDefaultSize
	}
	if r.Parameters.Port == 0 {
		r.Parameters.Port = 5201
	}
	if r.Parameters.Duration == 0 {
		r.Parameters.Duration = 5
	}
	if r.Parameters.Streams == 0 {
		r.Parameters.Streams = 1
	}
	if err := r.Parameters.Validate(firstIs6); err != nil {
		return err
	}
	if r.AutoIntervalSeconds == 0 {
		r.AutoIntervalSeconds = 60
	}
	if r.AutoIntervalSeconds < DashboardProbeMinIntervalSec || r.AutoIntervalSeconds > DashboardProbeMaxIntervalSec {
		return fmt.Errorf("自动探测周期需在 %d 至 %d 秒之间", DashboardProbeMinIntervalSec, DashboardProbeMaxIntervalSec)
	}
	return nil
}
