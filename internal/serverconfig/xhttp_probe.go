package serverconfig

import (
	"context"
	"net"
)

// A local target is accepted only through a validated managed graph. Public
// targets keep the existing public-DNS, pinned-address and TLS checks.
func ProbePlanRealityTarget(ctx context.Context, input Input) (RealityTarget, error) {
	if isXHTTPGroup(input.Protocol) {
		return RealityTarget{}, nil
	}
	host, port := input.RealityServerName, "443"
	if input.RealityTarget != "" {
		host, port, _ = net.SplitHostPort(input.RealityTarget)
	}
	options := realityProbeOptions{resolver: net.DefaultResolver, dialContext: (&net.Dialer{}).DialContext, serverName: input.RealityServerName, port: port}
	var selected RealityTarget
	for _, name := range xhttpRealityNames(input) {
		options.serverName = name
		target, err := probeRealityTarget(ctx, host, options)
		if err != nil {
			return RealityTarget{}, err
		}
		if name == input.RealityServerName {
			selected = target
		}
	}
	return selected, nil
}
