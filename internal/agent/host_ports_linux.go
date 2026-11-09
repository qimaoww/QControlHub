//go:build linux

package agent

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func hostPortObservationSupported() bool { return true }

// Read every transport in this Agent's host network namespace. Any incomplete
// table fails closed. Connected TCP clients are not listening sockets; bound
// UDP sockets remain reserved even while connected to a peer.
func observeHostPorts() (*core.HostPortObservation, error) {
	started := time.Now().UTC()
	ports := map[int]bool{}
	for _, name := range []string{"tcp", "tcp6", "udp", "udp6"} {
		file, err := os.Open("/proc/net/" + name)
		if err != nil {
			return nil, err
		}
		limited := &io.LimitedReader{R: file, N: (16 << 20) + 1}
		err = readHostPortTable(limited, strings.HasPrefix(name, "tcp"), ports)
		if limited.N <= 0 {
			err = fmt.Errorf("socket table exceeds supported size")
		}
		file.Close()
		if err != nil {
			return nil, err
		}
	}
	result := &core.HostPortObservation{ObservedAt: started, Ports: []int{}}
	for port := range ports {
		result.Ports = append(result.Ports, port)
	}
	slices.Sort(result.Ports)
	return result, nil
}

func readHostPortTable(reader io.Reader, tcp bool, ports map[int]bool) error {
	scanner := bufio.NewScanner(reader)
	if !scanner.Scan() || !strings.Contains(scanner.Text(), "local_address") {
		return fmt.Errorf("missing socket table header")
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			return fmt.Errorf("incomplete socket table")
		}
		if tcp && fields[3] != "0A" {
			continue
		}
		_, raw, ok := strings.Cut(fields[1], ":")
		port, err := strconv.ParseUint(raw, 16, 16)
		if !ok || err != nil {
			return fmt.Errorf("invalid socket address")
		}
		if port != 0 {
			ports[int(port)] = true
		}
	}
	return scanner.Err()
}
