//go:build linux

package hostmetrics

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"sort"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

const ipv6DefaultDestination = "00000000000000000000000000000000"

func networkInterfaceDetails(names []string) ([]core.HostNetworkInterface, error) {
	details := make([]core.HostNetworkInterface, 0, len(names))
	for _, name := range names {
		if !safeNetworkInterfaceName(name) {
			return nil, errors.New("unsafe network interface name")
		}
		device, err := net.InterfaceByName(name)
		if err != nil {
			return nil, fmt.Errorf("read %s addresses: %w", name, err)
		}
		assigned, err := device.Addrs()
		if err != nil {
			return nil, fmt.Errorf("read %s addresses: %w", name, err)
		}
		addresses := make([]string, 0, len(assigned))
		seen := make(map[netip.Addr]struct{}, len(assigned))
		for _, address := range assigned {
			value := address.String()
			var addr netip.Addr
			if prefix, parseErr := netip.ParsePrefix(value); parseErr == nil {
				addr = prefix.Addr().Unmap()
			} else {
				parsed, parseErr := netip.ParseAddr(value)
				if parseErr != nil {
					continue
				}
				addr = parsed.Unmap()
			}
			if !usableNetworkAddr(addr) {
				continue
			}
			if _, duplicate := seen[addr]; duplicate {
				continue
			}
			seen[addr] = struct{}{}
			addresses = append(addresses, addr.String())
		}
		sort.SliceStable(addresses, func(i, j int) bool {
			leftAddr, _ := netip.ParseAddr(addresses[i])
			rightAddr, _ := netip.ParseAddr(addresses[j])
			left, right := networkAddrPriority(leftAddr), networkAddrPriority(rightAddr)
			if left != right {
				return left < right
			}
			return addresses[i] < addresses[j]
		})
		if len(addresses) > 0 {
			details = append(details, core.HostNetworkInterface{Name: name, Addresses: addresses})
		}
	}
	return details, nil
}

func usableNetworkAddress(ip net.IP) bool {
	if ip == nil {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	return usableNetworkAddr(address.Unmap())
}

func usableNetworkAddr(address netip.Addr) bool {
	return address.IsValid() && address.IsGlobalUnicast() && !address.IsUnspecified() && !address.IsLoopback() && !address.IsLinkLocalUnicast() && !address.IsLinkLocalMulticast() && !netpolicy.IsCloudflareAddress(address)
}

func networkAddressPriority(ip net.IP) int {
	if ip == nil {
		return 4
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return 4
	}
	return networkAddrPriority(address.Unmap())
}

func networkAddrPriority(address netip.Addr) int {
	if !address.IsValid() {
		return 4
	}
	if address.Is4() {
		if address.IsPrivate() {
			return 1
		}
		return 0
	}
	if address.IsPrivate() {
		return 3
	}
	return 2
}

func routedNetworkInterfaces() ([]string, error) {
	return routedNetworkInterfacesWithScratch(nil)
}

func routedNetworkInterfacesWithScratch(scratch *[]byte) ([]string, error) {
	result := make(map[string]struct{})
	if contents, err := readMetricFileReuse(scratch, "/proc/net/route", 1<<20); err == nil {
		parseIPv4DefaultRoutesData(contents, result)
	}
	if contents, err := readMetricFileReuse(scratch, "/proc/net/ipv6_route", 1<<20); err == nil {
		parseIPv6DefaultRoutesData(contents, result)
	}
	if len(result) == 0 {
		contents, err := readMetricFileReuse(scratch, "/proc/net/dev", 1<<20)
		if err == nil {
			parseNetworkDeviceNamesData(contents, result)
		}
	}
	if len(result) == 0 {
		for _, entry := range fallbackNetworkInterfaces() {
			result[entry] = struct{}{}
		}
	}
	interfaces := make([]string, 0, len(result))
	for name := range result {
		if safeNetworkInterfaceName(name) {
			interfaces = append(interfaces, name)
		}
	}
	sort.Strings(interfaces)
	if len(interfaces) == 0 || len(interfaces) > 16 {
		return nil, errors.New("no safe default-route network interface found")
	}
	return interfaces, nil
}

func fallbackNetworkInterfaces() []string {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		// sysfs class entries are links to devices, not directory entries.
		if (entry.IsDir() || entry.Type()&os.ModeSymlink != 0) && safeNetworkInterfaceName(entry.Name()) {
			result = append(result, entry.Name())
		}
	}
	sort.Strings(result)
	return result
}

func parseIPv4DefaultRoutes(contents string, result map[string]struct{}) {
	parseIPv4DefaultRoutesData(contents, result)
}

func parseIPv4DefaultRoutesData[T ~[]byte | ~string](contents T, result map[string]struct{}) {
	pos := 0
	for {
		line, nextPos, ok := nextLineSlice(contents, pos)
		if !ok {
			return
		}
		pos = nextPos
		iface, fieldPos, ok := nextASCIIField(line, 0)
		if !ok {
			continue
		}
		dest, fieldPos, ok := nextASCIIField(line, fieldPos)
		if !ok || !equalBytesString(dest, "00000000") {
			continue
		}
		_, fieldPos, ok = nextASCIIField(line, fieldPos)
		if !ok {
			continue
		}
		flagsField, _, ok := nextASCIIField(line, fieldPos)
		if !ok {
			continue
		}
		flags, err := parseHexUintASCII(flagsField)
		if err == nil && flags&1 != 0 && !equalBytesString(iface, "lo") {
			result[string(iface)] = struct{}{}
		}
	}
}

func parseHexUintASCII[T ~[]byte | ~string](data T) (uint64, error) {
	if len(data) == 0 {
		return 0, errors.New("empty hex integer")
	}
	var value uint64
	for i := 0; i < len(data); i++ {
		c := data[i]
		var digit uint64
		switch {
		case c >= '0' && c <= '9':
			digit = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			digit = uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			digit = uint64(c-'A') + 10
		default:
			return 0, errors.New("invalid hex integer")
		}
		if value > (math.MaxUint64-digit)/16 {
			return 0, errors.New("hex integer overflow")
		}
		value = value*16 + digit
	}
	return value, nil
}

func parseIPv6DefaultRoutes(contents string, result map[string]struct{}) {
	parseIPv6DefaultRoutesData(contents, result)
}

func parseIPv6DefaultRoutesData[T ~[]byte | ~string](contents T, result map[string]struct{}) {
	pos := 0
	for {
		line, nextPos, ok := nextLineSlice(contents, pos)
		if !ok {
			return
		}
		pos = nextPos
		field0, fieldPos, ok := nextASCIIField(line, 0)
		if !ok || !equalBytesString(field0, ipv6DefaultDestination) {
			continue
		}
		field1, fieldPos, ok := nextASCIIField(line, fieldPos)
		if !ok || !equalBytesString(field1, "00") {
			continue
		}
		fieldCount := 2
		var lastField T
		for {
			field, next, ok := nextASCIIField(line, fieldPos)
			if !ok {
				break
			}
			fieldPos = next
			lastField = field
			fieldCount++
		}
		if fieldCount >= 10 && !equalBytesString(lastField, "lo") {
			result[string(lastField)] = struct{}{}
		}
	}
}

func parseNetworkDeviceNames(contents string, result map[string]struct{}) {
	parseNetworkDeviceNamesData(contents, result)
}

func parseNetworkDeviceNamesData[T ~[]byte | ~string](contents T, result map[string]struct{}) {
	pos := 0
	for {
		line, nextPos, ok := nextLineSlice(contents, pos)
		if !ok {
			return
		}
		pos = nextPos
		colon := -1
		for i := 0; i < len(line); i++ {
			if line[i] == ':' {
				colon = i
				break
			}
		}
		if colon < 0 {
			continue
		}
		name := trimASCIIWhitespace(line[:colon])
		if len(name) > 0 && !equalBytesString(name, "lo") {
			result[string(name)] = struct{}{}
		}
	}
}

func trimASCIIWhitespace[T ~[]byte | ~string](data T) T {
	start := 0
	for start < len(data) && isASCIIWhitespace(data[start]) {
		start++
	}
	end := len(data)
	for end > start && isASCIIWhitespace(data[end-1]) {
		end--
	}
	return data[start:end]
}

func safeNetworkInterfaceName(name string) bool {
	if name == "" || name == "lo" || len(name) > 64 || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("_.:-", character) {
			continue
		}
		return false
	}
	return true
}

func networkCounters(interfaces []string) (uint64, uint64, error) {
	return networkCountersWithScratch(interfaces, nil)
}

func networkCountersWithScratch(interfaces []string, scratch *[]byte) (uint64, uint64, error) {
	for _, name := range interfaces {
		if !safeNetworkInterfaceName(name) {
			return 0, 0, errors.New("unsafe network interface name")
		}
	}
	if len(interfaces) > 0 && len(interfaces) <= 16 {
		if rx, tx, ok := procNetDevCounters(interfaces, scratch); ok {
			return rx, tx, nil
		}
	}
	return sysfsNetworkCounters(interfaces, scratch)
}

func procNetDevCounters(interfaces []string, scratch *[]byte) (uint64, uint64, bool) {
	contents, err := readMetricFileReuse(scratch, "/proc/net/dev", 1<<20)
	if err != nil {
		return 0, 0, false
	}
	return parseProcNetDevCounters(contents, interfaces)
}

func parseProcNetDevCounters[T ~[]byte | ~string](contents T, interfaces []string) (uint64, uint64, bool) {
	if len(interfaces) == 0 || len(interfaces) > 16 {
		return 0, 0, false
	}
	var matchedMask uint16
	expectedMask := uint16((1 << len(interfaces)) - 1)
	var rxTotal, txTotal uint64

	pos := 0
	for {
		line, nextPos, ok := nextLineSlice(contents, pos)
		if !ok {
			break
		}
		pos = nextPos
		colon := -1
		for i := 0; i < len(line); i++ {
			if line[i] == ':' {
				colon = i
				break
			}
		}
		if colon < 0 {
			continue
		}
		name := trimASCIIWhitespace(line[:colon])
		idx := -1
		for i, candidate := range interfaces {
			if equalBytesString(name, candidate) {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		bit := uint16(1 << idx)
		if matchedMask&bit != 0 {
			return 0, 0, false
		}
		rest := line[colon+1:]
		rxField, fieldPos, hasRX := nextASCIIField(rest, 0)
		if !hasRX {
			return 0, 0, false
		}
		rx, err := parseUintASCII(rxField)
		if err != nil {
			return 0, 0, false
		}
		for skip := 0; skip < 7; skip++ {
			_, nextFieldPos, ok := nextASCIIField(rest, fieldPos)
			if !ok {
				return 0, 0, false
			}
			fieldPos = nextFieldPos
		}
		txField, _, hasTX := nextASCIIField(rest, fieldPos)
		if !hasTX {
			return 0, 0, false
		}
		tx, err := parseUintASCII(txField)
		if err != nil {
			return 0, 0, false
		}
		if math.MaxUint64-rxTotal < rx || math.MaxUint64-txTotal < tx {
			return 0, 0, false
		}
		rxTotal += rx
		txTotal += tx
		matchedMask |= bit
	}
	if matchedMask == expectedMask {
		return rxTotal, txTotal, true
	}
	return 0, 0, false
}

func sysfsNetworkCounters(interfaces []string, scratch *[]byte) (uint64, uint64, error) {
	var rxTotal, txTotal uint64
	for _, name := range interfaces {
		if !safeNetworkInterfaceName(name) {
			return 0, 0, errors.New("unsafe network interface name")
		}
		rx, err := readUintMetricReuse("/sys/class/net/"+name+"/statistics/rx_bytes", scratch)
		if err != nil {
			return 0, 0, fmt.Errorf("read %s receive counter: %w", name, err)
		}
		tx, err := readUintMetricReuse("/sys/class/net/"+name+"/statistics/tx_bytes", scratch)
		if err != nil {
			return 0, 0, fmt.Errorf("read %s transmit counter: %w", name, err)
		}
		if math.MaxUint64-rxTotal < rx || math.MaxUint64-txTotal < tx {
			return 0, 0, errors.New("network counter overflow")
		}
		rxTotal += rx
		txTotal += tx
	}
	return rxTotal, txTotal, nil
}

func readUintMetric(path string) (uint64, error) {
	return readUintMetricReuse(path, nil)
}

func readUintMetricReuse(path string, scratch *[]byte) (uint64, error) {
	contents, err := readMetricFileReuse(scratch, path, 128)
	if err != nil {
		return 0, err
	}
	return parseUintASCII(trimASCIIWhitespace(contents))
}

func bytesPerSecond(delta uint64, seconds float64) uint64 {
	if delta == 0 || seconds <= 0 {
		return 0
	}
	value := float64(delta) / seconds
	if value >= float64(math.MaxUint64) {
		return math.MaxUint64
	}
	return uint64(value)
}
