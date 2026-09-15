package store

import (
	"fmt"
)

func (s *Store) encryptEnrollmentToken(rawToken string) (string, error) {
	if s.cryptor == nil {
		return "", fmt.Errorf("%w: QCH_CONFIG_ENCRYPTION_KEY is required", ErrSecretUnavailable)
	}
	sealed, err := s.cryptor.encrypt(rawToken)
	if err != nil {
		return "", fmt.Errorf("%w: encrypt enrollment credential: %v", ErrSecretUnavailable, err)
	}
	return sealed, nil
}
