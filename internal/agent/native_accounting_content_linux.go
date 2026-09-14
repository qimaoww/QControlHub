package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func (e *Executor) prepareNativeAccountingContent(ctx context.Context, engine core.Engine, spec EngineSpec, content string) (string, string) {
	initial, compilationErr := serverconfig.PrepareIndependentEgress(engine, content)
	if compilationErr == nil && initial.Source == "disabled" {
		return initial.Content, ""
	}
	if compilationErr != nil {
		if !strings.Contains(content, "qch-trf-") {
			return content, "independent egress unavailable: " + compilationErr.Error()
		}
		source, err := accountingUpdateInput(engine, spec.ConfigPath, content)
		if err != nil {
			return content, "accounting update rejected: " + err.Error()
		}
		content = source
		if _, err := serverconfig.PrepareIndependentEgress(engine, content); err != nil {
			return content, "independent egress unavailable: " + err.Error()
		}
	}
	if engine == core.EngineSingBox {
		if err := validatePrivilegedExecutable(spec.Binary); err != nil {
			return content, "native accounting unavailable: " + err.Error()
		}
		version, err := run(ctx, spec.Binary, "version")
		if err != nil || !strings.Contains(version, "with_v2ray_api") {
			plan, planErr := serverconfig.PrepareMarkedSingBoxAccounting(content)
			if planErr != nil {
				return content, "dual accounting unavailable: " + planErr.Error()
			}
			if err := checkOutboundMarkRouting(ctx); err != nil {
				return content, "dual accounting unavailable: " + err.Error()
			}
			return plan.Content, ""
		}
	}
	plan, err := serverconfig.PrepareAccounting(engine, content)
	if err != nil {
		return content, "native accounting unavailable: " + err.Error()
	}
	if plan.Source == "nft-dual" {
		if err := checkOutboundMarkRouting(ctx); err != nil {
			return content, "dual accounting unavailable: " + err.Error()
		}
	}
	return plan.Content, ""
}

// A new socket mark must not silently select an administrator's fwmark-based
// routing table. Complex policy routing is intentionally an explicit review.
func checkOutboundMarkRouting(ctx context.Context) error {
	var binary string
	for _, candidate := range []string{"/usr/sbin/ip", "/usr/bin/ip", "/sbin/ip", "/bin/ip"} {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil && validatePrivilegedExecutable(resolved) == nil {
			binary = resolved
			break
		}
	}
	if binary == "" {
		return errors.New("iproute2 is required to verify outbound mark routing")
	}
	for _, family := range []string{"-4", "-6"} {
		output, err := run(ctx, binary, family, "rule", "show")
		if err != nil {
			return errors.New("cannot verify policy routing before applying outbound marks")
		}
		if strings.Contains(output, "fwmark") {
			return errors.New("existing fwmark policy routing requires manual review before enabling outbound accounting")
		}
	}
	return nil
}
