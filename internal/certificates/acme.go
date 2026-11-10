package certificates

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"

	"net/http"
	"time"

	"golang.org/x/crypto/acme"
)

var Directories = map[string]string{
	"letsencrypt":         "https://acme-v02.api.letsencrypt.org/directory",
	"letsencrypt-staging": "https://acme-staging-v02.api.letsencrypt.org/directory",
	"zerossl":             "https://acme.zerossl.com/v2/DV90",
}

type ACMEAccount struct {
	Key    string `json:"key"`
	URL    string `json:"url"`
	EABID  string `json:"eab_id,omitempty"`
	EABKey string `json:"eab_key,omitempty"`
}

func Client(ca string, account ACMEAccount) (*acme.Client, error) {
	directory, ok := Directories[ca]
	if !ok {
		return nil, errors.New("签发机构不支持")
	}
	key, err := ParseKey(account.Key)
	if err != nil {
		return nil, err
	}
	return &acme.Client{Key: key, DirectoryURL: directory, KID: acme.KeyID(account.URL), HTTPClient: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, UserAgent: "QControlHub certificate-center"}, nil
}
func Register(ctx context.Context, ca, email string, account ACMEAccount) (ACMEAccount, error) {
	client, err := Client(ca, account)
	if err != nil {
		return account, err
	}
	input := &acme.Account{Contact: []string{"mailto:" + email}}
	if (account.EABID == "") != (account.EABKey == "") {
		return account, errors.New("EAB Key ID 与 HMAC Key 必须同时填写")
	}
	if account.EABID != "" {
		key, err := base64.RawURLEncoding.DecodeString(account.EABKey)
		if err != nil {
			key, err = base64.URLEncoding.DecodeString(account.EABKey)
		}
		if err != nil {
			return account, errors.New("EAB HMAC 密钥必须是 base64url")
		}
		input.ExternalAccountBinding = &acme.ExternalAccountBinding{KID: account.EABID, Key: key}
	}
	if ca == "zerossl" && input.ExternalAccountBinding == nil {
		return account, errors.New("ZeroSSL 需要从账号取得 EAB Key ID 与 HMAC Key")
	}
	reg, err := client.Register(ctx, input, acme.AcceptTOS)
	if err != nil {
		return account, errors.New("ACME 账号注册失败，请检查邮箱、EAB 和 CA 网络连接")
	}
	account.URL = reg.URI
	return account, nil
}
func CSR(domains []string, kind string) ([]byte, string, error) {
	key, keyPEM, err := NewKey(kind)
	if err != nil {
		return nil, "", err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: domains}, key)
	return csr, keyPEM, err
}

func Finalize(ctx context.Context, client *acme.Client, order *acme.Order, csr []byte, key string) (Pair, error) {
	// The installed x/crypto version accepts the durable order URI directly,
	// including CA finalize responses without a Location header.
	chain, _, err := client.CreateCertFromOrder(ctx, order, csr, true)
	if err != nil {
		return Pair{}, errors.New("ACME 签发或证书下载失败，请检查 CA 订单后重试")
	}
	var cert []byte
	for _, der := range chain {
		cert = append(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return Pair{string(cert), key}, nil
}
