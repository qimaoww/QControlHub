package serverconfig

import (
	"encoding/base64"
	"errors"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
)

// mldsa65VerifyFromSeed mirrors `xray mldsa65 -i <seed>` so the client
// verification key can be reconstructed after the server-only seed has been
// persisted in the Xray configuration.
func mldsa65VerifyFromSeed(value string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("invalid ML-DSA-65 seed")
	}
	seed := [32]byte(decoded)
	publicKey, _ := mldsa65.NewKeyFromSeed(&seed)
	return base64.RawURLEncoding.EncodeToString(publicKey.Bytes()), nil
}
