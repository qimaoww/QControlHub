package authn

import (
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	minPasswordBytes = 12
	maxPasswordBytes = 72
	passwordCost     = 12
)

// ValidatePassword applies the panel's password policy before hashing. The
// upper bound is bcrypt's input limit and prevents silently hashing a prefix.
func ValidatePassword(password string) error {
	if !utf8.ValidString(password) {
		return errors.New("password must be valid UTF-8")
	}
	length := len([]byte(password))
	if length < minPasswordBytes {
		return errors.New("password must be at least 12 bytes")
	}
	if length > maxPasswordBytes {
		return errors.New("password must be at most 72 bytes")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func CheckPassword(hash, password string) bool {
	if strings.TrimSpace(hash) == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func NormalizeUsername(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", errors.New("username is required")
	}
	if len(value) > 64 {
		return "", errors.New("username must be at most 64 bytes")
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return "", errors.New("username may contain only lowercase letters, numbers, dot, underscore, and hyphen")
	}
	return value, nil
}
