//go:build linux

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAdminTokenCredential(t *testing.T) {
	t.Parallel()
	raw := strings.Repeat("a", 48)
	want := sha256.Sum256([]byte(raw))
	encoded := fmt.Sprintf("%x", want)

	for _, test := range []struct {
		name    string
		raw     string
		digest  string
		wantErr bool
	}{
		{name: "raw compatibility", raw: raw},
		{name: "digest only", digest: encoded},
		{name: "matching transition values", raw: raw, digest: encoded},
		{name: "missing", wantErr: true},
		{name: "short raw", raw: "short", wantErr: true},
		{name: "short digest", digest: strings.Repeat("0", 62), wantErr: true},
		{name: "invalid digest", digest: strings.Repeat("z", 64), wantErr: true},
		{name: "mismatch", raw: raw, digest: strings.Repeat("0", 64), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseAdminTokenCredential(test.raw, test.digest)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseAdminTokenCredential(%q, %q) succeeded", test.raw, test.digest)
				}
				return
			}
			if err != nil || got != want {
				t.Fatalf("digest = %x, %v; want %x", got, err, want)
			}
		})
	}
}

func TestSecretFromEnvOrFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "secret")
	if err := os.WriteFile(path, []byte("  file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QCH_TEST_SECRET", "")
	t.Setenv("QCH_TEST_SECRET_FILE", path)
	value, err := secretFromEnvOrFile("QCH_TEST_SECRET", "QCH_TEST_SECRET_FILE")
	if err != nil || value != "file-secret" {
		t.Fatalf("file secret = %q, %v", value, err)
	}

	t.Setenv("QCH_TEST_SECRET", "environment-secret")
	if _, err := secretFromEnvOrFile("QCH_TEST_SECRET", "QCH_TEST_SECRET_FILE"); err == nil {
		t.Fatal("simultaneous environment and file secrets were accepted")
	}
}
