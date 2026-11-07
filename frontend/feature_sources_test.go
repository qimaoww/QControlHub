package frontend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustReadFrontendFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(".", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func frontendSources(t *testing.T, names ...string) string {
	t.Helper()
	var source strings.Builder
	for _, name := range names {
		source.Write(mustReadFrontendFile(t, name))
		source.WriteByte('\n')
	}
	return source.String()
}
