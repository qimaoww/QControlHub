package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Minimal local ACME fixture. It exercises directory discovery, account/order
// persistence, HTTP-01 presentation, CSR finalization and certificate download.
// JWS signature validation belongs to the CA; this fixture checks client flow.
func mockTLSACME(t *testing.T, domain string, challengePort int) (*httptest.Server, []byte, *atomic.Int32) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "QCH local ACME CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	var mutex sync.Mutex
	authorized := false
	var certificate string
	orders := &atomic.Int32{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		w.Header().Set("Replay-Nonce", base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprint(time.Now().UnixNano()))))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", "<"+server.URL+"/directory>;rel=\"index\"")
		var envelope struct {
			Payload string `json:"payload"`
		}
		_ = json.NewDecoder(r.Body).Decode(&envelope)
		payload, _ := base64.RawURLEncoding.DecodeString(envelope.Payload)
		order := func(status string) map[string]any {
			value := map[string]any{"status": status, "identifiers": []any{map[string]any{"type": "dns", "value": domain}}, "authorizations": []string{server.URL + "/authz"}, "finalize": server.URL + "/finalize"}
			if certificate != "" {
				value["certificate"] = server.URL + "/certificate"
			}
			return value
		}
		var value any
		switch r.URL.Path {
		case "/directory":
			value = map[string]any{"newNonce": server.URL + "/nonce", "newAccount": server.URL + "/account", "newOrder": server.URL + "/new-order"}
		case "/nonce":
			w.WriteHeader(200)
			return
		case "/account":
			w.Header().Set("Location", server.URL+"/account/1")
			w.WriteHeader(201)
			value = map[string]any{"status": "valid", "contact": []string{"mailto:certs@example.test"}, "orders": server.URL + "/orders"}
		case "/new-order":
			orders.Add(1)
			w.Header().Set("Location", server.URL+"/order")
			w.WriteHeader(201)
			value = order("pending")
		case "/authz":
			status := "pending"
			if authorized {
				status = "valid"
			}
			value = map[string]any{"status": status, "identifier": map[string]any{"type": "dns", "value": domain}, "challenges": []any{map[string]any{"type": "http-01", "status": status, "url": server.URL + "/challenge", "token": "qch-local-acme-challenge"}}}
		case "/challenge":
			request, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/.well-known/acme-challenge/qch-local-acme-challenge", challengePort), nil)
			request.Host = domain
			client := &http.Client{Timeout: 2 * time.Second}
			response, err := client.Do(request)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 200 || !strings.HasPrefix(string(data), "qch-local-acme-challenge.") {
				http.Error(w, "HTTP-01 key authorization not served", 500)
				return
			}
			authorized = true
			value = map[string]any{"type": "http-01", "url": server.URL + "/challenge", "status": "valid", "token": "qch-local-acme-challenge"}
		case "/finalize":
			var body struct {
				CSR string `json:"csr"`
			}
			_ = json.Unmarshal(payload, &body)
			data, _ := base64.RawURLEncoding.DecodeString(body.CSR)
			csr, err := x509.ParseCertificateRequest(data)
			if err != nil || csr.CheckSignature() != nil || !authorized {
				http.Error(w, "invalid CSR or challenge", 400)
				return
			}
			leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: csr.DNSNames, Subject: csr.Subject, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			der, err := x509.CreateCertificate(rand.Reader, leaf, ca, csr.PublicKey, key)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) + string(caPEM)
			w.Header().Set("Location", server.URL+"/order")
			value = order("valid")
		case "/order":
			status := "ready"
			if certificate != "" {
				status = "valid"
			}
			value = order(status)
		case "/certificate":
			w.Header().Set("Content-Type", "application/pem-certificate-chain")
			fmt.Fprint(w, certificate)
			return
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	t.Cleanup(server.Close)
	return server, caPEM, orders
}
