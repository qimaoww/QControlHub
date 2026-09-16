package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"time"
	"unicode/utf8"
)

const (
	AgentFeatureIPQuality   = "ip-quality-v1"
	MaxIPQualityResultBytes = 128 << 10
	IPQualityTimeout        = 10 * time.Minute
	// Leave time for WSS result delivery before a disconnected execution is retried.
	IPQualityLeaseTimeout = IPQualityTimeout + 2*time.Minute
)

// IPQuality retains the upstream database-specific values. The databases do
// not share a scoring scale; missing results must not become zero-risk scores.
type IPQualityResult struct {
	Reports []json.RawMessage `json:"reports"`
}

type IPQualityRecord struct {
	TaskID     string           `json:"task_id"`
	AgentID    string           `json:"agent_id"`
	Status     TaskStatus       `json:"status"`
	Error      string           `json:"error,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	StartedAt  *time.Time       `json:"started_at,omitempty"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
	Result     *IPQualityResult `json:"result,omitempty"`
}

type IPQualitySchedule struct {
	AgentID   string    `json:"agent_id"`
	Enabled   bool      `json:"enabled"`
	NextRunAt time.Time `json:"next_run_at"`
}

type IPQualityHistory struct {
	Date      string              `json:"date"`
	Timezone  string              `json:"timezone"`
	Records   []IPQualityRecord   `json:"records"`
	Schedules []IPQualitySchedule `json:"schedules"`
}

// ParseIPQualityReports consumes the JSON object stream produced by ip.sh -o:
// dual-stack output contains two adjacent objects, not a JSON array.
func ParseIPQualityReports(content []byte) (IPQualityResult, error) {
	if len(content) > MaxIPQualityResultBytes || !utf8.Valid(content) {
		return IPQualityResult{}, errors.New("IPQuality report is too large or is not UTF-8")
	}
	result := IPQualityResult{}
	decoder := json.NewDecoder(bytes.NewReader(content))
	for {
		var report json.RawMessage
		err := decoder.Decode(&report)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return IPQualityResult{}, fmt.Errorf("invalid IPQuality JSON: %w", err)
		}
		result.Reports = append(result.Reports, report)
		if len(result.Reports) > 2 {
			return IPQualityResult{}, errors.New("IPQuality returned more than two address families")
		}
	}
	return NormalizeIPQualityResult(&result)
}

// NormalizeIPQualityResult is also used at the authenticated result boundary;
// neither a process exit status nor an Agent's success flag proves a report.
func NormalizeIPQualityResult(input *IPQualityResult) (IPQualityResult, error) {
	if input == nil || len(input.Reports) < 1 || len(input.Reports) > 2 {
		return IPQualityResult{}, errors.New("IPQuality must return one or two reports")
	}
	result := IPQualityResult{Reports: make([]json.RawMessage, 0, len(input.Reports))}
	families := map[bool]bool{}
	total := 0
	for _, raw := range input.Reports {
		total += len(raw)
		if total > MaxIPQualityResultBytes || !utf8.Valid(raw) {
			return IPQualityResult{}, errors.New("IPQuality report is too large or is not UTF-8")
		}
		var report map[string]json.RawMessage
		if err := json.Unmarshal(raw, &report); err != nil || report == nil {
			return IPQualityResult{}, errors.New("IPQuality report must be an object")
		}
		var head struct {
			IP string `json:"IP"`
		}
		if err := json.Unmarshal(report["Head"], &head); err != nil {
			return IPQualityResult{}, errors.New("IPQuality report is missing its header")
		}
		address, err := netip.ParseAddr(head.IP)
		if err != nil || !address.IsGlobalUnicast() || address.IsPrivate() || address.Zone() != "" {
			return IPQualityResult{}, errors.New("IPQuality report must identify a full public IP address")
		}
		ipv4 := address.Unmap().Is4()
		if families[ipv4] {
			return IPQualityResult{}, errors.New("IPQuality returned duplicate address families")
		}
		families[ipv4] = true
		for _, section := range []string{"Info", "Type", "Score", "Factor", "Media", "Mail"} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(report[section], &fields); err != nil || fields == nil {
				return IPQualityResult{}, fmt.Errorf("IPQuality report has an invalid %s section", section)
			}
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return IPQualityResult{}, err
		}
		result.Reports = append(result.Reports, json.RawMessage(compact.Bytes()))
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > MaxIPQualityResultBytes {
		return IPQualityResult{}, errors.New("encoded IPQuality result exceeds the size limit")
	}
	return result, nil
}
