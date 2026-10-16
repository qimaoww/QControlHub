package agent

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"golang.org/x/net/http2"
)

// Verify the configured route and backend's transport handshake after TLS.
// Authentication/egress traffic is covered by the exported-client live matrix.
func probeFrontendProtocol(ctx context.Context, f core.TLSFrontend, config *tls.Config) error {
	if f.Transport == "xhttp" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(f.PublicPort))
	host := f.Host
	if host == "" {
		host = f.Domain
	}
	if f.Transport == "http" {
		transport := &http2.Transport{TLSClientConfig: config, DialTLSContext: func(ctx context.Context, network, _ string, cfg *tls.Config) (net.Conn, error) {
			return (&tls.Dialer{NetDialer: &net.Dialer{Timeout: 2 * time.Second}, Config: cfg}).DialContext(ctx, network, address)
		}}
		defer transport.CloseIdleConnections()
		method := f.Method
		if method == "" {
			method = http.MethodPut
		}
		request, err := http.NewRequestWithContext(ctx, method, "https://"+f.Domain+f.Path, nil)
		if err != nil {
			return err
		}
		request.Host = host
		response, err := transport.RoundTrip(request)
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode != 200 || response.ProtoMajor != 2 {
			return fmt.Errorf("HTTP/H2 路径 %s 返回 %s %d", f.Path, response.Proto, response.StatusCode)
		}
		return nil
	}
	conn, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: 2 * time.Second}, Config: config}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	key := ""
	if f.Transport == "websocket" {
		key = "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"
	}
	_, err = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n%s\r\n", f.Path, host, key)
	if err != nil {
		return err
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return err
	}
	if response.StatusCode != 101 || !strings.EqualFold(response.Header.Get("Upgrade"), "websocket") {
		return fmt.Errorf("%s 代理路径 %s 返回 HTTP %d；检查 Host、路径和内部监听", f.Transport, f.Path, response.StatusCode)
	}
	return nil
}
