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
// The panel renders its own report image from these bytes.
type IPQualityResult struct {
	Reports []json.RawMessage `json:"reports"`
	// ReportsText carries the detector's own printed report, one entry per
	// address family, including its ANSI colour codes. The panel renders the
	// archived image from this text, so the image is exactly what the terminal
	// shows instead of a re-derived approximation.
	ReportsText []string `json:"reports_text,omitempty"`
}

type IPQualityRecord struct {
	TaskID     string                 `json:"task_id"`
	AgentID    string                 `json:"agent_id"`
	Status     TaskStatus             `json:"status"`
	Error      string                 `json:"error,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	StartedAt  *time.Time             `json:"started_at,omitempty"`
	FinishedAt *time.Time             `json:"finished_at,omitempty"`
	Result     *IPQualityResult       `json:"result,omitempty"`
	Archives   []IPQualityArchiveInfo `json:"archives,omitempty"`
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
	if len(input.ReportsText) != 0 {
		if len(input.ReportsText) != len(result.Reports) {
			return IPQualityResult{}, errors.New("IPQuality printed report does not match address families")
		}
		total := 0
		for _, text := range input.ReportsText {
			total += len(text)
			if text == "" || total > MaxIPQualityResultBytes || !utf8.ValidString(text) {
				return IPQualityResult{}, errors.New("IPQuality printed report is too large or is not UTF-8")
			}
		}
		result.ReportsText = input.ReportsText
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > MaxIPQualityResultBytes {
		return IPQualityResult{}, errors.New("encoded IPQuality result exceeds the size limit")
	}
	return result, nil
}
