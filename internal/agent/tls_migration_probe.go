package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Check the outbound connection's inode against the target core's process.
// A proxy that authenticates the same credential on the wrong backend must fail.
func peerSocketOwned(pid int, peer, local *net.TCPAddr) bool {
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return false
	}
	owned := map[string]bool{}
	for _, fd := range fds {
		link, _ := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, fd.Name()))
		owned[link] = true
	}
	for _, file := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "01" {
				continue
			}
			v4 := func(port int) string { return fmt.Sprintf("0100007F:%04X", port) }
			v6 := func(port int) string { return fmt.Sprintf("0000000000000000FFFF00000100007F:%04X", port) }
			if (fields[1] == v4(peer.Port) || fields[1] == v6(peer.Port)) && (fields[2] == v4(local.Port) || fields[2] == v6(local.Port)) && owned["socket:["+fields[9]+"]"] {
				return true
			}
		}
	}
	return false
}

// Limit the probe to a nonce echo on this host. The transient Xray client
// receives its credential via stdin, runs as the managed core account, and has
// no user-supplied outbound, destination, command or file path.
func verifyMigrationRoute(ctx context.Context, clientBinary string, pid int, f core.TLSFrontend, plan serverconfig.Input, identity *commandIdentity) error {
	if plan.Credential == "" || (plan.Protocol != "vless-ws-tls" && plan.Protocol != serverconfig.ProtocolXHTTPTLS && !strings.Contains(plan.Protocol, "vless")) {
		return errors.New("unsupported authenticated probe")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	echo, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return err
	}
	defer echo.Close()
	deadline, _ := ctx.Deadline()
	_ = echo.SetDeadline(deadline)
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	go func() {
		c, err := echo.AcceptTCP()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(deadline)
		data := make([]byte, len(nonce))
		if _, err = io.ReadFull(c, data); err == nil && bytes.Equal(data, nonce[:]) && peerSocketOwned(pid, c.RemoteAddr().(*net.TCPAddr), c.LocalAddr().(*net.TCPAddr)) {
			_, _ = c.Write(data)
		}
	}()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	tls := map[string]any{"serverName": f.Domain, "useSystemCA": true}
	stream := map[string]any{"security": "tls", "tlsSettings": tls}
	switch f.Transport {
	case "websocket":
		stream["network"] = "ws"
		stream["wsSettings"] = map[string]any{"path": f.Path, "host": f.Host}
	case "xhttp":
		stream["network"] = "xhttp"
		stream["xhttpSettings"] = map[string]any{"path": f.Path, "host": f.Host, "mode": plan.XHTTPMode}
		tls["alpn"] = []string{"h2"}
	default:
		return errors.New("unsupported migration transport")
	}
	cfg := map[string]any{"log": map[string]any{"loglevel": "none"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "tunnel", "settings": map[string]any{"address": "127.0.0.1", "port": echo.Addr().(*net.TCPAddr).Port, "network": "tcp"}}}, "outbounds": []any{map[string]any{"protocol": "vless", "settings": map[string]any{"address": "127.0.0.1", "port": f.PublicPort, "id": plan.Credential, "encryption": "none"}, "streamSettings": stream}}}
	body, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, clientBinary, "run", "-config", "stdin:")
	cmd.Env = append(commandEnvironment(""), "SSL_CERT_FILE="+f.CertificatePath)
	cmd.Stdin = bytes.NewReader(body)
	configureCommand(cmd)
	configureCommandIdentity(cmd, identity)
	if err = cmd.Start(); err != nil {
		return errors.New("verification client unavailable")
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	for ctx.Err() == nil {
		c, e := dialer.DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if e == nil {
			defer c.Close()
			_ = c.SetDeadline(deadline)
			if _, e = c.Write(nonce[:]); e != nil {
				return e
			}
			var reply [32]byte
			if _, e = io.ReadFull(c, reply[:]); e != nil || reply != nonce {
				return errors.New("backend data verification failed")
			}
			return nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	return errors.New("verification client not ready")
}
