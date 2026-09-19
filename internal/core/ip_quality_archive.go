package core

import (
	"encoding/json"
	"net/netip"
	"regexp"
	"time"
)

const MaxIPQualityArchiveBytes = 2 << 20

// The pinned upstream returns a direct SVG link, never an arbitrary URL.
var ipQualityReportURL = regexp.MustCompile(`^https://(?i:report\.check\.place)/(?i:ip)/[A-Za-z0-9_-]{1,128}\.svg$`)

func ValidIPQualityReportURL(value string) bool { return ipQualityReportURL.MatchString(value) }

func IPQualityReportFamily(report json.RawMessage) int {
	var value struct{ Head struct{ IP string } }
	_ = json.Unmarshal(report, &value)
	address, err := netip.ParseAddr(value.Head.IP)
	if err == nil && address.Unmap().Is4() {
		return 4
	}
	return 6
}

type IPQualityArchiveInfo struct {
	Family       int       `json:"family"`
	SourceURL    string    `json:"source_url"`
	SHA256       string    `json:"sha256"`
	Size         int       `json:"size"`
	DownloadedAt time.Time `json:"downloaded_at"`
}

// Content stays out of JSON/WSS/history. Only the panel downloads these bytes.
type IPQualityArchive struct {
	IPQualityArchiveInfo
	Content []byte `json:"-"`
}
