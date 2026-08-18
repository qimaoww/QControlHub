package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// encryptedPrefix marks stored values produced by configCryptor. Values
// without the prefix are legacy plaintext and pass through unchanged, which
// makes enabling encryption a transparent migration.
const encryptedPrefix = "rf1:"

// configCryptor encrypts configuration payloads at rest with AES-256-GCM.
// A nil *configCryptor means plaintext storage (encryption disabled).
type configCryptor struct {
	aead cipher.AEAD
}

// newConfigCryptor derives a 256-bit key from the configured secret. An empty
// secret disables encryption entirely (returns nil).
func newConfigCryptor(secret string) (*configCryptor, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, nil
	}
	digest := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		return nil, fmt.Errorf("create config cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create config GCM: %w", err)
	}
	return &configCryptor{aead: aead}, nil
}

// verify probes the cryptor with a fixed round trip so a misconfigured key is
// detected at startup instead of after data has been written.
func (c *configCryptor) verify() error {
	if c == nil {
		return nil
	}
	sealed, err := c.encrypt("qcontrolhub-key-probe")
	if err != nil {
		return err
	}
	opened, err := c.decrypt(sealed)
	if err != nil {
		return fmt.Errorf("config encryption key self-test failed: %w", err)
	}
	if opened != "qcontrolhub-key-probe" {
		return errors.New("config encryption key self-test round trip mismatch")
	}
	return nil
}

// encrypt seals plaintext for storage. A nil receiver returns the input
// unchanged (plaintext mode).
func (c *configCryptor) encrypt(plaintext string) (string, error) {
	if c == nil {
		return plaintext, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate config encryption nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return encryptedPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// decrypt opens a stored value. Legacy plaintext (no prefix) passes through
// unchanged; a nil receiver also returns the input untouched.
func (c *configCryptor) decrypt(stored string) (string, error) {
	if c == nil || !strings.HasPrefix(stored, encryptedPrefix) {
		return stored, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, encryptedPrefix))
	if err != nil {
		return "", fmt.Errorf("decode encrypted configuration: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("encrypted configuration is truncated")
	}
	plaintext, err := c.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt configuration (wrong key or corrupted data): %w", err)
	}
	return string(plaintext), nil
}

// encryptContent is a convenience wrapper used by write paths.
func (s *Store) encryptContent(plaintext string) (string, error) {
	if s.cryptor == nil {
		return plaintext, nil
	}
	return s.cryptor.encrypt(plaintext)
}

// decryptContent is a convenience wrapper used by read paths.
func (s *Store) decryptContent(stored string) (string, error) {
	if s.cryptor == nil {
		return stored, nil
	}
	return s.cryptor.decrypt(stored)
}
