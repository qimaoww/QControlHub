//go:build linux

package agent

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/netip"
	"os/exec"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func clientUDPConnectionFamilies(sockets map[netip.AddrPort]bool, listeners []serverconfig.ConnectionListener, local map[netip.Addr]bool) []string {
	families := map[string]bool{}
	for address := range local {
		for _, listener := range listeners {
			if listener.Transport == core.TrafficProtocolTCP || !clientSocketListening(sockets, address.String(), listener.Port) {
				continue
			}
			family := "ipv6"
			if address.Is4() {
				family = "ipv4"
			}
			families[family] = true
		}
	}
	var result []string
	for _, family := range []string{"ipv4", "ipv6"} {
		if families[family] {
			result = append(result, family)
		}
	}
	return result
}

// Discard excess stderr without blocking the child or retaining arbitrary output.
type clientConntrackError struct{ text string }

func (b *clientConntrackError) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 4096 - len(b.text); remaining > 0 {
		b.text += string(p[:min(remaining, n)])
	}
	return n, nil
}

func collectClientUDPFamily(ctx context.Context, command, family string, local map[netip.Addr]bool, sockets map[netip.AddrPort]bool, appendFlow func(core.ClientConnection)) string {
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, command, "-L", "-f", family, "-p", "udp", "-o", "extended")
	cmd.WaitDelay = 200 * time.Millisecond
	stderr := &clientConntrackError{}
	cmd.Stderr = stderr
	pipe, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		if pipe != nil {
			pipe.Close()
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "conntrack not installed"
		}
		return "conntrack could not start"
	}
	limited := &io.LimitedReader{R: pipe, N: 16 << 20}
	scanner := bufio.NewScanner(limited)
	for scanner.Scan() {
		if flow, ok := parseClientUDPFlow(scanner.Text(), local); ok && clientSocketListening(sockets, flow.LocalIP, flow.LocalPort) {
			appendFlow(flow)
		}
	}
	pipe.Close()
	err = cmd.Wait()
	if commandCtx.Err() != nil {
		return "conntrack timed out or canceled"
	}
	if scanner.Err() != nil || limited.N == 0 {
		return "conntrack output incomplete"
	}
	if err != nil {
		message := strings.ToLower(stderr.text)
		if strings.Contains(message, "operation not permitted") || strings.Contains(message, "permission denied") || strings.Contains(message, "cap_net_admin") {
			return "conntrack permission denied (CAP_NET_ADMIN required)"
		}
		return "conntrack unavailable (check kernel connection tracking)"
	}
	return ""
}
