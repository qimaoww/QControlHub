// Package certificates owns panel-side key material. No process-global provider
// environment, ACME client, resolver or mutable account key is shared by orders.
package certificates

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

type Pair struct {
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
}
type Info struct {
	Domains              []string
	KeyType, Fingerprint string
	NotBefore, NotAfter  time.Time
}

func Domains(values []string) ([]string, error) {
	if len(values) < 1 || len(values) > 100 {
		return nil, errors.New("请输入 1–100 个域名")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(v), "."))
		wildcard := strings.HasPrefix(v, "*.")
		v = strings.TrimPrefix(v, "*.")
		var err error
		v, err = idna.Lookup.ToASCII(v)
		if err != nil || len(v) > 253 || !strings.Contains(v, ".") || net.ParseIP(v) != nil {
			return nil, fmt.Errorf("无效域名 %q", v)
		}
		for _, label := range strings.Split(v, ".") {
			if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, errors.New("域名标签无效")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return nil, errors.New("域名包含无效字符")
				}
			}
		}
		if wildcard {
			v = "*." + v
		}
		if !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	return out, nil
}
func NewKey(kind string) (crypto.Signer, string, error) {
	var key crypto.Signer
	var err error
	switch kind {
	case "ecdsa256":
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "ecdsa384":
		key, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "rsa2048":
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	case "rsa4096":
		key, err = rsa.GenerateKey(rand.Reader, 4096)
	default:
		return nil, "", errors.New("密钥类型需为 ecdsa256、ecdsa384、rsa2048 或 rsa4096")
	}
	if err != nil {
		return nil, "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), err
}
func ParseKey(value string) (crypto.Signer, error) {
	block, rest := pem.Decode([]byte(value))
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("私钥必须是单个 PEM 块")
	}
	var valueKey any
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		valueKey, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		valueKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		valueKey, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		err = errors.New("不支持的私钥 PEM 类型")
	}
	if err != nil {
		return nil, err
	}
	key, ok := valueKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("私钥算法不支持")
	}
	return key, nil
}
func Inspect(pair Pair, now time.Time) (Info, error) {
	if len(pair.Certificate) > 256<<10 || len(pair.PrivateKey) > 32<<10 {
		return Info{}, errors.New("证书或私钥过大")
	}
	kp, err := tls.X509KeyPair([]byte(pair.Certificate), []byte(pair.PrivateKey))
	if err != nil {
		return Info{}, errors.New("证书或私钥格式无效，或私钥不匹配")
	}
	chain := []*x509.Certificate{}
	rest := []byte(pair.Certificate)
	for len(strings.TrimSpace(string(rest))) > 0 {
		b, next := pem.Decode(rest)
		if b == nil || b.Type != "CERTIFICATE" {
			return Info{}, errors.New("证书链只接受 CERTIFICATE PEM 块")
		}
		cert, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return Info{}, errors.New("证书链解析失败")
		}
		chain = append(chain, cert)
		rest = next
	}
	leaf := chain[0]
	if leaf.IsCA {
		return Info{}, errors.New("请导入服务器叶证书及其证书链")
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return Info{}, errors.New("证书尚未生效或已过期")
	}
	if len(leaf.ExtKeyUsage) > 0 {
		allowed := false
		for _, u := range leaf.ExtKeyUsage {
			allowed = allowed || u == x509.ExtKeyUsageServerAuth || u == x509.ExtKeyUsageAny
		}
		if !allowed {
			return Info{}, errors.New("证书不允许用于 TLS 服务器认证")
		}
	}
	for i := 1; i < len(chain); i++ {
		if err := chain[i-1].CheckSignatureFrom(chain[i]); err != nil {
			return Info{}, fmt.Errorf("证书链第 %d 项签名校验失败", i+1)
		}
		if now.Before(chain[i].NotBefore) || !now.Before(chain[i].NotAfter) {
			return Info{}, errors.New("证书链包含失效证书")
		}
	}
	// An included chain is checked cryptographically. Private PKI and self-signed
	// leaf certificates remain supported; importing does not assert public trust.
	domains, err := Domains(leaf.DNSNames)
	if err != nil {
		return Info{}, errors.New("证书必须包含有效 DNS SAN")
	}
	kind := ""
	switch key := kp.PrivateKey.(type) {
	case *rsa.PrivateKey:
		if key.N.BitLen() < 2048 {
			return Info{}, errors.New("RSA 密钥至少 2048 位")
		}
		kind = fmt.Sprintf("rsa%d", key.N.BitLen())
	case *ecdsa.PrivateKey:
		kind = fmt.Sprintf("ecdsa%d", key.Curve.Params().BitSize)
	default:
		return Info{}, errors.New("仅支持 RSA 或 ECDSA TLS 证书")
	}
	sum := sha256.Sum256(leaf.Raw)
	return Info{domains, kind, hex.EncodeToString(sum[:]), leaf.NotBefore, leaf.NotAfter}, nil
}
func Covers(pair Pair, names []string) error {
	block, _ := pem.Decode([]byte(pair.Certificate))
	if block == nil {
		return errors.New("证书格式无效")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := cert.VerifyHostname(name); err != nil {
			return fmt.Errorf("证书不覆盖域名 %s", name)
		}
	}
	return nil
}
func SelfSigned(domains []string, kind string, days int) (Pair, error) {
	if days < 1 || days > 825 {
		return Pair{}, errors.New("自签有效期为 1–825 天")
	}
	names, err := Domains(domains)
	if err != nil {
		return Pair{}, err
	}
	key, keyPEM, err := NewKey(kind)
	if err != nil {
		return Pair{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return Pair{}, err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: names[0]}, DNSNames: names, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(time.Duration(days) * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	return Pair{string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), keyPEM}, err
}

// Environment describes verifiable trust, not a hostname/issuer-name guess.
// Trust follows the panel trust store; this does not claim production issuance.
func Environment(pair Pair) (string, error) {
	kp, err := tls.X509KeyPair([]byte(pair.Certificate), []byte(pair.PrivateKey))
	if err != nil {
		return "", err
	}
	leaf, err := x509.ParseCertificate(kp.Certificate[0])
	if err != nil {
		return "", err
	}
	if leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil {
		return "self-signed", nil
	}
	pool := x509.NewCertPool()
	for _, der := range kp.Certificate[1:] {
		c, e := x509.ParseCertificate(der)
		if e != nil {
			return "", e
		}
		pool.AddCert(c)
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Intermediates: pool}); err == nil {
		return "system-trusted", nil
	}
	return "untrusted", nil
}
