package serverconfig

import "github.com/qimaoww/qcontrolhub/internal/core"

// Official builds may omit with_v2ray_api. Linux routing_mark supplies the
// same dedicated-outbound attribution without requiring a custom binary.
func PrepareMarkedSingBoxAccounting(content string) (AccountingPlan, error) {
	// Share validation and clone generation with bound exits. In particular,
	// validate generated marks instead of stripping them before verification.
	return prepareAccounting(core.EngineSingBox, content, true)
}
