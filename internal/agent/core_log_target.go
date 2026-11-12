package agent

import (
	"context"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type preparedCoreLogTargetKey struct{}
type preparedCoreLogTarget func(core.Engine, EngineSpec, string) error

// Only the executor's compiled candidate (or a matching completed migration
// record) can advance the expected digest. Never derive it from the final file.
func bindPreparedCoreLogTarget(ctx context.Context, engine core.Engine, spec EngineSpec, digest string) error {
	if bind, ok := ctx.Value(preparedCoreLogTargetKey{}).(preparedCoreLogTarget); ok {
		return bind(engine, spec, digest)
	}
	return nil
}
