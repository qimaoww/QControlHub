package authn

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSignAndVerifyRequest(t *testing.T) {
	t.Parallel()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Unix(1_750_000_000, 0).UTC()
	body := []byte(`{"runtime":{"mihomo":{"installed":true}}}`)
	request := httptest.NewRequest(http.MethodPost, "https://control.example/v1/agent/heartbeat?next=a%2Fb&limit=1", nil)

	if err := SignRequest(request, body, "agent_123", privateKey, now); err != nil {
		t.Fatalf("SignRequest() error = %v", err)
	}
	if got := request.Header.Get(HeaderAgentID); got != "agent_123" {
		t.Fatalf("agent header = %q, want agent_123", got)
	}
	nonce, err := VerifyRequest(request, body, publicKey, now)
	if err != nil {
		t.Fatalf("VerifyRequest() error = %v", err)
	}
	if nonce == "" || nonce != request.Header.Get(HeaderNonce) {
		t.Fatalf("verified nonce = %q, header nonce = %q", nonce, request.Header.Get(HeaderNonce))
	}

	// The acceptance window is inclusive at both boundaries.
	if _, err := VerifyRequest(request, body, publicKey, now.Add(MaxClockSkew)); err != nil {
		t.Fatalf("VerifyRequest() at positive clock-skew boundary: %v", err)
	}
	if _, err := VerifyRequest(request, body, publicKey, now.Add(-MaxClockSkew)); err != nil {
		t.Fatalf("VerifyRequest() at negative clock-skew boundary: %v", err)
	}
}

func TestVerifyRequestRejectsTamperingAndExpiredSignatures(t *testing.T) {
	t.Parallel()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	otherPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate alternate key: %v", err)
	}
	now := time.Unix(1_750_000_000, 0).UTC()
	body := []byte(`{"ok":true}`)
	newSignedRequest := func(t *testing.T) *http.Request {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "https://control.example/v1/tasks?cursor=abc", nil)
		if err := SignRequest(request, body, "agent_123", privateKey, now); err != nil {
			t.Fatalf("SignRequest() error = %v", err)
		}
		return request
	}

	tests := []struct {
		name      string
		mutate    func(*http.Request)
		body      []byte
		key       []byte
		verifyNow time.Time
	}{
		{name: "body", body: []byte(`{"ok":false}`), key: publicKey, verifyNow: now},
		{name: "method", mutate: func(r *http.Request) { r.Method = http.MethodPut }, body: body, key: publicKey, verifyNow: now},
		{name: "path", mutate: func(r *http.Request) { r.URL.Path = "/v1/other" }, body: body, key: publicKey, verifyNow: now},
		{name: "query", mutate: func(r *http.Request) { r.URL.RawQuery = "cursor=changed" }, body: body, key: publicKey, verifyNow: now},
		{name: "timestamp", mutate: func(r *http.Request) { r.Header.Set(HeaderTime, "1750000001") }, body: body, key: publicKey, verifyNow: now},
		{name: "nonce", mutate: func(r *http.Request) { r.Header.Set(HeaderNonce, r.Header.Get(HeaderNonce)+"AA") }, body: body, key: publicKey, verifyNow: now},
		{name: "signature", mutate: func(r *http.Request) { r.Header.Set(HeaderSignature, "not-base64") }, body: body, key: publicKey, verifyNow: now},
		{name: "different key", body: body, key: otherPublicKey, verifyNow: now},
		{name: "expired", body: body, key: publicKey, verifyNow: now.Add(MaxClockSkew + time.Second)},
		{name: "too far in future", body: body, key: publicKey, verifyNow: now.Add(-MaxClockSkew - time.Second)},
		{name: "invalid public key", body: body, key: []byte("short"), verifyNow: now},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := newSignedRequest(t)
			if test.mutate != nil {
				test.mutate(request)
			}
			if _, err := VerifyRequest(request, test.body, test.key, test.verifyNow); err == nil {
				t.Fatal("VerifyRequest() accepted a tampered or invalid request")
			}
		})
	}
}

func TestSignedRequestInputValidationAndKeyEncoding(t *testing.T) {
	t.Parallel()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://control.example/", nil)
	if err := SignRequest(request, nil, "agent_123", ed25519.PrivateKey("short"), time.Now()); err == nil {
		t.Fatal("SignRequest() accepted an invalid private key")
	}
	if _, err := VerifyRequest(request, nil, publicKey, time.Now()); err == nil {
		t.Fatal("VerifyRequest() accepted missing signature headers")
	}

	encoded := EncodePrivateKey(privateKey)
	decoded, err := DecodePrivateKey(encoded)
	if err != nil {
		t.Fatalf("DecodePrivateKey() error = %v", err)
	}
	if !privateKey.Equal(decoded) {
		t.Fatal("decoded private key differs from original")
	}
	if EncodePublicKey(publicKey) == "" {
		t.Fatal("EncodePublicKey() returned an empty value")
	}
	for _, value := range []string{"", "not-base64", EncodePublicKey(publicKey)} {
		if _, err := DecodePrivateKey(value); err == nil {
			t.Fatalf("DecodePrivateKey(%q) unexpectedly succeeded", value)
		}
	}
}
