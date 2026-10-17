package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type certificateTestTransport func(*http.Request) (*http.Response, error)

func (f certificateTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Optional real local CA gate. DNS vendor HTTP is a deterministic fixture;
// Pebble performs actual DNS-01 validation through its own resolver.
func TestCertificateLocalACMEPersistenceRenewalAndCancellation(t *testing.T) {
	directory := os.Getenv("QCH_PEBBLE_DIRECTORY")
	if directory == "" {
		t.Skip("QCH_PEBBLE_DIRECTORY enables the real local ACME gate")
	}
	db, ctx, database := isolatedConfigScopeStore(t)
	old := certificates.Directories["letsencrypt-staging"]
	certificates.Directories["letsencrypt-staging"] = directory
	defer func() { certificates.Directories["letsencrypt-staging"] = old }()
	hash, err := authn.HashPassword("local certificate account password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(ctx, core.UserRequest{Username: "local-ca-user", Role: core.RoleUser, Permissions: []core.Permission{core.PermissionTasksExecute}}, hash)
	if err != nil {
		t.Fatal(err)
	}
	owner := WithConfigScope(ctx, user.ID, false)
	_, key, err := certificates.NewKey("ecdsa256")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := certificates.Register(ctx, "letsencrypt-staging", "local@example.test", certificates.ACMEAccount{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	account, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "local CA", Kind: "acme", CA: "letsencrypt-staging", Email: "local@example.test"}, registered)
	if err != nil {
		t.Fatal(err)
	}
	dnsAccount, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "vendor fixture", Kind: "dns", Provider: "cloudflare"}, certificates.DNSCredentials{Zone: "example.test", Token: "local-only-token"})
	if err != nil {
		t.Fatal(err)
	}
	management := os.Getenv("QCH_PEBBLE_MANAGEMENT")
	dnsAddress := os.Getenv("QCH_PEBBLE_DNS")
	records := map[string]map[string]string{}
	nextID := 0
	var mu sync.Mutex
	manage := func(path string, payload any) {
		t.Helper()
		raw, _ := json.Marshal(payload)
		resp, err := http.Post(management+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("challenge fixture: %s %d", path, resp.StatusCode)
		}
	}
	configure := func(target *Store) {
		target.certificateResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, dnsAddress)
		}}
		target.certificateDNSHTTP = &http.Client{Transport: certificateTestTransport(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			defer mu.Unlock()
			body := `{"success":true,"result":{}}`
			if r.Method == "GET" {
				if strings.Contains(r.URL.Path, "dns_records") {
					var result []map[string]string
					for name, values := range records {
						for id, value := range values {
							result = append(result, map[string]string{"id": id, "name": name, "content": value})
						}
					}
					raw, _ := json.Marshal(map[string]any{"success": true, "result": result})
					body = string(raw)
				} else {
					body = `{"success":true,"result":[{"id":"local-zone"}]}`
				}
			} else if r.Method == "POST" {
				var input struct{ Name, Content string }
				if json.NewDecoder(r.Body).Decode(&input) != nil {
					t.Fatal("bad DNS payload")
				}
				nextID++
				id := fmt.Sprintf("local-record-%d", nextID)
				if records[input.Name] == nil {
					records[input.Name] = map[string]string{}
				}
				records[input.Name][id] = input.Content
				manage("/set-txt", map[string]string{"host": input.Name + ".", "value": input.Content})
				body = `{"success":true,"result":{"id":"` + id + `"}}`
			} else {
				parts := strings.Split(r.URL.Path, "/")
				id := parts[len(parts)-1]
				for name, values := range records {
					if _, ok := values[id]; ok {
						delete(values, id)
						manage("/clear-txt", map[string]string{"host": name + "."})
						for _, value := range values {
							manage("/set-txt", map[string]string{"host": name + ".", "value": value})
						}
					}
				}
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})}
	}
	configure(db)
	defer func() {
		for name := range records {
			manage("/clear-txt", map[string]string{"host": name + "."})
		}
	}()
	item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "automatic local ACME", Domains: []string{"restart.example.test", "*.restart.example.test"}, Method: "dns-api", AccountID: account.ID, DNSAccountID: dnsAccount.ID, KeyType: "ecdsa256", AutoRenew: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	issued, err := db.GetCertificate(owner, item.ID)
	if err != nil || issued.Version != 1 {
		orders, _ := db.CertificateOrders(owner, item.ID)
		t.Fatalf("real CA did not issue: %+v %v orders=%+v", issued, err, orders)
	}
	for _, values := range records {
		if len(values) != 0 {
			t.Fatal("TXT cleanup incomplete")
		}
	}
	// Restart with the same key ring; all identifiers, credentials and versions
	// are loaded from PostgreSQL rather than a login session or memory cache.
	reopened, err := OpenWithConfigKey(ctx, database, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	configure(reopened)
	if _, err = reopened.pool.Exec(ctx, `UPDATE panel_certificates SET metadata=jsonb_set(metadata,'{not_after}',to_jsonb(now()+interval '10 days')),next_attempt=now() WHERE id=$1`, item.ID); err != nil {
		t.Fatal(err)
	}
	if err = reopened.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	renewed, err := reopened.GetCertificate(owner, item.ID)
	if err != nil || renewed.Version != 2 {
		orders, _ := reopened.CertificateOrders(owner, item.ID)
		t.Fatalf("renewal: %+v %v %+v", renewed, err, orders)
	}
	var stored string
	if err = reopened.pool.QueryRow(ctx, `SELECT secret FROM certificate_versions WHERE certificate_id=$1 AND version=2`, item.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "PRIVATE KEY") || !strings.HasPrefix(stored, keyedEncryptedPrefix) {
		t.Fatal("private key stored unencrypted")
	}
	if err = reopened.RenewCertificate(owner, item.ID); err != nil {
		t.Fatal(err)
	}
	orders, err := reopened.CertificateOrders(owner, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.ContinueCertificateOrder(owner, item.ID, orders[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err = reopened.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	orders, _ = reopened.CertificateOrders(owner, item.ID)
	if orders[0].Status != "canceled" {
		t.Fatal("cancel not recovered")
	}
	t.Log("Pebble verified DNS-01 SAN + wildcard; encrypted v1 survived panel restart; persisted account renewed to v2 without login; exact TXT cleanup and cancellation passed")
}
func TestCertificateSecretsRequireEncryptionAndSupportRotation(t *testing.T) {
	db, ctx, database := isolatedConfigScopeStore(t)
	owner := WithConfigScope(ctx, "token_certificate_owner", false)
	pair, err := certificates.SelfSigned([]string{"rotation.example.test"}, "ecdsa256", 90)
	if err != nil {
		t.Fatal(err)
	}
	item, err := db.SaveCertificatePair(owner, "", "rotation", "import", pair)
	if err != nil {
		t.Fatal(err)
	}
	plain := &Store{pool: db.pool}
	if _, err = plain.SaveCertificatePair(owner, "", "plaintext", "import", pair); err == nil {
		t.Fatal("silently saved plaintext")
	}
	rotated, err := OpenWithConfigKeyring(ctx, database, true, testEncryptionKey("new-primary"), []string{testEncryptionKey("config-scope")})
	if err != nil {
		t.Fatal(err)
	}
	defer rotated.Close()
	pair2, _ := certificates.SelfSigned([]string{"rotation.example.test"}, "ecdsa256", 91)
	if _, err = rotated.SaveCertificatePair(owner, item.ID, "", "import", pair2); err != nil {
		t.Fatal(err)
	}
	var secret string
	if err = rotated.pool.QueryRow(ctx, `SELECT secret FROM certificate_versions WHERE certificate_id=$1 AND version=1`, item.ID).Scan(&secret); err != nil {
		t.Fatal(err)
	}
	var previous certificates.Pair
	if err = rotated.openCertificate(secret, &previous); err != nil {
		t.Fatal(err)
	}
	if _, err = certificates.Inspect(previous, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestCertificateHTTPPreflightChecksEveryPublicAddress(t *testing.T) {
	ctx := context.Background()
	addresses := []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("2606:4700:4700::1111")}}
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "edge.example.test" || r.URL.Path != "/.well-known/acme-challenge/token" {
			t.Error("incorrect Host/path")
		}
		_, _ = w.Write([]byte("proof"))
	}))
	defer server.Close()
	lookup := func(context.Context, string) ([]net.IPAddr, error) { return addresses, nil }
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		seen = append(seen, address)
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
	}
	if err := checkHTTPChallengeWith(ctx, "edge.example.test", "token", "proof", lookup, dial); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[1] != "[2606:4700:4700::1111]:80" {
		t.Fatalf("missing IPv6 public port 80 check: %v", seen)
	}
	addresses = append(addresses, net.IPAddr{IP: net.ParseIP("127.0.0.1")})
	if err := checkHTTPChallengeWith(ctx, "edge.example.test", "token", "proof", lookup, dial); err == nil {
		t.Fatal("accepted private challenge endpoint")
	}
	if err := checkHTTPChallengeWith(ctx, "edge.example.test", "token", "wrong", lookup, dial); err == nil {
		t.Fatal("accepted incorrect challenge response")
	}
}
