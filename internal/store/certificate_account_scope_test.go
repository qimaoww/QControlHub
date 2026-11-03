package store

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCertificateWorkerUsesDatabaseOwner(t *testing.T) {
	for _, tc := range []struct{ name, owner, metadataOwner string }{
		{"legacy administrator", "", ""},
		{"missing metadata owner", "token_owner", ""},
		{"foreign metadata owner", "token_owner", "token_foreign"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, ctx, database := isolatedConfigScopeStore(t)
			owner := certificateOwnerContext(ctx, tc.owner)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "local CA unavailable", 400) }))
			defer server.Close()
			old := certificates.Directories["letsencrypt-staging"]
			certificates.Directories["letsencrypt-staging"] = server.URL
			defer func() { certificates.Directories["letsencrypt-staging"] = old }()
			_, key, err := certificates.NewKey("ecdsa256")
			if err != nil {
				t.Fatal(err)
			}
			account, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "same name", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{Key: key})
			if err != nil {
				t.Fatal(err)
			}
			foreign := certificateOwnerContext(ctx, "token_foreign")
			_, err = db.CreateCertificateAccount(foreign, core.CertificateAccount{Name: "same name", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{Key: "invalid foreign key"})
			if err != nil {
				t.Fatal(err)
			}
			item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "scope regression", Domains: []string{"scope.example.test"}, Method: "dns-manual", AccountID: account.ID, KeyType: "ecdsa256"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.pool.Exec(ctx, `UPDATE panel_certificates SET metadata=jsonb_set(metadata,'{owner_id}',to_jsonb($2::text)) WHERE id=$1`, item.ID, tc.metadataOwner)
			if err != nil {
				t.Fatal(err)
			}
			restarted, err := OpenWithConfigKey(ctx, database, true, testEncryptionKey("config-scope"))
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			for attempt := 1; attempt <= 2; attempt++ {
				if err = restarted.RunCertificateMaintenance(ctx); err != nil {
					t.Fatal(err)
				}
				orders, err := db.CertificateOrders(owner, item.ID)
				if err != nil || len(orders) != 1 || orders[0].Status != "queued" || orders[0].Stage != "retry" || orders[0].Attempt != attempt || strings.Contains(orders[0].Error, "账号读取") || calls.Load() != int32(attempt) {
					t.Fatalf("owner scope did not reach local CA: %+v calls=%d err=%v", orders, calls.Load(), err)
				}
				_, err = db.pool.Exec(ctx, `UPDATE certificate_orders SET next_attempt=now() WHERE certificate_id=$1`, item.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = db.RenewCertificate(foreign, item.ID); err == nil {
				t.Fatal("foreign renewal allowed")
			}
		})
	}
}

func TestCertificateAccountReadClassificationAndIsolation(t *testing.T) {
	db, ctx, database := isolatedConfigScopeStore(t)
	owner := certificateOwnerContext(ctx, "token_accounts")
	other := certificateOwnerContext(ctx, "token_other")
	account, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "same", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{Key: "secret fixture"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.CreateCertificateAccount(other, core.CertificateAccount{Name: "same", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{Key: "other fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var value certificates.ACMEAccount
	for _, tc := range []struct {
		name, id, kind string
		scope          context.Context
		message        string
		permanent      bool
	}{
		{"missing reference", "", "acme", owner, "引用缺失", true},
		{"absent", "cac_missing", "acme", owner, "不存在或无权访问", true},
		{"foreign", account.ID, "acme", other, "不存在或无权访问", true},
		{"wrong type", account.ID, "dns", owner, "类型不匹配", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.CertificateAccountSecret(tc.scope, tc.id, tc.kind, &value)
			var classified *certificateAccountReadError
			if !errors.As(err, &classified) || classified.permanent != tc.permanent || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("classification: %v", err)
			}
		})
	}
	// Relational identity is canonical for both selector and secret reads.
	_, err = db.pool.Exec(ctx, `UPDATE certificate_accounts SET metadata=metadata || '{"id":"stale","kind":"dns"}'::jsonb WHERE id=$1`, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := db.ListCertificateAccounts(owner)
	if err != nil || len(listed) != 1 || listed[0].ID != account.ID || listed[0].Kind != "acme" {
		t.Fatalf("selector mismatch: %+v %v", listed, err)
	}
	got, err := db.CertificateAccountSecret(owner, account.ID, "acme", &value)
	if err != nil || got.ID != account.ID || value.Key != "secret fixture" {
		t.Fatalf("read mismatch: %+v %v", got, err)
	}
	rotated, err := OpenWithConfigKeyring(ctx, database, true, testEncryptionKey("account-new"), []string{testEncryptionKey("config-scope")})
	if err != nil {
		t.Fatal(err)
	}
	defer rotated.Close()
	if _, err = rotated.CertificateAccountSecret(owner, account.ID, "acme", &value); err != nil || value.Key != "secret fixture" {
		t.Fatalf("old account key: %v", err)
	}
	var original string
	if err = db.pool.QueryRow(ctx, `SELECT secret FROM certificate_accounts WHERE id=$1`, account.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	corruptJSON, err := db.encryptContent(`{"key":`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, secret, message string }{
		{"format", "invalid", "密文格式损坏"},
		{"decrypt", original[:len(original)-4] + "AAAA", "解密失败"},
		{"secret JSON", corruptJSON, "秘密数据损坏"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.pool.Exec(ctx, `UPDATE certificate_accounts SET secret=$2 WHERE id=$1`, account.ID, tc.secret)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.CertificateAccountSecret(owner, account.ID, "acme", &value)
			var classified *certificateAccountReadError
			if !errors.As(err, &classified) || !classified.permanent || !strings.Contains(err.Error(), tc.message) || strings.Contains(err.Error(), "secret fixture") {
				t.Fatalf("classification: %v", err)
			}
		})
	}
	_, err = db.pool.Exec(ctx, `UPDATE certificate_accounts SET secret=$2,metadata='[]'::jsonb WHERE id=$1`, account.ID, original)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.CertificateAccountSecret(owner, account.ID, "acme", &value)
	if err == nil || !strings.Contains(err.Error(), "数据损坏") {
		t.Fatal(err)
	}
	_, err = db.pool.Exec(ctx, `UPDATE certificate_accounts SET metadata='{}'::jsonb WHERE id=$1`, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	cryptor := db.cryptor
	db.cryptor = nil
	_, err = db.CertificateAccountSecret(owner, account.ID, "acme", &value)
	db.cryptor = cryptor
	if err == nil || !strings.Contains(err.Error(), "密钥未配置") {
		t.Fatal(err)
	}
	// Real PostgreSQL lock timeout is retryable; database details stay out of Error().
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `LOCK certificate_accounts IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(owner, 50*time.Millisecond)
	defer cancel()
	_, err = db.CertificateAccountSecret(short, account.ID, "acme", &value)
	var classified *certificateAccountReadError
	if !errors.As(err, &classified) || classified.permanent || !strings.Contains(err.Error(), "稍后重试") {
		t.Fatalf("database failure: %v", err)
	}
	databaseFailure := err
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "database recovery", Domains: []string{"recovery.example.test"}, Method: "dns-manual", KeyType: "ecdsa256", AccountID: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	orders, err := db.CertificateOrders(owner, item.ID)
	if err != nil || len(orders) != 1 {
		t.Fatalf("orders: %+v %v", orders, err)
	}
	if err = db.recordCertificateOrderFailure(ctx, orders[0].ID, item.Method, "queued", 0, databaseFailure); err != nil {
		t.Fatal(err)
	}
	orders, err = db.CertificateOrders(owner, item.ID)
	if err != nil || orders[0].Status != "queued" || orders[0].Stage != "retry" || orders[0].NextAttempt == nil || !orders[0].NextAttempt.After(time.Now()) {
		t.Fatalf("database failure did not schedule retry: %+v %v", orders, err)
	}
}

func TestCertificateAccountFailureStopsRetryAndPreservesCause(t *testing.T) {
	for _, reference := range []string{"", "cac_missing", "dns", "foreign"} {
		t.Run("reference_"+reference, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			owner := certificateOwnerContext(ctx, "token_failure")
			account, err := db.CreateCertificateAccount(owner, core.CertificateAccount{Name: "valid account", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{Key: "unused key"})
			if err != nil {
				t.Fatal(err)
			}
			item, err := db.CreateACMECertificate(owner, core.Certificate{Name: "failure fixture", Domains: []string{"failure.example.test"}, Method: "dns-manual", KeyType: "ecdsa256", AccountID: account.ID})
			if err != nil {
				t.Fatal(err)
			}
			target := reference
			if reference == "dns" || reference == "foreign" {
				scope := owner
				kind := "dns"
				if reference == "foreign" {
					scope = certificateOwnerContext(ctx, "token_foreign")
					kind = "acme"
				}
				other, err := db.CreateCertificateAccount(scope, core.CertificateAccount{Name: "same name", Kind: kind}, map[string]string{})
				if err != nil {
					t.Fatal(err)
				}
				target = other.ID
			}
			if _, err = db.pool.Exec(ctx, `UPDATE panel_certificates SET metadata=jsonb_set(metadata,'{account_id}',to_jsonb($2::text)) WHERE id=$1`, item.ID, target); err != nil {
				t.Fatal(err)
			}
			if err = db.RunCertificateMaintenance(ctx); err != nil {
				t.Fatal(err)
			}
			orders, err := db.CertificateOrders(owner, item.ID)
			if err != nil || len(orders) != 1 || orders[0].Status != "failed" || orders[0].Stage != "cleanup" || orders[0].Attempt != 1 || !strings.Contains(orders[0].Error, "申请账号") {
				t.Fatalf("permanent failure retried: %+v %v", orders, err)
			}
			cause := orders[0].Error
			if err = db.RunCertificateMaintenance(ctx); err != nil {
				t.Fatal(err)
			}
			orders, err = db.CertificateOrders(owner, item.ID)
			if err != nil || orders[0].Status != "failed" || orders[0].Stage != "complete" || orders[0].Attempt != 1 || orders[0].Error != cause {
				t.Fatalf("cleanup replaced original failure: %+v %v", orders, err)
			}
		})
	}
}
