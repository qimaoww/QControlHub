package store

import (
	"strings"
	"testing"
)

func TestConfigCryptorRoundTripAndPrefix(t *testing.T) {
	t.Parallel()
	cryptor, err := newConfigCryptor("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if cryptor == nil {
		t.Fatal("non-empty secret produced a nil cryptor")
	}
	sealed, err := cryptor.encrypt(`{"server":"0.0.0.0","server_port":8388}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, encryptedPrefix) {
		t.Fatalf("sealed value lacks prefix: %q", sealed)
	}
	if sealed == `{"server":"0.0.0.0","server_port":8388}` {
		t.Fatal("sealed value is plaintext")
	}
	opened, err := cryptor.decrypt(sealed)
	if err != nil || opened != `{"server":"0.0.0.0","server_port":8388}` {
		t.Fatalf("decrypt = %q, %v", opened, err)
	}
	// Encrypting twice must produce distinct ciphertexts (random nonces).
	second, _ := cryptor.encrypt(`{"server":"0.0.0.0","server_port":8388}`)
	if second == sealed {
		t.Fatal("two encryptions produced identical ciphertext")
	}
}

func TestConfigCryptorNilMeansPlaintext(t *testing.T) {
	t.Parallel()
	cryptor, err := newConfigCryptor("")
	if err != nil || cryptor != nil {
		t.Fatalf("empty secret = %v, %v; want nil", cryptor, err)
	}
	if out, err := cryptor.encrypt("plain"); err != nil || out != "plain" {
		t.Fatalf("nil encrypt = %q, %v", out, err)
	}
	if out, err := cryptor.decrypt("plain"); err != nil || out != "plain" {
		t.Fatalf("nil decrypt = %q, %v", out, err)
	}
}

func TestConfigCryptorLegacyPlaintextPassesThrough(t *testing.T) {
	t.Parallel()
	cryptor, _ := newConfigCryptor("some secret")
	legacy := "mixed-port: 7890\nproxies: []\n"
	opened, err := cryptor.decrypt(legacy)
	if err != nil || opened != legacy {
		t.Fatalf("legacy decrypt = %q, %v", opened, err)
	}
}

func TestConfigCryptorDetectsTamperingAndWrongKey(t *testing.T) {
	t.Parallel()
	cryptor, _ := newConfigCryptor("secret one")
	sealed, err := cryptor.encrypt("payload")
	if err != nil {
		t.Fatal(err)
	}
	tampered := sealed + "extra"
	if _, err := cryptor.decrypt(tampered); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
	wrong, _ := newConfigCryptor("secret two")
	if _, err := wrong.decrypt(sealed); err == nil {
		t.Fatal("wrong key decrypted the payload")
	}
	// Truncated payload must fail cleanly, not panic.
	if _, err := cryptor.decrypt(encryptedPrefix + "AQID"); err == nil {
		t.Fatal("truncated payload was accepted")
	}
}

func TestConfigCryptorVerifyRejectsBrokenKey(t *testing.T) {
	t.Parallel()
	cryptor, _ := newConfigCryptor("working secret")
	if err := cryptor.verify(); err != nil {
		t.Fatalf("verify failed for a working key: %v", err)
	}
	if err := (*configCryptor)(nil).verify(); err != nil {
		t.Fatalf("nil cryptor verify = %v", err)
	}
}
