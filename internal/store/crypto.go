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

const keyedEncryptedPrefix = "rf2:"

const minConfigEncryptionKeyBytes = 32

// configCryptor encrypts configuration payloads at rest with AES-256-GCM.
// A nil *configCryptor means plaintext storage (encryption disabled).
type configCryptor struct {
	primary cryptorKey
	keys    map[string]cryptorKey
}

type cryptorKey struct {
	id   string
	aead cipher.AEAD
}

// newConfigCryptor derives a 256-bit key from the configured secret. An empty
// secret disables encryption entirely (returns nil).
func newConfigCryptor(secret string) (*configCryptor, error) {
	return newConfigCryptorKeyring([]string{secret})
}

func newConfigCryptorKeyring(secrets []string) (*configCryptor, error) {
	keys := make(map[string]cryptorKey)
	var primary cryptorKey
	for index, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		if index == 0 && len([]byte(secret)) < minConfigEncryptionKeyBytes {
			return nil, fmt.Errorf("QCH_CONFIG_ENCRYPTION_KEY must be at least %d bytes", minConfigEncryptionKeyBytes)
		}
		digest := sha256.Sum256([]byte(secret))
		id := base64.RawURLEncoding.EncodeToString(digest[:8])
		if _, exists := keys[id]; exists {
			continue
		}
		block, err := aes.NewCipher(digest[:])
		if err != nil {
			return nil, fmt.Errorf("create config cipher: %w", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("create config GCM: %w", err)
		}
		key := cryptorKey{id: id, aead: aead}
		if primary.aead == nil {
			primary = key
		}
		keys[id] = key
	}
	if primary.aead == nil {
		return nil, nil
	}
	return &configCryptor{primary: primary, keys: keys}, nil
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
	nonce := make([]byte, c.primary.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate config encryption nonce: %w", err)
	}
	sealed := c.primary.aead.Seal(nonce, nonce, []byte(plaintext), []byte(c.primary.id))
	return keyedEncryptedPrefix + c.primary.id + ":" + base64.StdEncoding.EncodeToString(sealed), nil
}

// decrypt opens a stored value. Legacy plaintext (no prefix) passes through
// unchanged; encrypted values fail closed when no key ring is configured.
func (c *configCryptor) decrypt(stored string) (string, error) {
	if !strings.HasPrefix(stored, encryptedPrefix) && !strings.HasPrefix(stored, keyedEncryptedPrefix) {
		return stored, nil
	}
	if c == nil {
		return "", errors.New("configuration encryption key is required for encrypted data")
	}
	if strings.HasPrefix(stored, keyedEncryptedPrefix) {
		parts := strings.SplitN(strings.TrimPrefix(stored, keyedEncryptedPrefix), ":", 2)
		if len(parts) != 2 {
			return "", errors.New("encrypted configuration key header is malformed")
		}
		key, ok := c.keys[parts[0]]
		if !ok {
			return "", fmt.Errorf("configuration encryption key %q is not configured", parts[0])
		}
		return decryptWithKey(key, parts[1], []byte(parts[0]))
	}
	encoded := strings.TrimPrefix(stored, encryptedPrefix)
	var lastErr error
	for _, key := range c.keys {
		plaintext, err := decryptWithKey(key, encoded, nil)
		if err == nil {
			return plaintext, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("decrypt legacy configuration (wrong key or corrupted data): %w", lastErr)
}

func decryptWithKey(key cryptorKey, encoded string, additionalData []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode encrypted configuration: %w", err)
	}
	nonceSize := key.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("encrypted configuration is truncated")
	}
	plaintext, err := key.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], additionalData)
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
		if strings.HasPrefix(stored, encryptedPrefix) || strings.HasPrefix(stored, keyedEncryptedPrefix) {
			return "", errors.New("configuration encryption key is required for encrypted data")
		}
		return stored, nil
	}
	return s.cryptor.decrypt(stored)
}
