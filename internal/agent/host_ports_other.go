//go:build !linux

package agent

import (
	"errors"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func hostPortObservationSupported() bool { return false }
func observeHostPorts() (*core.HostPortObservation, error) {
	return nil, errors.New("host socket observation unavailable")
}
