package release

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPublicKey(t *testing.T) {
	publicKey, _ := testKey(t)
	raw := base64.RawURLEncoding.EncodeToString(publicKey)
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	for _, fixture := range []struct {
		name    string
		content []byte
	}{
		{"inline", nil},
		{"raw-file", []byte(raw + "\n")},
		{"pem-file", encoded},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			value := raw
			if fixture.content != nil {
				value = filepath.Join(t.TempDir(), "release.pub")
				if err := os.WriteFile(value, fixture.content, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := LoadPublicKey(value)
			if err != nil || !bytes.Equal(got, publicKey) {
				t.Fatalf("public key did not round trip: %v", err)
			}
		})
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherDER, err := x509.MarshalPKIXPublicKey(&other.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"wrong-algorithm": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: otherDER}),
		"multiple-keys":   append(append([]byte(nil), encoded...), encoded...),
		"private-block":   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
		"invalid":         []byte("not a key"),
		"oversized":       bytes.Repeat([]byte("x"), (16<<10)+1),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "release.pub")
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadPublicKey(path); err == nil {
				t.Fatal("invalid public key accepted")
			}
		})
	}
	if _, err := LoadPublicKey(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing key file accepted")
	}
}
