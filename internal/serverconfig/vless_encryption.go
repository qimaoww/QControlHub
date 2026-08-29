package serverconfig

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
)

const (
	vlessDecryptionPrefix = "mlkem768x25519plus.native.600s."
	vlessEncryptionPrefix = "mlkem768x25519plus.native.0rtt."
)

// newVLESSEncryptionPair mirrors the X25519 authentication pair emitted by
// `xray vlessenc`. The private key remains in the server's decryption value;
// only the public key is embedded in the client encryption value.
func newVLESSEncryptionPair() (decryption, encryption string, err error) {
	privateBytes := make([]byte, 32)
	if _, err = rand.Read(privateBytes); err != nil {
		return "", "", err
	}
	privateBytes[0] &= 248
	privateBytes[31] &= 127
	privateBytes[31] |= 64
	privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		return "", "", err
	}
	decryption = vlessDecryptionPrefix + base64.RawURLEncoding.EncodeToString(privateBytes)
	encryption = vlessEncryptionPrefix + base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes())
	return decryption, encryption, nil
}

func vlessEncryptionFromDecryption(decryption string) (string, error) {
	if !strings.HasPrefix(decryption, vlessDecryptionPrefix) {
		return "", errors.New("VLESS Decryption 必须使用 xray vlessenc 的 X25519 600s 格式")
	}
	privateBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(decryption, vlessDecryptionPrefix))
	if err != nil || len(privateBytes) != 32 {
		return "", errors.New("VLESS Decryption 的 X25519 私钥无效")
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		return "", errors.New("VLESS Decryption 的 X25519 私钥无效")
	}
	return vlessEncryptionPrefix + base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()), nil
}

func validateVLESSEncryptionPair(decryption, encryption string) error {
	expected, err := vlessEncryptionFromDecryption(decryption)
	if err != nil {
		return err
	}
	if encryption != expected {
		return errors.New("VLESS Decryption 与 Encryption 不属于同一密钥对")
	}
	return nil
}

func isVLESSEncryptionProtocol(protocol string) bool {
	return protocol == ProtocolVLESSEncTCP || protocol == ProtocolVLESSEncXHTTP
}
