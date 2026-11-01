package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"golang.org/x/crypto/acme"
)

type nginxACMEIssuer struct{ directory, eab string }

// Only tests supply alternate CAs/roots. Panel input cannot override endpoints,
// trust, challenge listeners or account storage locations.
type nginxACMEOptions struct {
	issuers []nginxACMEIssuer
	roots   *x509.CertPool
}

func nginxACMEDefaults() nginxACMEOptions {
	return nginxACMEOptions{issuers: []nginxACMEIssuer{
		{letsEncryptDirectory, ""},
		{zeroSSLDirectory, "https://api.zerossl.com/acme/eab-credentials-email"},
	}}
}

func (m *tlsFrontendManager) nginxACMEPath(kind, identity string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(identity)))
	return filepath.Join(m.root, "acme", kind+"-"+hex.EncodeToString(sum[:])+".json")
}

func readNginxACME(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return errors.New("不安全的节点 ACME 数据文件")
	}
	if err = validateOwner(info, "node ACME data"); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, value); err != nil {
		return errors.New("节点 ACME 数据格式无效")
	}
	return nil
}

func (m *tlsFrontendManager) writeNginxACME(kind, identity string, value any) error {
	path := m.nginxACMEPath(kind, identity)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return m.writeConfig(path, string(data), true)
}

func verifyNginxCertificate(pair certificates.Pair, domain string, roots *x509.CertPool) (*x509.Certificate, error) {
	keyPair, err := tls.X509KeyPair([]byte(pair.Certificate), []byte(pair.PrivateKey))
	if err != nil || len(keyPair.Certificate) == 0 {
		return nil, errors.New("Nginx ACME 证书与私钥不匹配")
	}
	leaf, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return nil, err
	}
	options := x509.VerifyOptions{DNSName: domain, Roots: roots, Intermediates: x509.NewCertPool()}
	for _, der := range keyPair.Certificate[1:] {
		if cert, err := x509.ParseCertificate(der); err == nil {
			options.Intermediates.AddCert(cert)
		}
	}
	_, err = leaf.Verify(options)
	return leaf, err
}

func (m *tlsFrontendManager) storedNginxCertificate(domain string, roots ...*x509.CertPool) (string, string, error) {
	var pair certificates.Pair
	err := readNginxACME(m.nginxACMEPath("certificate", domain), &pair)
	if err == nil {
		var trust *x509.CertPool
		if len(roots) > 0 {
			trust = roots[0]
		}
		if _, err = verifyNginxCertificate(pair, domain, trust); err == nil {
			return pair.Certificate, pair.PrivateKey, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	// A verified pre-upgrade Caddy certificate may bootstrap the migration.
	// It is copied into independent storage before Caddy is stopped.
	return m.storedCertificate(domain, roots...)
}

func (m *tlsFrontendManager) hasValidNginxCertificate(domain string) bool {
	_, _, err := m.storedNginxCertificate(domain)
	return err == nil
}

func nginxCertificateDue(leaf *x509.Certificate) bool {
	window := min(30*24*time.Hour, leaf.NotAfter.Sub(leaf.NotBefore)/3)
	return !time.Now().Add(window).Before(leaf.NotAfter)
}

func (m *tlsFrontendManager) ensureNginxCertificates(ctx context.Context, frontends []core.TLSFrontend, renew bool, overrides ...nginxACMEOptions) error {
	options := nginxACMEDefaults()
	if len(overrides) > 0 {
		options = overrides[0]
	}
	seen := map[string]bool{}
	var result error
	for _, f := range frontends {
		domain := strings.ToLower(f.Domain)
		if !f.AutomaticCertificate || seen[domain] {
			continue
		}
		seen[domain] = true
		cert, key, err := m.storedNginxCertificate(domain, options.roots)
		if err == nil {
			pair := certificates.Pair{Certificate: cert, PrivateKey: key}
			leaf, err := verifyNginxCertificate(pair, domain, options.roots)
			if err != nil {
				return err
			}
			var installed certificates.Pair
			storedErr := readNginxACME(m.nginxACMEPath("certificate", domain), &installed)
			if storedErr != nil && !errors.Is(storedErr, os.ErrNotExist) {
				return storedErr
			}
			if installed != pair {
				if err = m.writeNginxACME("certificate", domain, pair); err != nil {
					return err
				}
			}
			if !renew || !nginxCertificateDue(leaf) {
				continue
			}
		}
		var retry time.Time
		if renew {
			if err := readNginxACME(m.nginxACMEPath("retry", domain), &retry); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if time.Now().Before(retry) {
				continue
			}
		}
		var failures error
		for _, issuer := range options.issuers {
			if issuer.eab != "" && f.ACMEEmail == "" {
				continue
			}
			attempt, cancel := context.WithTimeout(ctx, tlsIssuerBudget)
			pair, err := m.issueNginxCertificate(attempt, f, issuer)
			cancel()
			if err == nil {
				_, err = verifyNginxCertificate(pair, domain, options.roots)
			}
			if err == nil {
				err = m.writeNginxACME("certificate", domain, pair)
			}
			if err == nil {
				failures = nil
				break
			}
			failures = errors.Join(failures, fmt.Errorf("ACME %s：%w", issuer.directory, err))
			var problem *acme.Error
			if errors.As(err, &problem) {
				value := problem.Header.Get("Retry-After")
				when, parseErr := http.ParseTime(value)
				if seconds, err := strconv.Atoi(value); err == nil {
					when, parseErr = time.Now().Add(time.Duration(seconds)*time.Second), nil
				}
				if parseErr == nil && when.After(retry) {
					retry = when
				}
			}
			if ctx.Err() != nil {
				break
			}
		}
		if failures != nil {
			if retry.Before(time.Now().Add(time.Hour)) {
				retry = time.Now().Add(time.Hour)
			}
			failures = errors.Join(failures, m.writeNginxACME("retry", domain, retry))
			result = errors.Join(result, fmt.Errorf("%s 独立证书签发失败：%w", domain, failures))
		} else if err := os.Remove(m.nginxACMEPath("retry", domain)); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (m *tlsFrontendManager) issueNginxCertificate(ctx context.Context, f core.TLSFrontend, issuer nginxACMEIssuer) (certificates.Pair, error) {
	identity := issuer.directory + "\n" + f.ACMEEmail
	var account certificates.ACMEAccount
	if err := readNginxACME(m.nginxACMEPath("account", identity), &account); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return certificates.Pair{}, err
		}
		_, key, err := certificates.NewKey("ecdsa256")
		if err != nil {
			return certificates.Pair{}, err
		}
		account.Key = key
		if err = m.writeNginxACME("account", identity, account); err != nil {
			return certificates.Pair{}, err
		}
	}
	client, err := certificates.Client("letsencrypt", account)
	if err != nil {
		return certificates.Pair{}, err
	}
	client.DirectoryURL, client.UserAgent = issuer.directory, "QControlHub node ACME"
	defer client.HTTPClient.CloseIdleConnections()
	if account.URL == "" {
		registration := &acme.Account{}
		if f.ACMEEmail != "" {
			registration.Contact = []string{"mailto:" + f.ACMEEmail}
		}
		if issuer.eab != "" {
			if account.EABID == "" {
				if err := nginxZeroSSLEAB(ctx, client.HTTPClient, issuer.eab, f.ACMEEmail, &account); err != nil {
					return certificates.Pair{}, err
				}
				if err := m.writeNginxACME("account", identity, account); err != nil {
					return certificates.Pair{}, err
				}
			}
			key, err := base64.RawURLEncoding.DecodeString(account.EABKey)
			if err != nil {
				return certificates.Pair{}, errors.New("节点 ACME EAB 数据无效")
			}
			registration.ExternalAccountBinding = &acme.ExternalAccountBinding{KID: account.EABID, Key: key}
		}
		registered, err := client.Register(ctx, registration, acme.AcceptTOS)
		if errors.Is(err, acme.ErrAccountAlreadyExists) {
			registered, err = client.GetReg(ctx, "")
		}
		if err != nil {
			return certificates.Pair{}, fmt.Errorf("注册节点 ACME 账户：%w", err)
		}
		account.URL = registered.URI
		if err = m.writeNginxACME("account", identity, account); err != nil {
			return certificates.Pair{}, err
		}
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(strings.ToLower(f.Domain)))
	if err != nil {
		return certificates.Pair{}, err
	}
	challenges := &certificateChallengeService{}
	defer challenges.Close()
	for _, authURL := range order.AuthzURLs {
		auth, err := client.GetAuthorization(ctx, authURL)
		if err != nil {
			return certificates.Pair{}, err
		}
		if auth.Identifier.Type != "dns" || !strings.EqualFold(auth.Identifier.Value, f.Domain) {
			return certificates.Pair{}, errors.New("ACME 订单包含非预期域名")
		}
		if auth.Status == acme.StatusValid {
			continue
		}
		var challenge *acme.Challenge
		for _, candidate := range auth.Challenges {
			if candidate.Type == "http-01" {
				challenge = candidate
				break
			}
		}
		if challenge == nil {
			return certificates.Pair{}, errors.New("ACME 机构未提供 HTTP-01 验证")
		}
		value, err := client.HTTP01ChallengeResponse(challenge.Token)
		if err != nil {
			return certificates.Pair{}, err
		}
		if err = challenges.Replace(ctx, []core.HTTPChallenge{{Domain: f.Domain, Token: challenge.Token, Value: value, OrderID: order.URI, ExpiresAt: time.Now().Add(10 * time.Minute)}}); err != nil {
			return certificates.Pair{}, err
		}
		if _, err = client.Accept(ctx, challenge); err != nil {
			return certificates.Pair{}, err
		}
		if _, err = client.WaitAuthorization(ctx, authURL); err != nil {
			return certificates.Pair{}, err
		}
	}
	ready, err := client.WaitOrder(ctx, order.URI)
	if err != nil {
		return certificates.Pair{}, err
	}
	ready.URI = order.URI
	csr, key, err := certificates.CSR([]string{strings.ToLower(f.Domain)}, "ecdsa256")
	if err != nil {
		return certificates.Pair{}, err
	}
	return certificates.Finalize(ctx, client, ready, csr, key)
}

func nginxZeroSSLEAB(ctx context.Context, client *http.Client, endpoint, email string, account *certificates.ACMEAccount) error {
	body := url.Values{"email": []string{email}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("申请 ZeroSSL EAB：%w", err)
	}
	defer response.Body.Close()
	var result struct {
		Success bool   `json:"success"`
		ID      string `json:"eab_kid"`
		Key     string `json:"eab_hmac_key"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result) != nil || !result.Success || result.ID == "" || result.Key == "" {
		return errors.New("ZeroSSL EAB 申请失败，请检查 ACME 邮箱或稍后重试")
	}
	account.EABID, account.EABKey = result.ID, result.Key
	return nil
}
