package store

import (
	"encoding/json"
	"net/netip"
	"regexp"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Match source positions in known access messages, never arbitrary IPs in a
// line: destination addresses and outbound connection messages are not clients.
var (
	xrayClientLog   = regexp.MustCompile(`(?:^|\s)(?:from\s+)?((?:tcp:|udp:)?\S+)\s+accepted\s+(?:tcp|udp):\S+(?:\s+\[([^\]]+)\])?`)
	singClientLog   = regexp.MustCompile(`(?:^|\s)inbound/([a-z0-9_-]+)\[([^\]]*)\]: inbound (packet )?connection from (\S+)`)
	mihomoClientLog = regexp.MustCompile(`\[(TCP|UDP)\]\s+(\[[0-9a-fA-F:.]+\]:[0-9]+|[0-9.]+:[0-9]+)(?:\([^\r\n]*\))?\s+-->\s+\S+\s+.*using\s+`)
	ssTCPClientLog  = regexp.MustCompile(`(?:^|\s)established tcp tunnel (\S+) <-> `)
	ssUDPClientLog  = regexp.MustCompile(`(?:^|\s)created udp association for (\S+)(?:\s|$)`)
)

func clientConnectionFromLog(entry core.CoreLogEntry) (core.ClientConnection, bool) {
	c := core.ClientConnection{Engine: entry.Engine, LocalIP: "0.0.0.0"}
	message := sanitizeCoreLogMessage(entry.Message)
	// Mihomo can use JSON output; only parse its message field, not metadata.
	if strings.HasPrefix(message, "{") {
		var object struct {
			Message string `json:"message"`
			Msg     string `json:"msg"`
		}
		if json.Unmarshal([]byte(message), &object) != nil {
			return c, false
		}
		message = object.Message
		if message == "" {
			message = object.Msg
		}
		message = sanitizeCoreLogMessage(message)
	}
	var source string
	switch entry.Engine {
	case core.EngineXray:
		match := xrayClientLog.FindStringSubmatch(message)
		if match == nil {
			return c, false
		}
		source = match[1]
		// Xray's destination network may differ from the incoming network (e.g.
		// UDP tunneled through VLESS TCP). Only an explicit source prefix is known.
		for _, network := range []string{"tcp", "udp"} {
			if strings.HasPrefix(source, network+":") {
				c.Transport = network
				source = strings.TrimPrefix(source, network+":")
				break
			}
		}
		c.Inbound = match[2]
		for _, separator := range []string{" -> ", " >> "} {
			if inbound, _, ok := strings.Cut(c.Inbound, separator); ok {
				c.Inbound = inbound
				break
			}
		}
		c.Inbound = strings.TrimSpace(c.Inbound)
	case core.EngineSingBox:
		match := singClientLog.FindStringSubmatch(message)
		if match == nil {
			return c, false
		}
		c.Protocol, c.Inbound, source = match[1], match[2], match[4]
		// The line explicitly identifies a connection or packet connection.
		c.Transport = "tcp"
		if match[3] != "" {
			c.Transport = "udp"
		}
	case core.EngineMihomo:
		match := mihomoClientLog.FindStringSubmatch(message)
		if match == nil {
			return c, false
		}
		c.Transport, source = strings.ToLower(match[1]), match[2]
	case core.EngineShadowsocksRust:
		c.Protocol = "shadowsocks"
		if match := ssTCPClientLog.FindStringSubmatch(message); match != nil {
			source, c.Transport = match[1], "tcp"
		} else if match := ssUDPClientLog.FindStringSubmatch(message); match != nil {
			source, c.Transport = match[1], "udp"
		} else {
			return c, false
		}
	default:
		return c, false
	}
	endpoint, err := netip.ParseAddrPort(source)
	if err != nil || endpoint.Port() == 0 || endpoint.Addr().Zone() != "" || endpoint.Addr().Unmap().IsUnspecified() || endpoint.Addr().Unmap().IsMulticast() || len(c.Protocol) > 40 || len(c.Inbound) > 400 {
		return c, false
	}
	c.ClientIP, c.ClientPort = endpoint.Addr().Unmap().String(), int(endpoint.Port())
	return c, true
}
