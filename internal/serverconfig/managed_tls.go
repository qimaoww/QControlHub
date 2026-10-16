package serverconfig

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"sort"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func managedTLS(input Input) bool { return input.TLSMode == "caddy" || input.TLSMode == "nginx" }

func proxiedTLS(input Input) bool { return managedTLS(input) || input.TLSMode == "external" }

func frontendFor(input Input) core.TLSFrontend {
	f := core.TLSFrontend{Tag: input.Tag, Mode: input.TLSMode, Domain: input.TLSServerName, PublicPort: input.TLSPublicPort, BackendPort: input.Port, Transport: input.Transport, Path: input.TransportPath, Host: input.TransportHost, Method: input.TransportHTTPMethod}
	if input.TLSMode == "caddy" {
		f.ACMEEmail = input.TLSACMEEmail
	}
	return f
}

func validateManagedTLS(engine core.Engine, input Input) error {
	if input.TLSMode == "" || input.TLSMode == "native" {
		return nil
	}
	if engine != core.EngineSingBox || !input.TLSEnabled || input.RealityEnabled {
		return fmt.Errorf("托管反代仅支持 sing-box 的 TLS WebSocket / HTTPUpgrade / Caddy HTTP/H2 方案")
	}
	if input.Listen != "127.0.0.1" {
		return fmt.Errorf("托管反代的内部监听地址必须为 127.0.0.1")
	}
	f := frontendFor(input)
	// Certificate generation happens after validating the logical plan.
	if f.Mode == "nginx" {
		f.Certificate, f.PrivateKey = "pending", "pending"
	}
	if input.TLSMode == "external" {
		f.Mode = "caddy"
	}
	if err := f.Validate(); err != nil {
		return err
	}
	alpn := "http/1.1"
	if input.Transport == "http" {
		alpn = "h2"
	}
	if input.TLSALPN != "" && input.TLSALPN != alpn {
		return fmt.Errorf("此托管传输的 ALPN 必须留空或为 %s", alpn)
	}
	return nil
}

// MarshalClientMetadataWithPrevious keeps the server-side proxy key solely in
// versioned encrypted metadata. A form never receives or supplies that key.
func MarshalClientMetadataWithPrevious(input Input, previous string) (string, error) {
	encoded, err := MarshalClientMetadata(input)
	if err != nil || !proxiedTLS(input) {
		return encoded, err
	}
	var metadata clientMetadata
	if err = json.Unmarshal([]byte(encoded), &metadata); err != nil {
		return "", err
	}
	f := frontendFor(input)
	if input.TLSMode == "external" {
		metadata.ExternalFrontend = &f
		metadata.TLSALPN = input.TLSALPN
		data, err := json.Marshal(metadata)
		return string(data), err
	}
	var old clientMetadata
	if json.Unmarshal([]byte(previous), &old) == nil && old.Frontend != nil && old.Frontend.Mode == f.Mode && old.Frontend.Domain == f.Domain {
		f.Certificate, f.PrivateKey = old.Frontend.Certificate, old.Frontend.PrivateKey
	}
	if f.Mode == "nginx" {
		authority, authorityKey := old.TLSClientCertificate, old.TLSAuthorityKey
		if authorityKey == "" {
			authority, authorityKey, err = newFrontendAuthority()
			if err != nil {
				return "", err
			}
		}
		metadata.TLSClientCertificate, metadata.TLSAuthorityKey = authority, authorityKey
		pair, pairErr := tls.X509KeyPair([]byte(f.Certificate), []byte(f.PrivateKey))
		var leaf *x509.Certificate
		if pairErr == nil && len(pair.Certificate) > 0 {
			leaf, _ = x509.ParseCertificate(pair.Certificate[0])
		}
		if leaf == nil || leaf.VerifyHostname(f.Domain) != nil || time.Until(leaf.NotAfter) < 30*24*time.Hour {
			f.Certificate, f.PrivateKey, err = newFrontendCertificate(f.Domain, authority, authorityKey)
			if err != nil {
				return "", err
			}
		}
	} else {
		metadata.TLSClientCertificate = ""
	}
	if err = f.Validate(); err != nil {
		return "", err
	}
	metadata.Frontend = &f
	metadata.TLSALPN = input.TLSALPN
	data, err := json.Marshal(metadata)
	return string(data), err
}

// PreviousManagedTLSMetadata reuses the workspace's certificate authority.
// Several inbounds can then share one SNI and HTTPS port without different pins.
func PreviousManagedTLSMetadata(input Input, values map[string]string, originalTag string) string {
	if original := values[originalTag]; original != "" {
		var m clientMetadata
		if input.TLSMode != "nginx" || json.Unmarshal([]byte(original), &m) == nil && m.TLSAuthorityKey != "" {
			return original
		}
	}
	tags := make([]string, 0, len(values))
	for tag := range values {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		var m clientMetadata
		if json.Unmarshal([]byte(values[tag]), &m) == nil && m.Frontend != nil && m.Frontend.Mode == "nginx" && m.TLSAuthorityKey != "" {
			return values[tag]
		}
	}
	return ""
}

func newFrontendAuthority() (string, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "QControlHub managed TLS authority"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), nil
}

func newFrontendCertificate(domain, authority, authorityKey string) (string, string, error) {
	pair, err := tls.X509KeyPair([]byte(authority), []byte(authorityKey))
	if err != nil {
		return "", "", err
	}
	ca, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return "", "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: domain}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	if ip := net.ParseIP(domain); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{domain}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, ca, &key.PublicKey, pair.PrivateKey)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) + authority, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), nil
}

// ManagedTLSFrontends only binds metadata to the unchanged loopback inbound.
// Direct source edits that would detach a proxy are rejected rather than
// silently exposing a plaintext listener or exporting a stale public port.
func ManagedTLSFrontends(engine core.Engine, content string, metadata map[string]string) ([]core.TLSFrontend, error) {
	var root struct {
		Inbounds []struct {
			Type      string          `json:"type"`
			Tag       string          `json:"tag"`
			Listen    string          `json:"listen"`
			Port      int             `json:"listen_port"`
			TLS       json.RawMessage `json:"tls"`
			Transport struct {
				Type    string            `json:"type"`
				Path    string            `json:"path"`
				Host    json.RawMessage   `json:"host"`
				Method  string            `json:"method"`
				Headers map[string]string `json:"headers"`
			} `json:"transport"`
		} `json:"inbounds"`
	}
	if engine == core.EngineXray {
		return xhttpManagedFrontends(content, metadata)
	}
	if engine != core.EngineSingBox {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return nil, err
	}
	tags := map[string]bool{}
	ports := map[int]bool{}
	for _, ib := range root.Inbounds {
		tags[ib.Tag] = true
		ports[ib.Port] = true
	}
	for tag, value := range metadata {
		var m clientMetadata
		if json.Unmarshal([]byte(value), &m) == nil && (m.Frontend != nil || m.ExternalFrontend != nil) && !tags[tag] && ports[metadataFrontend(m).BackendPort] {
			return nil, fmt.Errorf("请通过预设表单重命名托管 TLS 入站 %s，以保留反代和证书归属", tag)
		}
	}
	var result []core.TLSFrontend
	for _, ib := range root.Inbounds {
		var m clientMetadata
		if json.Unmarshal([]byte(metadata[ib.Tag]), &m) != nil || metadataFrontend(m) == nil {
			continue
		}
		f := *metadataFrontend(m)
		transport := map[string]string{"ws": "websocket", "httpupgrade": "httpupgrade", "http": "http"}[ib.Transport.Type]
		if nativeProtocol(m.Protocol) != ib.Type || ib.Listen != "127.0.0.1" || ib.Port != f.BackendPort || transport != f.Transport || ib.Transport.Path != f.Path || len(ib.TLS) > 0 && string(ib.TLS) != "null" {
			return nil, fmt.Errorf("入站 %s 使用托管 TLS，请通过预设表单修改内部端口、路径或 TLS 模式", ib.Tag)
		}
		f.Tag = ib.Tag
		f.Host = ""
		_ = json.Unmarshal(ib.Transport.Host, &f.Host)
		if f.Host == "" {
			var hosts []string
			_ = json.Unmarshal(ib.Transport.Host, &hosts)
			if len(hosts) > 0 {
				f.Host = hosts[0]
			}
		}
		if f.Host == "" {
			f.Host = ib.Transport.Headers["Host"]
		}
		f.Method = ib.Transport.Method
		if m.ExternalFrontend != nil {
			check := f
			check.Mode = "caddy"
			if err := check.Validate(); err != nil {
				return nil, err
			}
			continue
		}
		if err := f.Validate(); err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, nil
}

func metadataFrontend(m clientMetadata) *core.TLSFrontend {
	if m.ExternalFrontend != nil {
		return m.ExternalFrontend
	}
	return m.Frontend
}
