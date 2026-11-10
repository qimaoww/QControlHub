package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// challtestsrv serves real TXT answers but returns NOTIMP for NS. Its isolated
// fixture namespace has no delegated child zones; model that explicitly.
type certificateLocalResolver struct{ *net.Resolver }

func (r certificateLocalResolver) LookupNS(_ context.Context, name string) ([]*net.NS, error) {
	if name == "example.test" || name == "other.test" {
		return []*net.NS{{Host: "ns.local-fixture.test."}}, nil
	}
	return nil, &net.DNSError{Err: "no child delegation in local fixture", Name: name, IsNotFound: true}
}

// Optional real local CA gate. DNS vendor HTTP is a deterministic fixture;
// Pebble performs actual DNS-01 validation through its own resolver.
func TestCertificateLocalACMEPersistenceRenewalAndCancellation(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "user"
		if legacy {
			name = "legacy_admin"
		}
		t.Run(name, func(t *testing.T) { testCertificateLocalACMEPersistenceRenewalAndCancellation(t, legacy) })
	}
}

func testCertificateLocalACMEPersistenceRenewalAndCancellation(t *testing.T, legacy bool) {
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
	if legacy {
		owner = WithConfigScope(ctx, "", true)
	}
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
	dnsAccount, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "vendor fixture", Kind: "dns", Provider: "cloudflare"}, certificates.DNSCredentials{Token: "local-only-token"})
	if err != nil {
		t.Fatal(err)
	}
	management := os.Getenv("QCH_PEBBLE_MANAGEMENT")
	dnsAddress := os.Getenv("QCH_PEBBLE_DNS")
	records := map[string]map[string]string{}
	nextID := 0
	holdTXT := true
	cleanupFailures := 2
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
		target.certificateResolver = certificateLocalResolver{Resolver: &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, dnsAddress)
		}}}
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
					body = `{"success":true,"result":[]}`
					if r.URL.Query().Get("name") == "example.test" {
						body = `{"success":true,"result":[{"id":"local-zone","name":"example.test"}]}`
					}
					if r.URL.Query().Get("name") == "other.test" {
						body = `{"success":true,"result":[{"id":"other-zone","name":"other.test"}]}`
					}
				}
			} else if r.Method == "POST" {
				var input struct{ Name, Content string }
				if json.NewDecoder(r.Body).Decode(&input) != nil {
					t.Fatal("bad DNS payload")
				}
				expectedZone := "local-zone"
				if strings.HasSuffix(input.Name, ".other.test") {
					expectedZone = "other-zone"
				}
				if r.URL.Path != "/client/v4/zones/"+expectedZone+"/dns_records" {
					t.Fatalf("wrong SAN zone: %s for %s", r.URL.Path, input.Name)
				}
				nextID++
				id := fmt.Sprintf("local-record-%d", nextID)
				if records[input.Name] == nil {
					records[input.Name] = map[string]string{}
				}
				records[input.Name][id] = input.Content
				if !holdTXT {
					manage("/set-txt", map[string]string{"host": input.Name + ".", "value": input.Content})
				}
				body = `{"success":true,"result":{"id":"` + id + `"}}`
			} else {
				if cleanupFailures > 0 {
					cleanupFailures--
					return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
				}
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
			if r.URL.Path == "/client/v4/zones" && r.URL.Query().Get("name") != "example.test" && r.URL.Query().Get("name") != "other.test" {
				body = `{"success":true,"result":[]}`
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
	item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "automatic local ACME", Domains: []string{"restart.example.test", "*.restart.example.test", "alt.other.test"}, Method: "dns-api", AccountID: account.ID, DNSAccountID: dnsAccount.ID, KeyType: "ecdsa256", AutoRenew: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate propagation delay followed by loss of already acknowledged TXT
	// resources between executions. Keep durable receipts and restart the store.
	pendingOrders, err := db.CertificateOrders(owner, item.ID)
	if err != nil || pendingOrders[0].Status != "queued" || pendingOrders[0].Attempt != 1 {
		t.Fatalf("expected propagation retry: %+v %v", pendingOrders, err)
	}
	mu.Lock()
	records = map[string]map[string]string{}
	holdTXT = false
	mu.Unlock()
	recovery, err := OpenWithConfigKey(ctx, database, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer recovery.Close()
	configure(recovery)
	if _, err = db.pool.Exec(ctx, `UPDATE certificate_orders SET next_attempt=now() WHERE id=$1`, pendingOrders[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = recovery.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	issued, err := db.GetCertificate(owner, item.ID)
	if err != nil || issued.Version != 1 {
		orders, _ := db.CertificateOrders(owner, item.ID)
		t.Fatalf("real CA did not issue: %+v %v orders=%+v", issued, err, orders)
	}
	cleanupOrders, err := db.CertificateOrders(owner, item.ID)
	if err != nil || cleanupOrders[0].Status != "succeeded" || cleanupOrders[0].Stage != "cleanup" || cleanupOrders[0].Attempt != 2 || cleanupOrders[0].CleanupError == "" {
		t.Fatalf("successful issuance lost to cleanup: %+v %v", cleanupOrders, err)
	}
	for i := 0; i < 2; i++ {
		if _, err = db.pool.Exec(ctx, `UPDATE certificate_orders SET next_attempt=now() WHERE id=$1`, cleanupOrders[0].ID); err != nil {
			t.Fatal(err)
		}
		if err = recovery.RunCertificateMaintenance(ctx); err != nil {
			t.Fatal(err)
		}
	}
	cleanupOrders, err = db.CertificateOrders(owner, item.ID)
	if err != nil || len(cleanupOrders) != 1 || cleanupOrders[0].Status != "succeeded" || cleanupOrders[0].Stage != "complete" || cleanupOrders[0].Attempt != 2 || cleanupOrders[0].CleanupError != "" || cleanupOrders[0].NextAttempt != nil {
		t.Fatalf("cleanup retries changed issuance: %+v %v", cleanupOrders, err)
	}
	issued, err = db.GetCertificate(owner, item.ID)
	if err != nil || issued.Version != 1 {
		t.Fatalf("cleanup saved again: %+v %v", issued, err)
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
	if _, err = reopened.pool.Exec(ctx, `UPDATE panel_certificates SET metadata=jsonb_set(metadata - 'owner_id','{not_after}',to_jsonb(now()+interval '10 days')),next_attempt=now() WHERE id=$1`, item.ID); err != nil {
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
	if _, err = reopened.UpdateCertificateDNSCredentials(owner, dnsAccount.ID, "rotated vendor", certificates.DNSCredentials{Token: "rotated-local-token", Zone: "obsolete.invalid"}); err != nil {
		t.Fatal(err)
	}
	if err = reopened.RenewCertificate(owner, item.ID); err != nil {
		t.Fatal(err)
	}
	if err = reopened.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	manuallyRenewed, err := reopened.GetCertificate(owner, item.ID)
	if err != nil || manuallyRenewed.Version != 3 {
		t.Fatalf("manual renewal: %+v %v", manuallyRenewed, err)
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
	manual, err := reopened.CreateACMECertificate(owner, core.Certificate{Name: "manual local ACME", Domains: []string{"manual-consent.example.test"}, Method: "dns-manual", AccountID: account.ID, KeyType: "ecdsa256"})
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	orders, err = reopened.CertificateOrders(owner, manual.ID)
	if err != nil || len(orders) != 1 || orders[0].Status != "waiting_dns" || orders[0].Attempt != 1 || len(orders[0].Challenges) != 1 {
		t.Fatalf("manual DNS consent state: %+v %v", orders, err)
	}
	manualOrder := orders[0]
	if err = reopened.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	orders, _ = reopened.CertificateOrders(owner, manual.ID)
	if orders[0].Status != "waiting_dns" || orders[0].Attempt != 1 {
		t.Fatal("maintenance executed a manual order without consent")
	}
	if err = reopened.ContinueCertificateOrder(owner, manual.ID, manualOrder.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = reopened.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	orders, _ = reopened.CertificateOrders(owner, manual.ID)
	if orders[0].Status != "waiting_dns" || orders[0].Attempt != 2 || !strings.Contains(orders[0].Error, "DNS TXT") {
		t.Fatalf("unpropagated manual TXT did not remain actionable: %+v", orders)
	}
	challenge := manualOrder.Challenges[0]
	manage("/set-txt", map[string]string{"host": challenge.Name + ".", "value": challenge.Value})
	defer manage("/clear-txt", map[string]string{"host": challenge.Name + "."})
	if err = reopened.ContinueCertificateOrder(owner, manual.ID, manualOrder.ID, false); err != nil {
		t.Fatal(err)
	}
	// Isolated crash fixture: the worker claimed the real order, then stopped
	// before validation. A new Store must honor its persisted continuation.
	if _, err = reopened.pool.Exec(ctx, `UPDATE certificate_orders SET status='running' WHERE id=$1`, manualOrder.ID); err != nil {
		t.Fatal(err)
	}
	resumed, err := OpenWithConfigKey(ctx, database, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	configure(resumed)
	if err = resumed.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	orders, err = resumed.CertificateOrders(owner, manual.ID)
	if err != nil || orders[0].Status != "succeeded" || orders[0].Attempt != 3 || orders[0].Error != "" {
		t.Fatalf("continued manual order did not resume after restart: %+v %v", orders, err)
	}
	t.Log("Pebble verified DNS-01 SAN + wildcard; encrypted v1 survived panel restart; persisted account renewed to v2 without login; exact TXT cleanup and cancellation passed")
	t.Log("Pebble verified manual DNS consent, missing TXT recovery and continued validation after worker restart")
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

func TestCertificateDNSResponseLossRecoveryPreservesExistingRecords(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	var body = `{"success":true,"result":[{"id":"existing","name":"_acme-challenge.example.test","content":"same-proof"},{"id":"created-by-order","name":"_acme-challenge.example.test","content":"same-proof"},{"id":"other-proof","name":"_acme-challenge.example.test","content":"other"}]}`
	client := certificates.DNSClient{Provider: "cloudflare", Credentials: certificates.DNSCredentials{Zone: "example.test", Token: "fixture"}, HTTP: &http.Client{Transport: certificateTestTransport(func(r *http.Request) (*http.Response, error) {
		response := body
		if r.URL.Path == "/client/v4/zones" {
			response = `{"success":true,"result":[{"id":"zone","name":"example.test"}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: http.Header{}}, nil
	})}}
	challenge := certificateChallengeState{CreatePending: true, BeforeIDs: []string{"existing"}, Public: core.CertificateChallenge{Name: "_acme-challenge.example.test", Value: "same-proof"}}
	if err := db.recoverDNSCreate(ctx, client, &challenge); err != nil {
		t.Fatal(err)
	}
	if challenge.Record.ID != "created-by-order" || challenge.CreatePending {
		t.Fatalf("incorrect recovery: %+v", challenge)
	}
	body = `{"success":true,"result":[{"id":"existing","name":"_acme-challenge.example.test","content":"same-proof"},{"id":"ambiguous-1","name":"_acme-challenge.example.test","content":"same-proof"},{"id":"ambiguous-2","name":"_acme-challenge.example.test","content":"same-proof"}]}`
	challenge.Record = certificates.DNSRecord{}
	challenge.CreatePending = true
	if err := db.recoverDNSCreate(ctx, client, &challenge); err == nil || challenge.Record.ID != "" {
		t.Fatal("ambiguous response claimed another writer's record")
	}
	client.Credentials.Zone = "removed.example.test"
	challenge.Zone = certificates.DNSZone{}
	if err := db.recoverDNSCreate(ctx, client, &challenge); err == nil || !strings.Contains(err.Error(), "原 DNS 区域") || challenge.Record.ID != "" {
		t.Fatalf("legacy recovery fell back to another zone: %+v %v", challenge, err)
	}
}

func TestCertificateCancellationDuringValidationDoesNotDelayCleanup(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	owner := WithConfigScope(ctx, "token_certificate_cancel", false)
	account, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "unused CA", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{})
	if err != nil {
		t.Fatal(err)
	}
	for _, startedStatus := range []string{"queued", "canceling"} {
		t.Run(startedStatus, func(t *testing.T) {
			item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "cancel during validation", Domains: []string{"cancel.example.test"}, Method: "dns-manual", AccountID: account.ID, KeyType: "ecdsa256"})
			if err != nil {
				t.Fatal(err)
			}
			orders, err := db.CertificateOrders(owner, item.ID)
			if err != nil || len(orders) != 1 {
				t.Fatalf("order missing: %v", err)
			}
			id := orders[0].ID
			if err := db.ContinueCertificateOrder(owner, item.ID, id, true); err != nil {
				t.Fatal(err)
			}
			// Interleave cancellation with recording a failure from an operation
			// that already started. No external CA is needed to test scheduling.
			if err := db.recordCertificateOrderFailure(ctx, id, "dns-manual", startedStatus, 6, errors.New("validation interrupted")); err != nil {
				t.Fatal(err)
			}
			var status string
			var immediatelyDue, backedOff bool
			if err := db.pool.QueryRow(ctx, `SELECT status,next_attempt<=now(),next_attempt>now()+interval '63 minutes' FROM certificate_orders WHERE id=$1`, id).Scan(&status, &immediatelyDue, &backedOff); err != nil {
				t.Fatal(err)
			}
			if status != "canceling" || immediatelyDue != (startedStatus == "queued") || backedOff != (startedStatus == "canceling") {
				t.Fatalf("incorrect cancellation schedule: status=%s due=%v backoff=%v", status, immediatelyDue, backedOff)
			}
			if startedStatus == "queued" {
				if err := db.RunCertificateMaintenance(ctx); err != nil {
					t.Fatal(err)
				}
				orders, err = db.CertificateOrders(owner, item.ID)
				if err != nil || orders[0].Status != "canceled" {
					t.Fatalf("next maintenance did not clean the canceled order: %+v %v", orders, err)
				}
			}
		})
	}
}

func TestCertificateFinishedOrderSchedulesImmediateCleanup(t *testing.T) {
	for _, reason := range []struct {
		name    string
		attempt int
		cause   error
	}{
		{"invalid authorization", 0, fmt.Errorf("validation rejected: %w", errCertificateOrderInvalid)},
		{"retry limit reached", 7, errors.New("validation repeatedly timed out")},
	} {
		t.Run(reason.name, func(t *testing.T) {
			testCertificateFinishedOrderCleanup(t, reason.attempt, reason.cause)
		})
	}
}

func testCertificateFinishedOrderCleanup(t *testing.T, attempt int, cause error) {
	t.Helper()
	db, ctx, _ := isolatedConfigScopeStore(t)
	owner := WithConfigScope(ctx, "token_certificate_invalid", false)
	account, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "unused CA", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{})
	if err != nil {
		t.Fatal(err)
	}
	item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "invalid order", Domains: []string{"invalid.example.test"}, Method: "dns-manual", AccountID: account.ID, KeyType: "ecdsa256"})
	if err != nil {
		t.Fatal(err)
	}
	orders, err := db.CertificateOrders(owner, item.ID)
	if err != nil || len(orders) != 1 {
		t.Fatalf("order missing: %v", err)
	}
	id := orders[0].ID
	if _, err := db.pool.Exec(ctx, `INSERT INTO certificate_dns_leases(name,value,order_id) VALUES('fixture.invalid.example.test','proof',$1)`, id); err != nil {
		t.Fatal(err)
	}
	if err := db.recordCertificateOrderFailure(ctx, id, "dns-manual", "queued", attempt, cause); err != nil {
		t.Fatal(err)
	}
	var status, stage string
	var due bool
	if err := db.pool.QueryRow(ctx, `SELECT status,stage,next_attempt<=now() FROM certificate_orders WHERE id=$1`, id).Scan(&status, &stage, &due); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || stage != "cleanup" || !due {
		t.Fatalf("invalid CA order was retried: %s %s due=%v", status, stage, due)
	}
	if err := db.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	var leases int
	if err := db.pool.QueryRow(ctx, `SELECT stage,(SELECT count(*) FROM certificate_dns_leases WHERE order_id=$1) FROM certificate_orders WHERE id=$1`, id).Scan(&stage, &leases); err != nil {
		t.Fatal(err)
	}
	if stage != "complete" || leases != 0 {
		t.Fatalf("terminal order cleanup was not completed: stage=%s leases=%d", stage, leases)
	}
}

// Use only an isolated, deliberately unavailable CA. Observe durable state while
// the local request is in flight, then verify scheduling and cancellation.
func TestCertificateWorkerPublishesExecutionAndRetryState(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	owner := WithConfigScope(ctx, "token_worker_status", false)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
		http.Error(w, "fixture CA unavailable", http.StatusBadRequest)
	}))
	defer server.Close()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	old := certificates.Directories["letsencrypt-staging"]
	certificates.Directories["letsencrypt-staging"] = server.URL
	defer func() { certificates.Directories["letsencrypt-staging"] = old }()
	_, key, err := certificates.NewKey("ecdsa256")
	if err != nil {
		t.Fatal(err)
	}
	account, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "local CA", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "worker states", Domains: []string{"worker.example.test"}, Method: "dns-manual", AccountID: account.ID, KeyType: "ecdsa256"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- db.RunCertificateMaintenance(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	orders, err := db.CertificateOrders(owner, item.ID)
	if err != nil || len(orders) != 1 || orders[0].Status != "running" || orders[0].Attempt != 1 {
		t.Fatalf("in-flight state: %+v %v", orders, err)
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	orders, err = db.CertificateOrders(owner, item.ID)
	if err != nil || orders[0].Status != "queued" || orders[0].Stage != "retry" || orders[0].Attempt != 1 || orders[0].NextAttempt == nil || !orders[0].NextAttempt.After(time.Now()) {
		t.Fatalf("retry state: %+v %v", orders, err)
	}
	if len(orders[0].Challenges) != 0 {
		t.Fatal("a failed setup must not ask for DNS records")
	}
	listed, err := db.GetCertificate(owner, item.ID)
	if err != nil || listed.Status != "retrying" {
		t.Fatalf("certificate retry label: %+v %v", listed, err)
	}
	if err := db.ContinueCertificateOrder(owner, item.ID, orders[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	orders, err = db.CertificateOrders(owner, item.ID)
	if err != nil || orders[0].Status != "canceled" || orders[0].Attempt != 1 || orders[0].Error != "" {
		t.Fatalf("cancellation counted as issuance or retained error: %+v %v", orders, err)
	}
}

func TestCertificateCleanupPreservesOutcomeAndAttempt(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	owner := WithConfigScope(ctx, "token_cleanup_status", false)
	account, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "fixture CA", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{})
	if err != nil {
		t.Fatal(err)
	}
	item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "cleanup states", Domains: []string{"cleanup.example.test"}, Method: "dns-manual", AccountID: account.ID, KeyType: "ecdsa256"})
	if err != nil {
		t.Fatal(err)
	}
	orders, err := db.CertificateOrders(owner, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := orders[0].ID
	// A synthetic encrypted cleanup journal points to an absent local DNS account.
	state := certificateOrderState{Challenges: []certificateChallengeState{{Record: certificates.DNSRecord{ID: "fixture-record"}}}}
	item.Method, item.DNSAccountID = "dns-api", "missing-fixture-account"
	for _, status := range []string{"succeeded", "failed"} {
		if _, err := db.pool.Exec(ctx, `UPDATE certificate_orders SET status=$2,stage='cleanup',attempt=2,error='original issuance failure' WHERE id=$1`, id, status); err != nil {
			t.Fatal(err)
		}
		if err := db.finishCertificateOrderCleanup(WithConfigScope(ctx, item.OwnerID, false), item, id, &state); err != nil {
			t.Fatal(err)
		}
		orders, err = db.CertificateOrders(owner, item.ID)
		if err != nil || orders[0].Status != status || orders[0].Stage != "cleanup" || orders[0].Attempt != 2 || orders[0].NextAttempt == nil || !orders[0].NextAttempt.After(time.Now()) {
			t.Fatalf("cleanup changed outcome: %+v %v", orders, err)
		}
		if status == "failed" && orders[0].Error != "original issuance failure" {
			t.Fatal("cleanup lost issuance failure")
		}
		item.Method = "dns-manual"
		if err := db.finishCertificateOrderCleanup(owner, item, id, &state); err != nil {
			t.Fatal(err)
		}
		orders, err = db.CertificateOrders(owner, item.ID)
		if err != nil || orders[0].Stage != "complete" || (status == "succeeded" && orders[0].Error != "") {
			t.Fatalf("cleanup completion: %+v %v", orders, err)
		}
		item.Method = "dns-api"
	}
}

func TestCertificateCleanupTimeoutPersistsIndependentOutcome(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	owner := WithConfigScope(ctx, "token_cleanup_timeout", false)
	account, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "fixture", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{})
	if err != nil {
		t.Fatal(err)
	}
	item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "cleanup deadline", Domains: []string{"timeout.example.test"}, Method: "dns-manual", AccountID: account.ID, KeyType: "ecdsa256"})
	if err != nil {
		t.Fatal(err)
	}
	orders, err := db.CertificateOrders(owner, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := orders[0].ID
	for _, status := range []string{"failed", "succeeded", "canceling"} {
		if _, err = db.pool.Exec(ctx, `UPDATE certificate_orders SET status=$2,stage='cleanup',attempt=8,error='original validation failure',next_attempt=now()-interval '1 day' WHERE id=$1`, id, status); err != nil {
			t.Fatal(err)
		}
		state := certificateOrderState{FailureStage: "validating"}
		expired, cancel := context.WithCancel(owner)
		cancel()
		if err = db.finishCertificateOrderCleanup(expired, item, id, &state); err != nil {
			t.Fatal(err)
		}
		orders, err = db.CertificateOrders(owner, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		got := orders[0]
		if got.Status != status || got.Stage != "cleanup" || got.Attempt != 8 || got.Error != "original validation failure" || got.CleanupError == "" || got.FailureStage != "validating" || got.NextAttempt == nil || !got.NextAttempt.After(time.Now()) {
			t.Fatalf("timeout outcome: %+v", got)
		}
		if err = db.finishCertificateOrderCleanup(owner, item, id, &state); err != nil {
			t.Fatal(err)
		}
		orders, err = db.CertificateOrders(owner, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		got = orders[0]
		expected := status
		if status == "canceling" {
			expected = "canceled"
		}
		if got.Status != expected || got.Stage != "complete" || got.Attempt != 8 || got.CleanupError != "" || got.NextAttempt != nil {
			t.Fatalf("completed outcome: %+v", got)
		}
		if status == "failed" && got.Error != "original validation failure" {
			t.Fatal("original failure lost")
		}
	}
}

// A local ACME contract fixture stops before public DNS resolution. The vendor
// write deliberately fails so only a missing receipt can reach it.
func TestCertificateDNSRetryRechecksPersistedReceipt(t *testing.T) {
	for _, scenario := range []string{"missing", "present", "lookup failure"} {
		t.Run(scenario, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			owner := WithConfigScope(ctx, "token_dns_receipt", false)
			var caURL string
			ca := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Replay-Nonce", "local-fixture-nonce")
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/dir":
					fmt.Fprintf(w, `{"newNonce":%q,"newAccount":%q,"newOrder":%q}`, caURL+"/nonce", caURL+"/acct", caURL+"/new-order")
				case "/nonce":
					w.WriteHeader(200)
				case "/order":
					fmt.Fprintf(w, `{"status":"pending","authorizations":[%q],"finalize":%q}`, caURL+"/auth", caURL+"/finalize")
				case "/auth":
					fmt.Fprint(w, `{"status":"pending","identifier":{"type":"dns","value":"receipt.example.test"},"challenges":[]}`)
				default:
					http.Error(w, "fixture stops before challenge validation", 400)
				}
			}))
			defer ca.Close()
			caURL = ca.URL
			old := certificates.Directories["letsencrypt-staging"]
			certificates.Directories["letsencrypt-staging"] = caURL + "/dir"
			defer func() { certificates.Directories["letsencrypt-staging"] = old }()
			_, key, err := certificates.NewKey("ecdsa256")
			if err != nil {
				t.Fatal(err)
			}
			acct, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "local", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{Key: key, URL: caURL + "/acct/1"})
			if err != nil {
				t.Fatal(err)
			}
			dns, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "vendor", Kind: "dns", Provider: "cloudflare"}, certificates.DNSCredentials{Zone: "example.test", Token: "local-fixture"})
			if err != nil {
				t.Fatal(err)
			}
			item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "receipt", Domains: []string{"receipt.example.test"}, Method: "dns-api", AccountID: acct.ID, DNSAccountID: dns.ID, KeyType: "ecdsa256"})
			if err != nil {
				t.Fatal(err)
			}
			orders, _ := db.CertificateOrders(owner, item.ID)
			id := orders[0].ID
			writes := 0
			db.certificateDNSHTTP = &http.Client{Transport: certificateTestTransport(func(r *http.Request) (*http.Response, error) {
				status := 200
				body := `{"success":true,"result":[{"id":"zone","name":"example.test"}]}`
				if r.Method != "GET" {
					writes++
					status = 503
					body = `{}`
				} else if strings.Contains(r.URL.Path, "dns_records") {
					body = `{"success":true,"result":[{"id":"peer","name":"_acme-challenge.receipt.example.test","content":"proof"}]}`
					if scenario == "present" {
						body = `{"success":true,"result":[{"id":"receipt","name":"_acme-challenge.receipt.example.test","content":"proof"}]}`
					} else if scenario == "lookup failure" {
						status = 503
					}
				}
				if r.URL.Path == "/client/v4/zones" && r.URL.Query().Get("name") != "example.test" {
					body = `{"success":true,"result":[]}`
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
			})}
			state := certificateOrderState{URL: caURL + "/order", Challenges: []certificateChallengeState{{Public: core.CertificateChallenge{Domain: "receipt.example.test", Name: "_acme-challenge.receipt.example.test", Value: "proof", Type: "dns-01", Status: "pending"}, AuthURL: caURL + "/auth", Record: certificates.DNSRecord{ID: "receipt", Zone: "zone"}, Zone: certificates.DNSZone{ID: "zone", Name: "example.test"}}}}
			if err = db.saveCertificateOrderState(owner, id, state, "dns_created"); err != nil {
				t.Fatal(err)
			}
			if err = db.processCertificateOrder(owner, item, id, "queued", state); err == nil {
				t.Fatal("fixture must fail")
			}
			expected := 0
			if writes != expected {
				t.Fatalf("vendor writes=%d, want %d", writes, expected)
			}
			if scenario == "missing" {
				var secret string
				if err = db.pool.QueryRow(ctx, `SELECT secret FROM certificate_orders WHERE id=$1`, id).Scan(&secret); err != nil {
					t.Fatal(err)
				}
				var saved certificateOrderState
				if err = db.openCertificate(secret, &saved); err != nil {
					t.Fatal(err)
				}
				c := saved.Challenges[0]
				if c.CreatePending || !c.Borrowed || c.Record.ID != "peer" {
					t.Fatalf("lost peer ownership boundary: %+v", c)
				}
			}
		})
	}
}

func TestCertificateCleanupPartialRetryAcrossRestartIsIsolated(t *testing.T) {
	db, ctx, database := isolatedConfigScopeStore(t)
	owner := WithConfigScope(ctx, "token_cleanup_owner", false)
	other := WithConfigScope(ctx, "token_cleanup_peer", false)
	acct, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "local", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{})
	if err != nil {
		t.Fatal(err)
	}
	dns, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "vendor", Kind: "dns", Provider: "cloudflare"}, certificates.DNSCredentials{Zone: "example.test", Token: "local"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "partial cleanup", Domains: []string{"shared.example.test"}, Method: "dns-api", AccountID: acct.ID, DNSAccountID: dns.ID, KeyType: "ecdsa256"})
	if err != nil {
		t.Fatal(err)
	}
	orders, err := db.CertificateOrders(owner, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := orders[0].ID
	if _, err = db.CertificateOrders(other, item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("peer read: %v", err)
	}
	if err = db.ContinueCertificateOrder(other, item.ID, id, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("peer cancel: %v", err)
	}
	if _, err = db.UpdateCertificateDNSCredentials(owner, dns.ID, "rotated", certificates.DNSCredentials{Token: "rotated", Zone: "other.test"}); err != nil {
		t.Fatal(err)
	}
	var rotated certificates.DNSCredentials
	if _, err = db.CertificateAccountSecret(owner, dns.ID, "dns", &rotated); err != nil || rotated.Zone != "example.test" {
		t.Fatalf("lost legacy recovery hint: %v", err)
	}
	records := map[string]string{"first": "value-a", "second": "value-b", "peer": "value-a"}
	deletes := map[string]int{}
	failures := 2
	configure := func(target *Store) {
		target.certificateDNSHTTP = &http.Client{Transport: certificateTestTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/client/v4/zones" {
				t.Fatal("cleanup rediscovered an acknowledged record's region")
			}
			status := 200
			body := `{"success":true,"result":[{"id":"zone","name":"example.test"}]}`
			if strings.Contains(r.URL.Path, "dns_records") {
				if r.Method == "DELETE" {
					parts := strings.Split(r.URL.Path, "/")
					recordID := parts[len(parts)-1]
					deletes[recordID]++
					if recordID == "second" && failures > 0 {
						failures--
						status = 503
					} else {
						delete(records, recordID)
					}
					body = `{"success":true,"result":{}}`
				} else {
					result := []map[string]string{}
					for recordID, value := range records {
						result = append(result, map[string]string{"id": recordID, "name": "_acme-challenge.shared.example.test", "content": value})
					}
					raw, _ := json.Marshal(map[string]any{"success": true, "result": result})
					body = string(raw)
				}
			}
			if r.URL.Path == "/client/v4/zones" && r.URL.Query().Get("name") != "example.test" {
				body = `{"success":true,"result":[]}`
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})}
	}
	state := certificateOrderState{FailureStage: "validating", Challenges: []certificateChallengeState{
		{Public: core.CertificateChallenge{Name: "_acme-challenge.shared.example.test", Value: "value-a"}, Record: certificates.DNSRecord{ID: "first", Zone: "zone"}},
		{Public: core.CertificateChallenge{Name: "_acme-challenge.shared.example.test", Value: "value-b"}, Record: certificates.DNSRecord{ID: "second", Zone: "zone"}},
	}}
	sealed, err := db.sealCertificate(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.pool.Exec(ctx, `UPDATE certificate_orders SET status='failed',stage='cleanup',secret=$2,attempt=8,error='original validation failure',next_attempt=now() WHERE id=$1`, id, sealed); err != nil {
		t.Fatal(err)
	}
	configure(db)
	if err = db.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithConfigKey(ctx, database, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	configure(reopened)
	for i := 0; i < 2; i++ {
		if _, err = db.pool.Exec(ctx, `UPDATE certificate_orders SET next_attempt=now() WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if err = reopened.RunCertificateMaintenance(ctx); err != nil {
			t.Fatal(err)
		}
	}
	orders, err = reopened.CertificateOrders(owner, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := orders[0]
	if got.Status != "failed" || got.Stage != "complete" || got.Attempt != 8 || got.Error != "original validation failure" || got.CleanupError != "" || got.NextAttempt != nil {
		t.Fatalf("restart outcome: %+v", got)
	}
	if len(records) != 1 || records["peer"] != "value-a" || deletes["first"] != 1 || deletes["second"] != 3 || deletes["peer"] != 0 {
		t.Fatalf("cleanup escaped receipts: records=%v deletes=%v", records, deletes)
	}
	var versions int
	if err = db.pool.QueryRow(ctx, `SELECT count(*) FROM certificate_versions WHERE certificate_id=$1`, item.ID).Scan(&versions); err != nil || versions != 0 {
		t.Fatalf("cleanup saved certificates: %d %v", versions, err)
	}
}

func TestCertificateHTTPRetryRetainsRoutesAndOfflineIsTemporary(t *testing.T) {
	for _, offline := range []bool{false, true} {
		name := "CA request failure"
		if offline {
			name = "node offline"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			_, owner := sharedTestUser(t, db, ctx, "http-owner")
			recipient, shared := sharedTestUser(t, db, ctx, "http-recipient")
			agent := sharedTestAgent(t, db, owner)
			if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features || '["panel-certificates-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
				t.Fatal(err)
			}
			domains := []string{"shared.example.test"}
			current, err := db.AgentSharing(owner, agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			sharing, err := db.SetAgentSharing(owner, agent.ID, core.AgentSharingRequest{Revision: current.Revision, Shares: []core.AgentSharingRecipient{{Username: recipient.Username, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}, HTTPDomains: &domains}}})
			if err != nil {
				t.Fatal(err)
			}
			sh := sharing.Shares[0]
			if _, err = db.RespondAgentShare(shared, sh.ID, core.AgentShareResponseRequest{Revision: sh.InvitationRevision, Decision: "accept"}); err != nil {
				t.Fatal(err)
			}
			ca := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "isolated CA request rejected", 400) }))
			defer ca.Close()
			old := certificates.Directories["letsencrypt-staging"]
			certificates.Directories["letsencrypt-staging"] = ca.URL
			defer func() { certificates.Directories["letsencrypt-staging"] = old }()
			_, key, err := certificates.NewKey("ecdsa256")
			if err != nil {
				t.Fatal(err)
			}
			type target struct {
				ctx     context.Context
				item    core.Certificate
				orderID string
			}
			targets := []target{}
			for i, scope := range []context.Context{owner, shared} {
				account, err := db.CreateCertificateAccount(scope, core.CertificateAccount{Name: "isolated CA", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{Key: key})
				if err != nil {
					t.Fatal(err)
				}
				domain := "owner.example.test"
				if i == 1 {
					domain = domains[0]
				}
				item, err := db.CreateACMECertificate(scope, core.Certificate{Name: "HTTP retry", Domains: []string{domain}, Method: "http-01", AgentID: agent.ID, AccountID: account.ID, KeyType: "ecdsa256"})
				if err != nil {
					t.Fatal(err)
				}
				orders, err := db.CertificateOrders(scope, item.ID)
				if err != nil {
					t.Fatal(err)
				}
				id := orders[0].ID
				if _, err = db.pool.Exec(ctx, `INSERT INTO certificate_http_challenges(order_id,agent_id,domain,token,value,expires_at) VALUES($1,$2,$3,$4,'local-proof',now()+interval '15 minutes')`, id, agent.ID, domain, fmt.Sprintf("local-token-%d", i)); err != nil {
					t.Fatal(err)
				}
				targets = append(targets, target{scope, item, id})
			}
			if offline {
				if _, err = db.pool.Exec(ctx, `UPDATE agents SET last_seen=now()-interval '2 minutes' WHERE id=$1`, agent.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err = db.RunCertificateMaintenance(ctx); err != nil {
				t.Fatal(err)
			}
			for _, target := range targets {
				orders, err := db.CertificateOrders(target.ctx, target.item.ID)
				if err != nil {
					t.Fatal(err)
				}
				got := orders[0]
				if got.Status != "queued" || got.Stage != "retry" || got.Attempt != 1 || got.NextAttempt == nil || !got.NextAttempt.After(time.Now()) {
					t.Fatalf("temporary failure terminated HTTP order: %+v", got)
				}
			}
			routes, err := db.AgentCertificateChallenges(ctx, agent.ID)
			if err != nil || len(routes) != 2 {
				t.Fatalf("retry removed CA validation routes: %d %v", len(routes), err)
			}
			expectedAttempts := 1
			if offline {
				if _, err = db.pool.Exec(ctx, `UPDATE agents SET last_seen=now() WHERE id=$1`, agent.ID); err != nil {
					t.Fatal(err)
				}
				for _, target := range targets {
					if _, err = db.pool.Exec(ctx, `UPDATE certificate_orders SET next_attempt=now() WHERE id=$1`, target.orderID); err != nil {
						t.Fatal(err)
					}
				}
				if err = db.RunCertificateMaintenance(ctx); err != nil {
					t.Fatal(err)
				}
				expectedAttempts = 2
				for _, target := range targets {
					orders, err := db.CertificateOrders(target.ctx, target.item.ID)
					if err != nil || orders[0].Status != "queued" || orders[0].Attempt != expectedAttempts {
						t.Fatalf("reconnection did not resume: %+v %v", orders, err)
					}
				}
				if _, err = db.pool.Exec(ctx, `UPDATE agents SET last_seen=now()-interval '2 minutes' WHERE id=$1`, agent.ID); err != nil {
					t.Fatal(err)
				}
			}
			// Revocation remains terminal even while the node is offline, and clears
			// only this recipient's route. The owner's retry must survive.
			if err = db.SetCertificateHTTPGrant(owner, core.HTTPChallengeGrant{AgentID: agent.ID, UserID: recipient.ID, Domain: domains[0]}, true); err != nil {
				t.Fatal(err)
			}
			if _, err = db.pool.Exec(ctx, `UPDATE certificate_orders SET next_attempt=now() WHERE id=$1`, targets[1].orderID); err != nil {
				t.Fatal(err)
			}
			if err = db.RunCertificateMaintenance(ctx); err != nil {
				t.Fatal(err)
			}
			orders, err := db.CertificateOrders(shared, targets[1].item.ID)
			if err != nil || orders[0].Status != "failed" {
				t.Fatalf("revoked authority retried: %+v %v", orders, err)
			}
			routes, err = db.AgentCertificateChallenges(ctx, agent.ID)
			if err != nil || len(routes) != 1 || routes[0].OrderID != targets[0].orderID {
				t.Fatalf("revocation crossed owners: %+v %v", routes, err)
			}
			if err = db.ContinueCertificateOrder(owner, targets[0].item.ID, targets[0].orderID, true); err != nil {
				t.Fatal(err)
			}
			if err = db.RunCertificateMaintenance(ctx); err != nil {
				t.Fatal(err)
			}
			routes, err = db.AgentCertificateChallenges(ctx, agent.ID)
			if err != nil || len(routes) != 0 {
				t.Fatal("canceled challenge remained available")
			}
			orders, err = db.CertificateOrders(owner, targets[0].item.ID)
			if err != nil || orders[0].Status != "canceled" || orders[0].Attempt != expectedAttempts {
				t.Fatalf("cancellation counted as issuance: %+v %v", orders, err)
			}
		})
	}
}

func TestCertificateResumesCAOutcomeWithoutRepublishingOrRefinalizing(t *testing.T) {
	for _, scenario := range []string{"processing", "missing certificate URL", "download response lost", "CA temporarily unavailable", "CA rate limited", "invalid order"} {
		t.Run(scenario, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			owner := WithConfigScope(ctx, "token_ca_recovery", false)
			pair, err := certificates.SelfSigned([]string{"resume.example.test"}, "ecdsa256", 90)
			if err != nil {
				t.Fatal(err)
			}
			var caURL string
			orderReads, finalizes, authReads, downloads := 0, 0, 0, 0
			ca := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Replay-Nonce", "local-fixture-nonce")
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/dir":
					fmt.Fprintf(w, `{"newNonce":%q,"newAccount":%q,"newOrder":%q}`, caURL+"/nonce", caURL+"/acct", caURL+"/new-order")
				case "/nonce":
					w.WriteHeader(200)
				case "/order":
					orderReads++
					if orderReads == 1 && (scenario == "CA temporarily unavailable" || scenario == "CA rate limited") {
						status := http.StatusServiceUnavailable
						problem := "serverInternal"
						if scenario == "CA rate limited" {
							status = http.StatusTooManyRequests
							problem = "rateLimited"
						}
						w.Header().Set("Retry-After", "1")
						w.WriteHeader(status)
						fmt.Fprintf(w, `{"type":"urn:ietf:params:acme:error:%s","detail":"local fixture retry"}`, problem)
						return
					}
					status := "valid"
					link := `,"certificate":"` + caURL + `/cert"`
					if scenario == "invalid order" {
						status = "invalid"
						link = ""
					}
					if scenario == "processing" && orderReads == 1 {
						status = "processing"
						link = ""
					}
					if scenario == "missing certificate URL" {
						link = ""
					}
					fmt.Fprintf(w, `{"status":%q,"authorizations":[%q],"finalize":%q%s}`, status, caURL+"/auth", caURL+"/finalize", link)
				case "/cert":
					downloads++
					if scenario == "download response lost" && downloads == 1 {
						http.Error(w, "fixture response loss", 400)
						return
					}
					w.Header().Set("Content-Type", "application/pem-certificate-chain")
					fmt.Fprint(w, pair.Certificate)
				case "/finalize":
					finalizes++
					http.Error(w, "must not finalize again", 400)
				default:
					authReads++
					http.Error(w, "must not read old challenge", 400)
				}
			}))
			defer ca.Close()
			caURL = ca.URL
			old := certificates.Directories["letsencrypt-staging"]
			certificates.Directories["letsencrypt-staging"] = caURL + "/dir"
			defer func() { certificates.Directories["letsencrypt-staging"] = old }()
			_, key, err := certificates.NewKey("ecdsa256")
			if err != nil {
				t.Fatal(err)
			}
			account, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "recovery", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{Key: key, URL: caURL + "/acct/1"})
			if err != nil {
				t.Fatal(err)
			}
			dns, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "DNS", Kind: "dns", Provider: "cloudflare"}, certificates.DNSCredentials{Token: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "recovery", Domains: []string{"resume.example.test"}, Method: "dns-api", KeyType: "ecdsa256", AccountID: account.ID, DNSAccountID: dns.ID})
			if err != nil {
				t.Fatal(err)
			}
			orders, _ := db.CertificateOrders(owner, item.ID)
			id := orders[0].ID
			state := certificateOrderState{URL: caURL + "/order", Key: pair.PrivateKey, Challenges: []certificateChallengeState{{URL: caURL + "/challenge", AuthURL: caURL + "/auth", Public: core.CertificateChallenge{Name: "_acme-challenge.resume.example.test", Value: "old-proof", Status: "pending"}}}}
			if err = db.saveCertificateOrderState(owner, id, state, "finalizing"); err != nil {
				t.Fatal(err)
			}
			db.certificateDNSHTTP = &http.Client{Transport: certificateTestTransport(func(*http.Request) (*http.Response, error) {
				t.Error("republished DNS after CA progressed")
				return nil, errors.New("unexpected DNS call")
			})}
			err = db.processCertificateOrder(owner, item, id, "queued", state)
			if scenario == "invalid order" {
				if !errors.Is(err, errCertificateOrderInvalid) || downloads != 0 {
					t.Fatalf("invalid order did not stop safely: %v downloads=%d", err, downloads)
				}
			} else if scenario == "missing certificate URL" {
				if err == nil {
					t.Fatal("missing link claimed success")
				}
			} else {
				if scenario == "download response lost" {
					if err == nil {
						t.Fatal("expected first download failure")
					}
					err = db.processCertificateOrder(owner, item, id, "queued", state)
				}
				if err != nil {
					t.Fatal(err)
				}
				issued, err := db.GetCertificate(owner, item.ID)
				if err != nil || issued.Version != 1 {
					t.Fatalf("certificate not saved: %+v %v", issued, err)
				}
			}
			if finalizes != 0 || authReads != 0 {
				t.Fatalf("replayed CA operations: finalize=%d auth=%d", finalizes, authReads)
			}
		})
	}
}
