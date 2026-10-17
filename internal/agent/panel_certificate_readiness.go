package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/quic-go/quic-go"
)

// Service managers can report active after an individual inbound failed to bind.
// Verify the listener and exact issued leaf before committing the deployment.
func waitPanelCertificateListeners(ctx context.Context, task core.Task) error {
	if len(task.Certificates) == 0 {
		return nil
	}
	for _, input := range serverconfig.ParseAll(task.Engine, task.ConfigContent) {
		var material *core.DeploymentCertificate
		for i := range task.Certificates {
			candidate := &task.Certificates[i]
			if strings.Contains(input.CertificatePath, "/panel-certificates/"+candidate.ID+"/v"+strconv.Itoa(candidate.Version)+"/cert.pem") {
				material = candidate
				break
			}
		}
		if material == nil {
			continue
		}
		block, _ := pem.Decode([]byte(material.Certificate))
		if block == nil {
			return errors.New("TLS 就绪检查缺少证书")
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return err
		}
		name := input.TLSServerName
		if name == "" && len(leaf.DNSNames) > 0 {
			name = strings.Replace(leaf.DNSNames[0], "*.", "probe.", 1)
		}
		roots := x509.NewCertPool()
		// Trust only the panel's authorized leaf. Normal hostname, validity and TLS
		// signature verification remain enabled, including for imported self-signed pairs.
		roots.AddCert(leaf)
		expected := sha256.Sum256(leaf.Raw)
		config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: name, RootCAs: roots,
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 || sha256.Sum256(state.PeerCertificates[0].Raw) != expected {
					return errors.New("运行中的证书版本不匹配")
				}
				return nil
			},
		}
		if input.TLSALPN != "" {
			config.NextProtos = strings.Split(input.TLSALPN, ",")
		}
		host := input.Listen
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		if host == "::" || host == "[::]" {
			host = "::1"
		}
		address := net.JoinHostPort(host, strconv.Itoa(input.Port))
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = waitPanelCertificateListener(probeCtx, address, input.Protocol, config)
		cancel()
		if err != nil {
			return fmt.Errorf("入站 %s (%s) TLS 就绪检查失败: %w", input.Tag, address, err)
		}
	}
	return nil
}

func waitPanelCertificateListener(ctx context.Context, address, protocol string, config *tls.Config) error {
	var last error
	for {
		attempt, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		if protocol == serverconfig.ProtocolHy2 || protocol == serverconfig.ProtocolTUIC {
			options := config.Clone()
			if len(options.NextProtos) == 0 {
				options.NextProtos = []string{"h3"}
			}
			connection, err := quic.DialAddr(attempt, address, options, &quic.Config{HandshakeIdleTimeout: time.Second})
			last = err
			if err == nil {
				_ = connection.CloseWithError(0, "certificate readiness")
				cancel()
				return nil
			}
		} else {
			dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: time.Second}, Config: config}
			connection, err := dialer.DialContext(attempt, "tcp", address)
			last = err
			if err == nil {
				_ = connection.Close()
				cancel()
				return nil
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return fmt.Errorf("%v: %w", last, ctx.Err())
		case <-time.After(150 * time.Millisecond):
		}
	}
}
