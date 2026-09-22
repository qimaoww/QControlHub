package store

import (
	"encoding/json"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Match source positions in known access messages, never arbitrary IPs in a
// line: destination addresses and outbound connection messages are not clients.
var (
	xrayClientLog   = regexp.MustCompile(`(?:^|\s)(?:from\s+)?((?:tcp:|udp:)?\S+)\s+accepted\s+(?:tcp|udp):\S+(?:\s+\[([^\]]+)\])?`)
	singClientLog   = regexp.MustCompile(`^(?:inbound|endpoint)/([a-z0-9_-]+)\[([^\]]*)\]: (?:\[[^\]\r\n]*\] )?inbound (packet )?connection from (\S+)$`)
	mihomoClientLog = regexp.MustCompile(`^\[(TCP|UDP)\]\s+(\[[0-9a-fA-F:.]+\]:[0-9]+|[0-9.]+:[0-9]+)(?:\([^\r\n]*\))?\s+-->\s+\S+\s+.*using\s+`)
	// Anchor the rule clause between the destination and "using" so rule or
	// proxy text that merely contains "match InName(...)" cannot name an inbound.
	mihomoInNameLog = regexp.MustCompile(`^\[(TCP|UDP)\]\s+(?:\[[0-9a-fA-F:.]+\]:[0-9]+|[0-9.]+:[0-9]+)(?:\([^\r\n]*\))?\s+-->\s+\S+\s+match InName\(([^)\r\n]*)\)\s+using\s+`)
	ssTCPClientLog  = regexp.MustCompile(`(?:^|\s)established tcp tunnel (\S+) <-> `)
	ssUDPClientLog  = regexp.MustCompile(`(?:^|\s)created udp association for (\S+)(?:\s|$)`)
)

var (
	accessLogPrefix  = regexp.MustCompile(`^(?:(?:[+-]\d{4}\s+)?\d{4}[-/]\d{2}[-/]\d{2}[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\s+)?(?:TRACE|DEBUG|INFO)(?:\[\d+(?:\.\d+)?\])?\s+`)
	accessLogContext = regexp.MustCompile(`^\[\d+ [0-9.hmsµu]+\]\s+`)
	accessLogfmt     = regexp.MustCompile(`^(?:time=(?:"[^"\r\n]*"|\S+)\s+)?level=(?:info|debug|trace)\s+msg=("(?:[^"\\]|\\.)*")\s*$`)
)

// Strip only known logger envelopes. Searching arbitrary substrings can turn
// a quoted error, rule name, or destination text into a false client event.
func clientAccessMessage(message string) string {
	if strings.ContainsAny(message, "\r\n") {
		return ""
	}
	if match := accessLogfmt.FindStringSubmatch(message); match != nil {
		value, err := strconv.Unquote(match[1])
		if err != nil {
			return ""
		}
		// logrus formats Infoln messages with fmt.Sprintln and quotes the
		// trailing newline inside the msg field. Drop only trailing line breaks
		// so one access line is accepted while an embedded break still fails.
		value = strings.TrimRight(value, "\r\n")
		if strings.ContainsAny(value, "\r\n") {
			return ""
		}
		message = value
	}
	message = accessLogPrefix.ReplaceAllString(message, "")
	return accessLogContext.ReplaceAllString(message, "")
}

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
		match := singClientLog.FindStringSubmatch(clientAccessMessage(message))
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
		access := clientAccessMessage(message)
		match := mihomoClientLog.FindStringSubmatch(access)
		if match == nil {
			return c, false
		}
		c.Transport, source = strings.ToLower(match[1]), match[2]
		// A matched IN-NAME rule names the inbound (the panel's accounting and
		// independent-egress rules always do), which resolves its port later.
		if inbound := mihomoInNameLog.FindStringSubmatch(access); inbound != nil {
			c.Inbound = strings.TrimSpace(inbound[2])
		}
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
