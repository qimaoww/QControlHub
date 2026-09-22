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
	xrayClientLog = regexp.MustCompile(`(?:^|\s)(?:from\s+)?((?:tcp:|udp:)?\S+)\s+accepted\s+(?:tcp|udp):\S+(?:\s+\[([^\]]+)\])?`)
	singClientLog = regexp.MustCompile(`^(?:inbound|endpoint)/([a-z0-9_-]+)\[([^\]]*)\]: (?:\[[^\]\r\n]*\] )?inbound (packet )?connection from (\S+)$`)
	// singRoutedLog matches the line sing-box emits only after the protocol
	// handshake succeeds and the connection reaches the router. The shared
	// inbound listener logs the source at accept time, before the handshake, so
	// an inbound source is trusted only once the same connection was routed.
	singRoutedLog   = regexp.MustCompile(`^inbound/([a-z0-9_-]+)\[([^\]]*)\]: (?:\[[^\]\r\n]*\] )?inbound (?:packet addr connection|(?:packet )?connection to )`)
	mihomoClientLog = regexp.MustCompile(`^\[(TCP|UDP)\]\s+(\[[0-9a-fA-F:.]+\]:[0-9]+|[0-9.]+:[0-9]+)(?:\([^\r\n]*\))?\s+-->\s+\S+\s+.*using\s+`)
	// Anchor the rule clause between the destination and "using" so rule or
	// proxy text that merely contains "match InName(...)" cannot name an inbound.
	mihomoInNameLog = regexp.MustCompile(`^\[(TCP|UDP)\]\s+(?:\[[0-9a-fA-F:.]+\]:[0-9]+|[0-9.]+:[0-9]+)(?:\([^\r\n]*\))?\s+-->\s+\S+\s+match InName\(([^)\r\n]*)\)\s+using\s+`)
	ssTCPClientLog  = regexp.MustCompile(`(?:^|\s)established tcp tunnel (\S+) <-> `)
	ssUDPClientLog  = regexp.MustCompile(`(?:^|\s)created udp association for (\S+)(?:\s|$)`)
)

var (
	accessLogPrefix  = regexp.MustCompile(`^(?:(?:[+-]\d{4}\s+)?\d{4}[-/]\d{2}[-/]\d{2}[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\s+)?(?:TRACE|DEBUG|INFO)(?:\[\d+(?:\.\d+)?\])?\s+`)
	accessLogContext = regexp.MustCompile(`^\[(\d+) [0-9.hmsµu]+\]\s+`)
	accessLogfmt     = regexp.MustCompile(`^(?:time=(?:"[^"\r\n]*"|\S+)\s+)?level=(?:info|debug|trace)\s+msg=("(?:[^"\\]|\\.)*")\s*$`)
)

// coreLogMessage decodes the JSON envelope Mihomo can emit, keeping only its
// message field, then sanitizes the result for access parsing.
func coreLogMessage(entry core.CoreLogEntry) string {
	message := sanitizeCoreLogMessage(entry.Message)
	if !strings.HasPrefix(message, "{") {
		return message
	}
	var object struct {
		Message string `json:"message"`
		Msg     string `json:"msg"`
	}
	if json.Unmarshal([]byte(message), &object) != nil {
		return ""
	}
	message = object.Message
	if message == "" {
		message = object.Msg
	}
	return sanitizeCoreLogMessage(message)
}

// Strip only known logger envelopes. Searching arbitrary substrings can turn
// a quoted error, rule name, or destination text into a false client event.
// The sing-box connection context ID, when present, is returned alongside the
// access text so a source line can be tied to the connection that logged it.
func clientAccessMessage(message string) (string, string) {
	if strings.ContainsAny(message, "\r\n") {
		return "", ""
	}
	if match := accessLogfmt.FindStringSubmatch(message); match != nil {
		value, err := strconv.Unquote(match[1])
		if err != nil {
			return "", ""
		}
		// logrus formats Infoln messages with fmt.Sprintln and quotes the
		// trailing newline inside the msg field. Drop only trailing line breaks
		// so one access line is accepted while an embedded break still fails.
		value = strings.TrimRight(value, "\r\n")
		if strings.ContainsAny(value, "\r\n") {
			return "", ""
		}
		message = value
	}
	message = accessLogPrefix.ReplaceAllString(message, "")
	var id string
	if match := accessLogContext.FindStringSubmatch(message); match != nil {
		id = match[1]
	}
	return accessLogContext.ReplaceAllString(message, ""), id
}

// singBoxRoutedConnections collects the connection IDs whose handshake
// succeeded within one log batch. The connection context ID sing-box prints in
// brackets is shared by the accept-time source line and the routed line, so it
// lets the parser drop sources whose connection never completed.
func singBoxRoutedConnections(entries []core.CoreLogEntry) map[string]struct{} {
	routed := make(map[string]struct{})
	for _, entry := range entries {
		if entry.Engine != core.EngineSingBox {
			continue
		}
		access, id := clientAccessMessage(coreLogMessage(entry))
		if id == "" || !singRoutedLog.MatchString(access) {
			continue
		}
		routed[id] = struct{}{}
	}
	return routed
}

func clientConnectionFromLog(entry core.CoreLogEntry, routed map[string]struct{}) (core.ClientConnection, bool) {
	c := core.ClientConnection{Engine: entry.Engine, LocalIP: "0.0.0.0"}
	message := coreLogMessage(entry)
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
		access, id := clientAccessMessage(message)
		match := singClientLog.FindStringSubmatch(access)
		if match == nil {
			return c, false
		}
		// The shared inbound listener logs the source at accept time, before the
		// handshake, so an inbound source is kept only when the same connection
		// reached the post-handshake routing line. Endpoints (WireGuard, masque)
		// log the source after their own handshake and carry no context ID.
		if strings.HasPrefix(access, "inbound/") {
			if _, ok := routed[id]; id == "" || !ok {
				return c, false
			}
		}
		c.Protocol, c.Inbound, source = match[1], match[2], match[4]
		// The line explicitly identifies a connection or packet connection.
		c.Transport = "tcp"
		if match[3] != "" {
			c.Transport = "udp"
		}
	case core.EngineMihomo:
		access, _ := clientAccessMessage(message)
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
