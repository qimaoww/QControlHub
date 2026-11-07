package notify

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNotificationSMTPImplicitTLSAndSTARTTLS(t *testing.T) {
	for _, mode := range []string{"tls", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			fixture := httptest.NewTLSServer(nil)
			cert := fixture.TLS.Certificates[0]
			roots := x509.NewCertPool()
			roots.AddCert(fixture.Certificate())
			fixture.Close()
			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			defer serverConn.Close()
			delivered := make(chan string, 1)
			failed := make(chan error, 1)
			go func() {
				defer serverConn.Close()
				conn := serverConn
				if mode == "tls" {
					conn = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
				}
				reader := bufio.NewReader(conn)
				writer := bufio.NewWriter(conn)
				reply := func(s string) bool {
					if _, err := io.WriteString(writer, s+"\r\n"); err != nil {
						failed <- err
						return false
					}
					if err := writer.Flush(); err != nil {
						failed <- err
						return false
					}
					return true
				}
				if !reply("220 fixture ESMTP") {
					return
				}
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						failed <- err
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						if !reply("250-fixture\r\n250-STARTTLS\r\n250 AUTH PLAIN") {
							return
						}
					case strings.HasPrefix(line, "STARTTLS"):
						if !reply("220 begin TLS") {
							return
						}
						secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
						if err := secure.Handshake(); err != nil {
							failed <- err
							return
						}
						conn = secure
						reader = bufio.NewReader(conn)
						writer = bufio.NewWriter(conn)
					case strings.HasPrefix(line, "AUTH PLAIN"):
						if !strings.Contains(line, base64.StdEncoding.EncodeToString([]byte("\x00fixture-user\x00fixture-password"))) {
							t.Error("SMTP auth mapping")
						}
						if !reply("235 authenticated") {
							return
						}
					case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
						if !reply("250 accepted") {
							return
						}
					case strings.HasPrefix(line, "DATA"):
						if !reply("354 continue") {
							return
						}
						var data strings.Builder
						for {
							line, err = reader.ReadString('\n')
							if err != nil {
								failed <- err
								return
							}
							if line == ".\r\n" {
								break
							}
							data.WriteString(line)
						}
						if !reply("250 queued") {
							return
						}
						delivered <- data.String()
						return
					default:
						t.Error("unexpected SMTP command")
						return
					}
				}
			}()
			sender := &Sender{RootCAs: roots, Dial: func(context.Context, string, string) (net.Conn, error) { return clientConn, nil }}
			c := Channel{Type: "smtp", Values: map[string]string{"host": "127.0.0.1", "port": "465", "tls": mode, "username": "fixture-user", "from": "from@example.com", "to": "to@example.com"}, Secrets: map[string]string{"password": "fixture-password"}}
			e := Sample(TypeTaskFailed)
			e.Title = "中文\r\nBcc: evil@example.com"
			e.Message = "多行中文😀\n<&>"
			if err := sender.Send(context.Background(), c, e); err != nil {
				t.Fatal(err)
			}
			select {
			case data := <-delivered:
				if strings.Contains(data, "\r\nBcc:") || !strings.Contains(data, "Content-Type: text/plain; charset=UTF-8") || !strings.Contains(data, base64.StdEncoding.EncodeToString([]byte(e.Message))) {
					t.Fatal("unsafe or incorrect MIME", data)
				}
			case err := <-failed:
				t.Fatal(err)
			}
		})
	}
}

func TestNotificationSMTPCanceledTLSRemainsRetryable(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	sender := &Sender{Dial: func(context.Context, string, string) (net.Conn, error) { return client, nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := sender.Send(ctx, Channel{Type: "smtp", Values: map[string]string{"host": "localhost", "port": "465", "tls": "tls"}}, Sample(TypeTaskFailed))
	var result *DeliveryError
	if !errors.As(err, &result) || !result.Retry {
		t.Fatalf("temporary TLS failure cannot retry: %v", err)
	}
}
