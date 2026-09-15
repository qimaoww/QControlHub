//go:build linux

package agent

import (
	"os"
	"testing"
)

func assertFileContentAndMode(t *testing.T, path, want string, wantMode os.FileMode) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(content) != want {
		t.Fatalf("content of %s = %q, want %q", path, content, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != wantMode {
		t.Fatalf("mode of %s = %o, want %o", path, got, wantMode)
	}
}

func statFileMetadata(t *testing.T, path string) fileMetadata {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return metadataFromFileInfo(info)
}

func assertFileContentAndMetadata(t *testing.T, path, wantContent string, want fileMetadata) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(contents) != wantContent {
		t.Fatalf("content of %s = %q, want %q", path, contents, wantContent)
	}
	got := statFileMetadata(t, path)
	if got.mode != want.mode {
		t.Fatalf("mode of %s = %o, want %o", path, got.mode, want.mode)
	}
	if want.ownershipKnown && (!got.ownershipKnown || got.uid != want.uid || got.gid != want.gid) {
		t.Fatalf("ownership of %s = %d:%d (known=%t), want %d:%d", path, got.uid, got.gid, got.ownershipKnown, want.uid, want.gid)
	}
}

func assignAlternateTestGroup(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() != 0 {
		return
	}
	gid := 65534
	if gid == os.Getegid() {
		gid = 1
	}
	if err := os.Chown(path, os.Geteuid(), gid); err != nil {
		t.Fatalf("assign alternate test group: %v", err)
	}
}
