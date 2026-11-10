package agent

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func validateImportCertificateMaterials(engine core.Engine, refs []importFileReference, contents map[string][]byte) error {
	type material struct {
		certificate, key []byte
		field            string
	}
	pairs := map[string]*material{}
	for _, ref := range refs {
		field := ref.field
		cert, key := false, false
		group := field
		switch engine {
		case core.EngineSingBox:
			if strings.Contains(field, ".tls.") {
				for _, prefix := range []string{"client_", ""} {
					if strings.HasSuffix(field, "."+prefix+"certificate_path") {
						cert = true
						group = strings.TrimSuffix(field, prefix+"certificate_path") + prefix
						break
					}
					if strings.HasSuffix(field, "."+prefix+"key_path") {
						key = true
						group = strings.TrimSuffix(field, prefix+"key_path") + prefix
						break
					}
				}
			}
		case core.EngineXray:
			cert = strings.HasSuffix(field, ".certificateFile")
			key = strings.HasSuffix(field, ".keyFile")
			group = strings.TrimSuffix(strings.TrimSuffix(field, "certificateFile"), "keyFile")
		case core.EngineMihomo:
			cert = strings.HasSuffix(field, ".certificate")
			key = strings.HasSuffix(field, ".private-key")
			group = strings.TrimSuffix(strings.TrimSuffix(field, "certificate"), "private-key")
		}
		if !cert && !key {
			continue
		}
		item := pairs[group]
		if item == nil {
			item = &material{field: group}
			pairs[group] = item
		}
		if cert {
			item.certificate = append(item.certificate, contents[ref.path]...)
		} else {
			item.key = contents[ref.path]
		}
	}
	for _, item := range pairs {
		if len(item.certificate) == 0 || len(item.key) == 0 {
			continue
		} // Inline counterpart is checked by the source core.
		pair, err := tls.X509KeyPair(item.certificate, item.key)
		if err != nil {
			return fmt.Errorf("%s: certificate and private key are not a usable pair", item.field)
		}
		for _, der := range pair.Certificate {
			certificate, err := x509.ParseCertificate(der)
			if err != nil {
				return fmt.Errorf("%s: invalid certificate chain", item.field)
			}
			now := time.Now()
			if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
				return fmt.Errorf("%s: certificate is outside its validity period", item.field)
			}
		}
	}
	// CA bundles must contain actual certificates, never an arbitrary secret file.
	for _, ref := range refs {
		if strings.HasSuffix(ref.field, ".ca") || strings.HasSuffix(ref.field, ".ca-file") || strings.Contains(ref.field, "client_certificate_path") {
			data := contents[ref.path]
			count := 0
			for len(data) > 0 {
				block, rest := pem.Decode(data)
				if block == nil {
					break
				}
				data = rest
				if block.Type == "CERTIFICATE" {
					if _, err := x509.ParseCertificate(block.Bytes); err != nil {
						return fmt.Errorf("%s: invalid CA certificate", ref.field)
					}
					count++
				}
			}
			if count == 0 {
				return fmt.Errorf("%s: CA bundle contains no valid certificate", ref.field)
			}
		}
	}
	return nil
}
