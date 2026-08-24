package store

import (
	"strings"
	"testing"
)

func testEncryptionKey(label string) string {
	return strings.Repeat(label, (minConfigEncryptionKeyBytes+len(label)-1)/len(label))[:minConfigEncryptionKeyBytes]
}

func TestConfigCryptorRoundTripAndPrefix(t *testing.T) {
	t.Parallel()
	cryptor, err := newConfigCryptor(testEncryptionKey("roundtrip"))
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
	if !strings.HasPrefix(sealed, keyedEncryptedPrefix) {
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

func TestConfigCryptorKeyringReadsPreviousKeyAndWritesCurrentKey(t *testing.T) {
	t.Parallel()
	oldCryptor, _ := newConfigCryptor(testEncryptionKey("old secret"))
	oldSealed, err := oldCryptor.encrypt("rotated payload")
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := newConfigCryptorKeyring([]string{testEncryptionKey("current secret"), testEncryptionKey("old secret")})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := keyring.decrypt(oldSealed)
	if err != nil || opened != "rotated payload" {
		t.Fatalf("decrypt with previous key = %q, %v", opened, err)
	}
	currentSealed, err := keyring.encrypt("new payload")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(currentSealed, oldCryptor.primary.id) {
		t.Fatal("new ciphertext was written with the previous key")
	}
	if _, err := oldCryptor.decrypt(currentSealed); err == nil {
		t.Fatal("previous key decrypted data written with the current key")
	}
}

func TestConfigCryptorMissingKeyFailsClosedForCiphertext(t *testing.T) {
	t.Parallel()
	cryptor, _ := newConfigCryptor(testEncryptionKey("configured secret"))
	sealed, err := cryptor.encrypt("sensitive")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*configCryptor)(nil).decrypt(sealed); err == nil {
		t.Fatal("encrypted data was returned without a configured key")
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

func TestStoreDecryptContentFailsClosedWithoutKey(t *testing.T) {
	t.Parallel()
	plain := &Store{}
	if out, err := plain.decryptContent("legacy: plaintext"); err != nil || out != "legacy: plaintext" {
		t.Fatalf("legacy plaintext = %q, %v", out, err)
	}
	for _, stored := range []string{"rf1:legacy-ciphertext", "rf2:key-id:ciphertext"} {
		if out, err := plain.decryptContent(stored); err == nil || out != "" {
			t.Fatalf("encrypted value without key = %q, %v", out, err)
		}
	}
}

func TestConfigCryptorLegacyPlaintextPassesThrough(t *testing.T) {
	t.Parallel()
	cryptor, _ := newConfigCryptor(testEncryptionKey("some secret"))
	legacy := "mixed-port: 7890\nproxies: []\n"
	opened, err := cryptor.decrypt(legacy)
	if err != nil || opened != legacy {
		t.Fatalf("legacy decrypt = %q, %v", opened, err)
	}
}

func TestConfigCryptorDetectsTamperingAndWrongKey(t *testing.T) {
	t.Parallel()
	cryptor, _ := newConfigCryptor(testEncryptionKey("secret one"))
	sealed, err := cryptor.encrypt("payload")
	if err != nil {
		t.Fatal(err)
	}
	tampered := sealed + "extra"
	if _, err := cryptor.decrypt(tampered); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
	wrong, _ := newConfigCryptor(testEncryptionKey("secret two"))
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
	cryptor, _ := newConfigCryptor(testEncryptionKey("working secret"))
	if err := cryptor.verify(); err != nil {
		t.Fatalf("verify failed for a working key: %v", err)
	}
	if err := (*configCryptor)(nil).verify(); err != nil {
		t.Fatalf("nil cryptor verify = %v", err)
	}
}

func TestConfigCryptorRejectsWeakCurrentKeyButAllowsWeakPreviousKey(t *testing.T) {
	t.Parallel()
	if _, err := newConfigCryptor("short"); err == nil {
		t.Fatal("weak current encryption key was accepted")
	}
	current := testEncryptionKey("current")
	legacyWriter, err := newConfigCryptorKeyring([]string{"", "short"})
	if err != nil {
		t.Fatalf("weak legacy key fixture could not be built: %v", err)
	}
	sealed, err := legacyWriter.encrypt("legacy payload")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := newConfigCryptorKeyring([]string{current, "short"})
	if err != nil {
		t.Fatalf("strong current with weak previous key was rejected: %v", err)
	}
	if legacy.primary.id == "" {
		t.Fatal("keyring did not configure a current key")
	}
	if opened, err := legacy.decrypt(sealed); err != nil || opened != "legacy payload" {
		t.Fatalf("weak previous ciphertext was not readable: %q, %v", opened, err)
	}
}
