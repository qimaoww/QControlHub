package store

import (
	"strings"
	"testing"
)

func TestValidateConfigMetadataCountsCharactersNotUTF8Bytes(t *testing.T) {
	t.Parallel()
	name := strings.Repeat("节", 100)
	description := strings.Repeat("点", 300)
	if _, _, err := validateConfigMetadata(name, description); err != nil {
		t.Fatalf("valid multibyte metadata rejected: %v", err)
	}
	if _, _, err := validateConfigMetadata(name+"超", description); err == nil {
		t.Fatal("configuration name over 100 characters was accepted")
	}
	if _, _, err := validateConfigMetadata(name, description+"长"); err == nil {
		t.Fatal("configuration description over 300 characters was accepted")
	}
}
