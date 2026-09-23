package core

import (
	"encoding/json"
	"net/netip"
	"time"
)

const MaxIPQualityArchiveBytes = 2 << 20

func IPQualityReportFamily(report json.RawMessage) int {
	var value struct{ Head struct{ IP string } }
	_ = json.Unmarshal(report, &value)
	address, err := netip.ParseAddr(value.Head.IP)
	if err == nil && address.Unmap().Is4() {
		return 4
	}
	return 6
}

// The panel renders this image from the stored JSON; it is never fetched from
// an upstream report host.
type IPQualityArchiveInfo struct {
	Family     int       `json:"family"`
	SHA256     string    `json:"sha256"`
	Size       int       `json:"size"`
	RenderedAt time.Time `json:"rendered_at"`
}

// Content stays out of JSON/WSS/history. Only the panel serves these bytes.
type IPQualityArchive struct {
	IPQualityArchiveInfo
	Content []byte `json:"-"`
}
