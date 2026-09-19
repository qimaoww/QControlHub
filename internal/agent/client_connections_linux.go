//go:build linux

package agent

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// No connection history or client addresses are written to disk on the node.
func (e *Executor) collectClientConnections(ctx context.Context) *core.ClientConnectionReport {
	report := &core.ClientConnectionReport{Status: "ok", Connections: []core.ClientConnection{}}
	partial := func(detail string) {
		report.Status = "partial"
		if report.Detail != "" {
			report.Detail += "; "
		}
		report.Detail += detail
	}
	e.specsMu.RLock()
	specs := make(map[core.Engine]EngineSpec, len(e.Specs))
	for engine, spec := range e.Specs {
		specs[engine] = spec
	}
	e.specsMu.RUnlock()
	var listeners []serverconfig.ConnectionListener
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		spec, ok := specs[engine]
		if !ok {
			continue
		}
		content, err := readConfigurationFile(spec.ConfigPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			partial(string(engine) + ": configuration unavailable")
			continue
		}
		found, err := serverconfig.DiscoverConnectionListeners(engine, content)
		if err != nil {
			partial(string(engine) + ": listener discovery failed")
			continue
		}
		listeners = append(listeners, found...)
	}
	if len(listeners) == 0 {
		report.Detail = "no managed inbound listeners"
		return report
	}
	ambiguous := false
	seen := map[core.ClientConnection]bool{}
	appendFlow := func(flow core.ClientConnection) {
		var match *serverconfig.ConnectionListener
		for i := range listeners {
			candidate := &listeners[i]
			if candidate.Port != flow.LocalPort || (string(candidate.Transport) != flow.Transport && candidate.Transport != core.TrafficProtocolBoth) {
				continue
			}
			if match != nil {
				ambiguous = true
				return
			} // Shared ports cannot be attributed safely.
			match = candidate
		}
		if match == nil {
			return
		}
		flow.Engine, flow.Protocol, flow.Inbound = match.Engine, match.Protocol, match.Inbound
		if seen[flow] {
			return
		}
		if len(report.Connections) >= core.MaxClientConnections {
			report.Truncated = true
			return
		}
		seen[flow] = true
		report.Connections = append(report.Connections, flow)
	}
	// TCP sockets are matched against actual listening sockets, excluding outbound
	// ephemeral ports that merely coincide with a configured inbound port.
	for _, name := range []string{"tcp", "tcp6"} {
		file, err := os.Open("/proc/net/" + name)
		if errors.Is(err, os.ErrNotExist) && name == "tcp6" {
			continue
		}
		if err != nil {
			partial(name + ": socket table unavailable")
			continue
		}
		flows, err := readClientTCPSockets(file)
		file.Close()
		if err != nil {
			partial(name + ": socket table incomplete")
		}
		for _, flow := range flows {
			appendFlow(flow)
		}
	}
	hasUDP := false
	for _, l := range listeners {
		if l.Transport != core.TrafficProtocolTCP {
			hasUDP = true
		}
	}
	if hasUDP {
		// UDP peers do not appear in unconnected /proc/net/udp server sockets.
		// Conntrack supplies original-direction tuples for QUIC and UDP listeners.
		local := map[netip.Addr]bool{}
		addresses, err := net.InterfaceAddrs()
		if err != nil {
			partial("UDP: local interfaces unavailable")
		}
		if err == nil {
			for _, address := range addresses {
				if prefix, err := netip.ParsePrefix(address.String()); err == nil {
					local[prefix.Addr().Unmap()] = true
				}
			}
		}
		udpSockets := map[netip.AddrPort]bool{}
		for _, name := range []string{"udp", "udp6"} {
			file, err := os.Open("/proc/net/" + name)
			if errors.Is(err, os.ErrNotExist) && name == "udp6" {
				continue
			}
			if err != nil {
				partial(name + ": socket table unavailable")
				continue
			}
			sockets, err := readClientUDPSockets(file)
			file.Close()
			if err != nil {
				partial(name + ": socket table incomplete")
			}
			for socket := range sockets {
				udpSockets[socket] = true
			}
		}
		commandCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		cmd := exec.CommandContext(commandCtx, "conntrack", "-L", "-p", "udp", "-o", "extended")
		pipe, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err == nil {
			limited := &io.LimitedReader{R: pipe, N: 16 << 20}
			scanner := bufio.NewScanner(limited)
			for scanner.Scan() {
				if flow, ok := parseClientUDPFlow(scanner.Text(), local); ok && clientSocketListening(udpSockets, flow.LocalIP, flow.LocalPort) {
					appendFlow(flow)
				}
			}
			scanErr := scanner.Err()
			if limited.N == 0 {
				scanErr = fmt.Errorf("conntrack byte limit reached")
			}
			pipe.Close()
			err = cmd.Wait()
			if scanErr != nil {
				err = scanErr
			}
		}
		cancel()
		if err != nil {
			partial("UDP: conntrack unavailable or incomplete")
		}
	}
	if ambiguous {
		partial("ambiguous shared listening ports omitted")
	}
	if report.Truncated {
		partial("connection sample limit reached")
	}
	if err := report.Validate(); err != nil {
		return &core.ClientConnectionReport{Status: "unavailable", Detail: "invalid connection sample", Connections: []core.ClientConnection{}}
	}
	return report
}

func readClientTCPSockets(reader io.Reader) ([]core.ClientConnection, error) {
	limited := &io.LimitedReader{R: reader, N: 16 << 20}
	scanner := bufio.NewScanner(limited)
	listening := map[netip.AddrPort]bool{}
	var flows []core.ClientConnection
	lines := 0
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[0] == "sl" {
			continue
		}
		lines++
		if lines > 65536 {
			return nil, fmt.Errorf("socket table too large")
		}
		local, err := parseProcSocket(fields[1])
		if err != nil {
			return nil, err
		}
		if fields[3] == "0A" {
			listening[local] = true
			continue
		}
		if fields[3] != "01" {
			continue
		}
		peer, err := parseProcSocket(fields[2])
		if err != nil {
			return nil, err
		}
		flows = append(flows, core.ClientConnection{Transport: "tcp", LocalIP: local.Addr().String(), LocalPort: int(local.Port()), ClientIP: peer.Addr().String(), ClientPort: int(peer.Port())})
	}
	result := make([]core.ClientConnection, 0)
	for _, flow := range flows {
		if clientSocketListening(listening, flow.LocalIP, flow.LocalPort) {
			result = append(result, flow)
		}
	}
	if limited.N == 0 {
		return result, fmt.Errorf("socket table byte limit reached")
	}
	return result, scanner.Err()
}

func parseProcSocket(value string) (netip.AddrPort, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return netip.AddrPort{}, fmt.Errorf("invalid socket")
	}
	raw, err := hex.DecodeString(parts[0])
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, fmt.Errorf("invalid socket address")
	}
	// /proc encodes each native-endian 32-bit word. Linux targets supported by
	// this project are little-endian; use the native byte order for portability.
	for i := 0; i < len(raw); i += 4 {
		binary.BigEndian.PutUint32(raw[i:i+4], binary.NativeEndian.Uint32(raw[i:i+4]))
	}
	address, _ := netip.AddrFromSlice(raw)
	port, err := strconv.ParseUint(parts[1], 16, 16)
	return netip.AddrPortFrom(address.Unmap(), uint16(port)), err
}

func parseClientUDPFlow(line string, local map[netip.Addr]bool) (core.ClientConnection, bool) {
	values := map[string]string{}
	for _, field := range strings.Fields(line) {
		key, value, ok := strings.Cut(field, "=")
		if ok {
			if _, exists := values[key]; !exists {
				values[key] = value
			}
		}
	}
	src, se := netip.ParseAddr(values["src"])
	dst, de := netip.ParseAddr(values["dst"])
	sport, sp := strconv.Atoi(values["sport"])
	dport, dp := strconv.Atoi(values["dport"])
	if se != nil || de != nil || sp != nil || dp != nil || sport < 1 || sport > 65535 || dport < 1 || dport > 65535 || !local[dst.Unmap()] || src.IsUnspecified() || src.IsMulticast() {
		return core.ClientConnection{}, false
	}
	return core.ClientConnection{Transport: "udp", ClientIP: src.Unmap().String(), LocalIP: dst.Unmap().String(), ClientPort: sport, LocalPort: dport}, true
}

func clientSocketListening(sockets map[netip.AddrPort]bool, ip string, port int) bool {
	address, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	wildcard := netip.IPv6Unspecified()
	if address.Is4() {
		wildcard = netip.IPv4Unspecified()
	}
	return sockets[netip.AddrPortFrom(address, uint16(port))] || sockets[netip.AddrPortFrom(wildcard, uint16(port))] || sockets[netip.AddrPortFrom(netip.IPv6Unspecified(), uint16(port))]
}

func readClientUDPSockets(reader io.Reader) (map[netip.AddrPort]bool, error) {
	limited := &io.LimitedReader{R: reader, N: 16 << 20}
	scanner := bufio.NewScanner(limited)
	sockets := map[netip.AddrPort]bool{}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[0] == "sl" {
			continue
		}
		address, err := parseProcSocket(fields[1])
		if err != nil {
			return sockets, err
		}
		sockets[address] = true
	}
	if limited.N == 0 {
		return sockets, fmt.Errorf("UDP socket table byte limit reached")
	}
	return sockets, scanner.Err()
}
