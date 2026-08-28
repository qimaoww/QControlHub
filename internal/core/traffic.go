package core

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

type TrafficProtocol string

const (
	TrafficProtocolTCP  TrafficProtocol = "tcp"
	TrafficProtocolUDP  TrafficProtocol = "udp"
	TrafficProtocolBoth TrafficProtocol = "both"
)

func (protocol TrafficProtocol) Valid() bool {
	return protocol == TrafficProtocolTCP || protocol == TrafficProtocolUDP || protocol == TrafficProtocolBoth
}

type TrafficCycle string

const (
	TrafficCycleMonthly TrafficCycle = "monthly"
	TrafficCycleYearly  TrafficCycle = "yearly"
)

func (cycle TrafficCycle) Valid() bool {
	return cycle == TrafficCycleMonthly || cycle == TrafficCycleYearly
}

// PortTrafficPolicy is the control-plane definition and latest Agent-reported
// state for one listening port. Every discovered listener is monitored even
// when QuotaEnabled is false; LimitBytes and AutoBlock only take effect after
// an operator enables a quota.
type PortTrafficPolicy struct {
	ID                   string          `json:"id"`
	AgentID              string          `json:"agent_id"`
	Name                 string          `json:"name"`
	Engine               Engine          `json:"engine"`
	Port                 int             `json:"port"`
	Protocol             TrafficProtocol `json:"protocol"`
	Cycle                TrafficCycle    `json:"cycle"`
	CycleAnchor          time.Time       `json:"cycle_anchor"`
	LimitBytes           uint64          `json:"limit_bytes"`
	AutoBlock            bool            `json:"auto_block"`
	QuotaEnabled         bool            `json:"quota_enabled"`
	MonitoringEnabled    bool            `json:"monitoring_enabled"`
	Discovered           bool            `json:"discovered"`
	ResetGeneration      uint64          `json:"reset_generation"`
	ReceivedBytes        uint64          `json:"received_bytes"`
	SentBytes            uint64          `json:"sent_bytes"`
	UsedBytes            uint64          `json:"used_bytes"`
	ReceiveBPS           uint64          `json:"receive_bps"`
	SendBPS              uint64          `json:"send_bps"`
	PeriodStart          *time.Time      `json:"period_start,omitempty"`
	PeriodEnd            *time.Time      `json:"period_end,omitempty"`
	Blocked              bool            `json:"blocked"`
	EnforcementAvailable bool            `json:"enforcement_available"`
	EnforcementError     string          `json:"enforcement_error,omitempty"`
	LastReportedAt       *time.Time      `json:"last_reported_at,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// UnmarshalJSON preserves the original enforcement behavior when an older
// control plane or persisted Agent state does not contain auto_block.
func (policy *PortTrafficPolicy) UnmarshalJSON(data []byte) error {
	type wirePolicy PortTrafficPolicy
	decoded := wirePolicy{AutoBlock: true, MonitoringEnabled: true}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*policy = PortTrafficPolicy(decoded)
	return nil
}

type PortTrafficPolicyRequest struct {
	AgentID     string          `json:"agent_id"`
	Name        string          `json:"name"`
	Engine      Engine          `json:"engine"`
	Port        int             `json:"port"`
	Protocol    TrafficProtocol `json:"protocol"`
	Cycle       TrafficCycle    `json:"cycle"`
	CycleAnchor time.Time       `json:"cycle_anchor"`
	LimitBytes  uint64          `json:"limit_bytes"`
	AutoBlock   *bool           `json:"auto_block,omitempty"`
}

// PortTrafficEndpoint is a listener discovered in a node-owned core
// configuration. It is deliberately separate from PortTrafficPolicy: a
// configured listener exists before an operator decides whether to attach a
// quota to it.
type PortTrafficEndpoint struct {
	AgentID         string          `json:"agent_id"`
	Name            string          `json:"name"`
	Engine          Engine          `json:"engine"`
	Port            int             `json:"port"`
	Protocol        TrafficProtocol `json:"protocol"`
	ConfigVersion   int             `json:"config_version"`
	ConfigUpdatedAt time.Time       `json:"config_updated_at"`
}

type PortTrafficUsage struct {
	PolicyID             string    `json:"policy_id"`
	ResetGeneration      uint64    `json:"reset_generation"`
	ReceivedBytes        uint64    `json:"received_bytes"`
	SentBytes            uint64    `json:"sent_bytes"`
	UsedBytes            uint64    `json:"used_bytes"`
	ReceiveBPS           uint64    `json:"receive_bps"`
	SendBPS              uint64    `json:"send_bps"`
	PeriodStart          time.Time `json:"period_start"`
	PeriodEnd            time.Time `json:"period_end"`
	Blocked              bool      `json:"blocked"`
	EnforcementAvailable bool      `json:"enforcement_available"`
	EnforcementError     string    `json:"enforcement_error,omitempty"`
}

// PortTrafficDailyUsage is the durable UTC-day aggregate for one monitored
// port. It keeps a policy metadata snapshot so history remains meaningful
// after a policy is edited or removed.
type PortTrafficDailyUsage struct {
	PolicyID        string          `json:"policy_id"`
	AgentID         string          `json:"agent_id"`
	Name            string          `json:"name"`
	Engine          Engine          `json:"engine"`
	Port            int             `json:"port"`
	Protocol        TrafficProtocol `json:"protocol"`
	Day             string          `json:"day"`
	ReceivedBytes   uint64          `json:"received_bytes"`
	SentBytes       uint64          `json:"sent_bytes"`
	UsedBytes       uint64          `json:"used_bytes"`
	PeakReceiveBPS  uint64          `json:"peak_receive_bps"`
	PeakSendBPS     uint64          `json:"peak_send_bps"`
	SampleCount     uint64          `json:"sample_count"`
	FirstReportedAt time.Time       `json:"first_reported_at"`
	LastReportedAt  time.Time       `json:"last_reported_at"`
}

func ValidPortTrafficPolicyID(value string) bool {
	if len(value) != 20 || !strings.HasPrefix(value, "trf_") {
		return false
	}
	for _, character := range value[4:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func NormalizePortTrafficPolicyRequest(request PortTrafficPolicyRequest, now time.Time) (PortTrafficPolicyRequest, error) {
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.Name = strings.TrimSpace(request.Name)
	request.CycleAnchor = UTCDate(request.CycleAnchor)
	if request.AgentID == "" {
		return PortTrafficPolicyRequest{}, errors.New("agent_id is required")
	}
	if request.Name == "" || utf8.RuneCountInString(request.Name) > 100 {
		return PortTrafficPolicyRequest{}, errors.New("name is required and must not exceed 100 characters")
	}
	if !request.Engine.Valid() {
		return PortTrafficPolicyRequest{}, errors.New("engine must be mihomo, xray, sing-box, or ss-rust")
	}
	if request.Port < 1 || request.Port > 65535 {
		return PortTrafficPolicyRequest{}, errors.New("port must be between 1 and 65535")
	}
	if !request.Protocol.Valid() {
		return PortTrafficPolicyRequest{}, errors.New("protocol must be tcp, udp, or both")
	}
	if !request.Cycle.Valid() {
		return PortTrafficPolicyRequest{}, errors.New("cycle must be monthly or yearly")
	}
	if request.CycleAnchor.IsZero() {
		return PortTrafficPolicyRequest{}, errors.New("cycle_anchor is required")
	}
	if request.CycleAnchor.After(UTCDate(now)) {
		return PortTrafficPolicyRequest{}, errors.New("cycle_anchor cannot be in the future")
	}
	if request.LimitBytes == 0 || request.LimitBytes > math.MaxInt64 {
		return PortTrafficPolicyRequest{}, errors.New("limit_bytes must be between 1 and 9223372036854775807")
	}
	if request.AutoBlock == nil {
		enabled := true
		request.AutoBlock = &enabled
	}
	return request, nil
}

func UTCDate(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

// TrafficPeriodAt returns the calendar period containing now. The anchor day
// is retained across months and years, clamping only when a calendar period
// has fewer days (for example, January 31 resets on February 28/29).
func TrafficPeriodAt(anchor time.Time, cycle TrafficCycle, now time.Time) (time.Time, time.Time, error) {
	anchor = UTCDate(anchor)
	now = now.UTC()
	if anchor.IsZero() || !cycle.Valid() {
		return time.Time{}, time.Time{}, errors.New("invalid traffic period")
	}
	if now.Before(anchor) {
		return anchor, addTrafficPeriods(anchor, cycle, 1), nil
	}
	periods := now.Year() - anchor.Year()
	if cycle == TrafficCycleMonthly {
		periods = periods*12 + int(now.Month()-anchor.Month())
	}
	start := addTrafficPeriods(anchor, cycle, periods)
	if start.After(now) {
		periods--
		start = addTrafficPeriods(anchor, cycle, periods)
	}
	return start, addTrafficPeriods(anchor, cycle, periods+1), nil
}

func addTrafficPeriods(anchor time.Time, cycle TrafficCycle, periods int) time.Time {
	year, month := anchor.Year(), anchor.Month()
	if cycle == TrafficCycleMonthly {
		monthIndex := year*12 + int(month) - 1 + periods
		year = monthIndex / 12
		month = time.Month(monthIndex%12 + 1)
	} else {
		year += periods
	}
	day := anchor.Day()
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
