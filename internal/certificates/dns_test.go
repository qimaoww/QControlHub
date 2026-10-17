package certificates

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDNSProvidersCreateAndDeleteOnlyReturnedRecord(t *testing.T) {
	for _, provider := range []string{"cloudflare", "alidns", "dnspod"} {
		t.Run(provider, func(t *testing.T) {
			created, deleted := false, false
			c := DNSClient{Provider: provider, Credentials: DNSCredentials{Zone: "example.test", Token: "fixture-token", AccessKeyID: "fixture-id", AccessKeySecret: "fixture-secret", SecretID: "fixture-id", SecretKey: "fixture-secret"}, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := ""
				switch provider {
				case "cloudflare":
					if r.Header.Get("Authorization") != "Bearer fixture-token" {
						t.Fatal("missing token")
					}
					if r.Method == "GET" {
						body = `{"success":true,"result":[{"id":"zone-1"}]}`
					} else if r.Method == "POST" {
						raw, _ := io.ReadAll(r.Body)
						if !strings.Contains(string(raw), `"type":"TXT"`) || !strings.Contains(string(raw), "challenge-value") {
							t.Fatal("incorrect TXT payload")
						}
						created = true
						body = `{"success":true,"result":{"id":"record-1"}}`
					} else {
						if r.URL.Path != "/client/v4/zones/zone-1/dns_records/record-1" {
							t.Fatalf("deleted %s", r.URL.Path)
						}
						deleted = true
						body = `{"success":true,"result":{}}`
					}
				case "alidns":
					q := r.URL.Query()
					if q.Get("Signature") == "" || q.Get("SignatureNonce") == "" {
						t.Fatal("unsigned Alibaba request")
					}
					if q.Get("Action") == "AddDomainRecord" {
						if q.Get("RR") != "_acme-challenge" || q.Get("Value") != "challenge-value" {
							t.Fatal("bad Alibaba record")
						}
						created = true
						body = `{"RecordId":"record-1"}`
					} else {
						if q.Get("RecordId") != "record-1" {
							t.Fatal("wrong Alibaba cleanup")
						}
						deleted = true
						body = `{"RequestId":"ok"}`
					}
				case "dnspod":
					if !strings.HasPrefix(r.Header.Get("Authorization"), "TC3-HMAC-SHA256 ") {
						t.Fatal("unsigned Tencent request")
					}
					raw, _ := io.ReadAll(r.Body)
					if r.Header.Get("X-TC-Action") == "CreateRecord" {
						if !strings.Contains(string(raw), "challenge-value") {
							t.Fatal("bad DNSPod record")
						}
						created = true
						body = `{"Response":{"RecordId":123}}`
					} else {
						if !strings.Contains(string(raw), `"RecordId":123`) {
							t.Fatal("wrong DNSPod cleanup")
						}
						deleted = true
						body = `{"Response":{}}`
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
			})}}
			record, err := c.Create(context.Background(), "_acme-challenge.example.test", "challenge-value")
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Delete(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			if !created || !deleted {
				t.Fatal("missing lifecycle")
			}
			if _, err = c.Create(context.Background(), "_acme-challenge.other.test", "value"); err == nil {
				t.Fatal("cross-zone write")
			}
		})
	}
}
