package agent

import (
	"context"
	"crypto/tls"
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/quic-go/quic-go"
	"net"
	"testing"
	"time"
)

func TestPanelCertificateListenerReadiness(t *testing.T) {
	pair, err := certificates.SelfSigned([]string{"edge.example.test"}, "ecdsa256", 2)
	if err != nil {
		t.Fatal(err)
	}
	key, err := tls.X509KeyPair([]byte(pair.Certificate), []byte(pair.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	server := &tls.Config{Certificates: []tls.Certificate{key}, NextProtos: []string{"h3"}}
	taskFor := func(port, protocol string) core.Task {
		return core.Task{Engine: core.EngineMihomo, Certificates: []core.DeploymentCertificate{{ID: "crt_0000000000000001", Version: 1, Certificate: pair.Certificate}}, ConfigContent: "listeners:\n  - name: probe\n    type: " + protocol + "\n    listen: 127.0.0.1\n    port: " + port + "\n    password: fixture\n    certificate: /etc/qagent/mihomo/panel-certificates/crt_0000000000000001/v1/cert.pem\n    private-key: /etc/qagent/mihomo/panel-certificates/crt_0000000000000001/v1/key.pem\n"}
	}
	t.Run("trusted TCP leaf", func(t *testing.T) {
		listener, err := tls.Listen("tcp", "127.0.0.1:0", server)
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		go func() {
			connection, err := listener.Accept()
			if err == nil {
				defer connection.Close()
				_ = connection.(*tls.Conn).Handshake()
			}
		}()
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err = waitPanelCertificateListeners(ctx, taskFor(port, "trojan")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("trusted QUIC leaf", func(t *testing.T) {
		listener, err := quic.ListenAddr("127.0.0.1:0", server, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		go func() {
			connection, err := listener.Accept(ctx)
			if err == nil {
				<-connection.Context().Done()
			}
		}()
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		if err = waitPanelCertificateListeners(ctx, taskFor(port, "hysteria2")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("active wrong listener rejected", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		go func() {
			connection, err := listener.Accept()
			if err == nil {
				defer connection.Close()
				_, _ = connection.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
			}
		}()
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if waitPanelCertificateListeners(ctx, taskFor(port, "trojan")) == nil {
			t.Fatal("accepted non-TLS listener")
		}
	})
	t.Run("different valid certificate rejected", func(t *testing.T) {
		other, _ := certificates.SelfSigned([]string{"edge.example.test"}, "ecdsa256", 2)
		wrong, _ := tls.X509KeyPair([]byte(other.Certificate), []byte(other.PrivateKey))
		listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{wrong}})
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		go func() {
			connection, err := listener.Accept()
			if err == nil {
				defer connection.Close()
				_ = connection.(*tls.Conn).Handshake()
			}
		}()
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if waitPanelCertificateListeners(ctx, taskFor(port, "trojan")) == nil {
			t.Fatal("accepted wrong certificate version")
		}
	})
}
