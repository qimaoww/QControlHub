package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
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

func TestClientOnlyMetadataRequiresEncryptionKey(t *testing.T) {
	t.Parallel()
	dataStore := &Store{}
	_, err := dataStore.SaveAgentConfigWithClientMetadata(context.Background(), core.Config{}, 0, ConfigClientMetadataMutation{
		Tag: "sudoku", Content: `{"sudoku_client_key":"private"}`,
	})
	if !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("SaveAgentConfigWithClientMetadata() error = %v, want ErrSecretUnavailable", err)
	}
}
