package certificates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func dnsResponse(body any) *http.Response {
	raw, _ := json.Marshal(body)
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}
}
func providerCredentials(provider string) DNSCredentials {
	switch provider {
	case "cloudflare":
		return DNSCredentials{Token: "fixture-token"}
	case "alidns":
		return DNSCredentials{AccessKeyID: "fixture-id", AccessKeySecret: "fixture-secret"}
	default:
		return DNSCredentials{SecretID: "fixture-id", SecretKey: "fixture-secret"}
	}
}

func TestDNSPodReceiptVerification(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		match, failure bool
	}{
		{"present", `{"RecordInfo":{"Id":42,"SubDomain":"_acme-challenge","RecordType":"TXT","Value":"proof"}}`, true, false},
		{"absent", `{"Error":{"Code":"InvalidParameter.RecordIdInvalid"}}`, false, false},
		{"changed value", `{"RecordInfo":{"Id":42,"SubDomain":"_acme-challenge","RecordType":"TXT","Value":"peer"}}`, false, false},
		{"changed name", `{"RecordInfo":{"Id":42,"SubDomain":"peer","RecordType":"TXT","Value":"proof"}}`, false, false},
		{"changed type", `{"RecordInfo":{"Id":42,"SubDomain":"_acme-challenge","RecordType":"CNAME","Value":"proof"}}`, false, false},
		{"changed id", `{"RecordInfo":{"Id":43,"SubDomain":"_acme-challenge","RecordType":"TXT","Value":"proof"}}`, false, false},
		{"permission", `{"Error":{"Code":"OperationDenied.NoPermissionToOperateDomain"}}`, false, true},
		{"zone unavailable", `{"Error":{"Code":"InvalidParameterValue.DomainNotExists"}}`, false, true},
		{"rate limit", `{"Error":{"Code":"RequestLimitExceeded.RequestLimitExceeded"}}`, false, true},
		{"missing response", `{}`, false, true},
		{"null response", `{"RecordInfo":null}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := DNSClient{Provider: "dnspod", Credentials: providerCredentials("dnspod")}
			client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var input struct {
					Domain   string
					RecordID uint64 `json:"RecordId"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Fatal(err)
				}
				if r.Header.Get("X-TC-Action") != "DescribeRecord" || input.Domain != "example.test" || input.RecordID != 42 {
					t.Fatal("receipt verification relied on delayed listing or another zone/record")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"Response":` + tc.response + `}`))}, nil
			})}
			match, err := client.Matches(context.Background(), DNSRecord{"42", "example.test"}, "_acme-challenge.example.test.", "proof")
			if match != tc.match || (err != nil) != tc.failure {
				t.Fatalf("match=%v err=%v", match, err)
			}
			if tc.name == "permission" && !strings.Contains(err.Error(), "权限不足") {
				t.Fatalf("permission diagnostic: %v", err)
			}
		})
	}
}

func TestDNSProvidersWithoutAccountZone(t *testing.T) {
	for _, provider := range []string{"cloudflare", "alidns", "dnspod"} {
		t.Run(provider, func(t *testing.T) {
			zones := map[string]string{"example.co.uk": "zone-1", "delegated.example.co.uk": "zone-2", "other.test": "zone-3"}
			created, deleted, checks := 0, 0, 0
			client := DNSClient{Provider: provider, Credentials: providerCredentials(provider)}
			client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch provider {
				case "cloudflare":
					if r.Header.Get("Authorization") != "Bearer fixture-token" {
						t.Fatal("missing token")
					}
					q := r.URL.Query()
					if r.Method == "GET" && r.URL.Path == "/client/v4/zones" {
						if q.Get("name") == "" {
							if q.Get("per_page") != "1" {
								t.Fatal("unbounded zone listing")
							}
							checks++
							return dnsResponse(map[string]any{"success": true, "result": []any{}}), nil
						}
						result := []any{}
						if id := zones[q.Get("name")]; id != "" {
							result = append(result, map[string]string{"id": id, "name": q.Get("name")})
						}
						return dnsResponse(map[string]any{"success": true, "result": result}), nil
					}
					if r.Method == "POST" {
						var input struct{ Type, Name, Content string }
						_ = json.NewDecoder(r.Body).Decode(&input)
						if input.Type != "TXT" || input.Content != "proof" {
							t.Fatal("bad TXT")
						}
						created++
						return dnsResponse(map[string]any{"success": true, "result": map[string]string{"id": fmt.Sprint(created)}}), nil
					}
					if r.Method == "DELETE" {
						deleted++
						return dnsResponse(map[string]any{"success": true, "result": map[string]string{}}), nil
					}
				case "alidns":
					q := r.URL.Query()
					if q.Get("Signature") == "" || q.Get("SignatureNonce") == "" {
						t.Fatal("unsigned request")
					}
					switch q.Get("Action") {
					case "DescribeDomains":
						if q.Get("DomainName") != "" {
							t.Fatal("check requires domain")
						}
						checks++
						return dnsResponse(map[string]any{}), nil
					case "DescribeDomainInfo":
						if zones[q.Get("DomainName")] == "" {
							return dnsResponse(map[string]string{"Code": "InvalidDomainName.NoExist"}), nil
						}
						return dnsResponse(map[string]string{"DomainName": q.Get("DomainName")}), nil
					case "AddDomainRecord":
						if q.Get("Type") != "TXT" || q.Get("Value") != "proof" {
							t.Fatal("bad TXT")
						}
						created++
						return dnsResponse(map[string]string{"RecordId": fmt.Sprint(created)}), nil
					case "DeleteDomainRecord":
						deleted++
						return dnsResponse(map[string]any{}), nil
					}
				case "dnspod":
					if !strings.HasPrefix(r.Header.Get("Authorization"), "TC3-HMAC-SHA256 ") {
						t.Fatal("unsigned request")
					}
					var q map[string]any
					_ = json.NewDecoder(r.Body).Decode(&q)
					switch r.Header.Get("X-TC-Action") {
					case "DescribeDomainList":
						if q["Domain"] != nil {
							t.Fatal("check requires domain")
						}
						checks++
						return dnsResponse(map[string]any{"Response": map[string]any{}}), nil
					case "DescribeDomain":
						if zones[fmt.Sprint(q["Domain"])] == "" {
							return dnsResponse(map[string]any{"Response": map[string]any{"Error": map[string]string{"Code": "ResourceNotFound.DomainNotExists"}}}), nil
						}
						return dnsResponse(map[string]any{"Response": map[string]any{"DomainInfo": map[string]any{"Domain": q["Domain"]}}}), nil
					case "CreateRecord":
						if q["RecordType"] != "TXT" || q["Value"] != "proof" {
							t.Fatal("bad TXT")
						}
						created++
						return dnsResponse(map[string]any{"Response": map[string]int{"RecordId": created}}), nil
					case "DeleteRecord":
						deleted++
						return dnsResponse(map[string]any{"Response": map[string]any{}}), nil
					}
				}
				t.Fatalf("unexpected provider request %s %s", r.Method, r.URL.Path)
				return nil, nil
			})}
			if err := client.Validate(); err != nil {
				t.Fatal(err)
			}
			if err := client.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"example.co.uk", "node.example.co.uk", "a.b.example.co.uk", "delegated.example.co.uk", "node.delegated.example.co.uk", "other.test"} {
				target := "_acme-challenge." + name
				zone, err := client.Resolve(context.Background(), target)
				if err != nil {
					t.Fatal(err)
				}
				expected := "example.co.uk"
				if strings.HasSuffix(name, "delegated.example.co.uk") {
					expected = "delegated.example.co.uk"
				}
				if name == "other.test" {
					expected = name
				}
				if zone.Name != expected {
					t.Fatalf("%s resolved to %+v", name, zone)
				}
				// An obsolete account hint must never constrain another domain.
				client.Credentials.Zone = "obsolete.invalid"
				record, err := client.Create(context.Background(), target, "proof")
				if err != nil {
					t.Fatal(err)
				}
				if provider == "cloudflare" && record.Zone != zones[expected] {
					t.Fatal("wrong zone ID")
				}
				if provider != "cloudflare" && record.Zone != expected {
					t.Fatal("wrong zone")
				}
				if err = client.Delete(context.Background(), record); err != nil {
					t.Fatal(err)
				}
			}
			if created != 6 || deleted != 6 || checks != 1 {
				t.Fatalf("incomplete lifecycle %d/%d/%d", created, deleted, checks)
			}
			if _, err := client.Resolve(context.Background(), "_acme-challenge.notexample.co.uk"); err == nil {
				t.Fatal("near suffix matched")
			}
			if _, err := client.CreateInZone(context.Background(), DNSZone{"id", "example.co.uk"}, "_acme-challenge.notexample.co.uk", "proof"); err == nil {
				t.Fatal("cross-zone write")
			}
			client.Credentials = DNSCredentials{Token: "token-only"}
			if provider != "cloudflare" && client.Validate() == nil {
				t.Fatal("wrong credential type accepted")
			}
		})
	}
}

func TestDNSRecoveryPaginationAndCredentialIsolation(t *testing.T) {
	for _, provider := range []string{"cloudflare", "alidns", "dnspod"} {
		t.Run(provider, func(t *testing.T) {
			size := map[string]int{"cloudflare": 100, "alidns": 500, "dnspod": 3000}[provider]
			pages := 0
			client := DNSClient{Provider: provider, Credentials: providerCredentials(provider)}
			client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				pages++
				page := 0
				switch provider {
				case "cloudflare":
					page, _ = strconv.Atoi(r.URL.Query().Get("page"))
					if !strings.Contains(r.URL.Path, "/saved-zone/") {
						t.Fatal("rediscovered historical zone")
					}
				case "alidns":
					page, _ = strconv.Atoi(r.URL.Query().Get("PageNumber"))
					if r.URL.Query().Get("DomainName") != "saved-zone" {
						t.Fatal("wrong historical zone")
					}
				default:
					var p struct {
						Offset int
						Domain string
					}
					_ = json.NewDecoder(r.Body).Decode(&p)
					page = p.Offset/size + 1
					if p.Domain != "saved-zone" {
						t.Fatal("wrong historical zone")
					}
				}
				count := size
				if page == 2 {
					count = 1
				}
				if page > 2 {
					t.Fatal("pagination did not stop")
				}
				records := []any{}
				for i := 0; i < count; i++ {
					value := "peer"
					if page == 2 {
						value = "proof"
					}
					switch provider {
					case "cloudflare":
						records = append(records, map[string]string{"id": fmt.Sprintf("%d-%d", page, i), "name": "_acme-challenge.example.test", "content": value})
					case "alidns":
						records = append(records, map[string]string{"RecordId": fmt.Sprintf("%d-%d", page, i), "RR": "_acme-challenge.example.test", "Type": "TXT", "Value": value})
					default:
						records = append(records, map[string]any{"RecordId": page*size + i, "Name": "_acme-challenge.example.test", "Type": "TXT", "Value": value})
					}
				}
				switch provider {
				case "cloudflare":
					return dnsResponse(map[string]any{"success": true, "result": records}), nil
				case "alidns":
					return dnsResponse(map[string]any{"DomainRecords": map[string]any{"Record": records}}), nil
				default:
					return dnsResponse(map[string]any{"Response": map[string]any{"RecordList": records}}), nil
				}
			})}
			records, err := client.FindInZone(context.Background(), "saved-zone", "_acme-challenge.example.test", "proof")
			if err != nil || len(records) != 1 || pages != 2 {
				t.Fatalf("pagination %v %v pages=%d", records, err, pages)
			}
		})
	}
	// A denied child lookup must not fall through to a writable parent.
	requests := 0
	client := DNSClient{Provider: "cloudflare", Credentials: providerCredentials("cloudflare"), HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"secret":"never expose"}`))}, nil
	})}}
	_, err := client.Resolve(context.Background(), "node.example.test")
	var apiErr *DNSAPIError
	if !errors.As(err, &apiErr) || requests != 1 || !strings.Contains(err.Error(), "权限不足") || strings.Contains(err.Error(), "never expose") {
		t.Fatalf("unsafe failure %v requests=%d", err, requests)
	}
	for _, status := range []int{401, 429, 503} {
		client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})}
		if err := client.Check(context.Background()); err == nil {
			t.Fatal("provider failure ignored")
		}
	}
}

func TestCloudflareZonePaginationDoesNotSelectAmbiguousZone(t *testing.T) {
	pages := 0
	client := DNSClient{Provider: "cloudflare", Credentials: providerCredentials("cloudflare")}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		pages++
		if r.URL.Query().Get("name") != "example.test" || r.URL.Query().Get("page") != strconv.Itoa(pages) {
			t.Fatal("zone discovery lost its exact filter or page")
		}
		zones := []any{map[string]string{"id": fmt.Sprint(pages), "name": "example.test"}}
		if pages == 1 {
			// Ignore unrelated entries, but inspect the following page before
			// trusting a single exact match from the current page.
			for i := 1; i < 50; i++ {
				zones = append(zones, map[string]string{"id": "peer", "name": "notexample.test"})
			}
		}
		return dnsResponse(map[string]any{"success": true, "result": zones}), nil
	})}
	if _, err := client.Resolve(context.Background(), "example.test"); err == nil || !strings.Contains(err.Error(), "多个同名区域") || pages != 2 {
		t.Fatalf("unsafe paginated selection: pages=%d err=%v", pages, err)
	}
}

type targetResolver struct {
	canonical string
	delegated string
	temporary bool
}

func (r targetResolver) LookupCNAME(_ context.Context, name string) (string, error) {
	if r.temporary {
		return "", &net.DNSError{Err: "temporary", IsTemporary: true}
	}
	if r.canonical != "" {
		return r.canonical, nil
	}
	return name, nil
}
func (r targetResolver) LookupNS(_ context.Context, name string) ([]*net.NS, error) {
	if name == r.delegated || name == "example.test" || name == "target.test" {
		return []*net.NS{{Host: "ns.delegated.test."}}, nil
	}
	return nil, &net.DNSError{Err: "not found", IsNotFound: true}
}
func (r targetResolver) LookupTXT(context.Context, string) ([]string, error) { return nil, nil }
func TestDNSTargetDelegationAndCNAME(t *testing.T) {
	client := DNSClient{Provider: "cloudflare", Credentials: providerCredentials("cloudflare"), HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		result := []any{}
		name := r.URL.Query().Get("name")
		if name == "example.test" || name == "target.test" {
			result = append(result, map[string]string{"id": name + "-id", "name": name})
		}
		return dnsResponse(map[string]any{"success": true, "result": result}), nil
	})}}
	name, zone, err := client.Target(context.Background(), targetResolver{canonical: "_proof.target.test."}, "_acme-challenge.example.test")
	if err != nil || name != "_proof.target.test" || zone.Name != "target.test" {
		t.Fatalf("CNAME resolution: %s %+v %v", name, zone, err)
	}
	_, _, err = client.Target(context.Background(), targetResolver{delegated: "child.example.test"}, "_acme-challenge.child.example.test")
	if err == nil || !strings.Contains(err.Error(), "已委派") {
		t.Fatalf("parent would receive delegated challenge: %v", err)
	}
	if _, _, err = client.Target(context.Background(), targetResolver{temporary: true}, "_acme-challenge.example.test"); err == nil {
		t.Fatal("temporary DNS error ignored")
	}
}

func TestDNSConcurrentCredentialScopesAndPermissionChanges(t *testing.T) {
	// Same names under different credentials must never share a resolved ID.
	for _, token := range []string{"scope-a", "scope-b"} {
		t.Run(token, func(t *testing.T) {
			t.Parallel()
			currentID := token + "-zone"
			client := DNSClient{Provider: "cloudflare", Credentials: DNSCredentials{Token: token}, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("credential scope crossed")
				}
				if r.URL.Query().Get("name") == "example.test" {
					return dnsResponse(map[string]any{"success": true, "result": []any{map[string]string{"id": currentID, "name": "example.test"}}}), nil
				}
				return dnsResponse(map[string]any{"success": true, "result": []any{}}), nil
			})}}
			for i := 0; i < 3; i++ {
				zone, err := client.Resolve(context.Background(), "_acme-challenge.node.example.test")
				if err != nil || zone.ID != currentID {
					t.Fatalf("scope leaked %+v %v", zone, err)
				}
				currentID = token + "-changed"
			}
		})
	}
}
