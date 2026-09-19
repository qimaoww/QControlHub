//go:build !linux

package agent

import (
	"context"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (e *Executor) collectClientConnections(context.Context) *core.ClientConnectionReport {
	return &core.ClientConnectionReport{Status: "unavailable", Detail: "client connection collection requires Linux", Connections: []core.ClientConnection{}}
}
