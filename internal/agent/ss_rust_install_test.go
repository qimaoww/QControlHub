package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/ulikunitz/xz"
)

const ssRustGLIBCFixture = "printf \"%s: /lib/libc.so.6: version `GLIBC_2.39' not found (required by %s)\\n\" \"$0\" \"$0\" >&2\nexit 1\n"

func ssRustArchiveFixture(t *testing.T, body string) []byte {
	t.Helper()
	padding := make([]byte, 2<<20)
	if _, err := rand.Read(padding); err != nil {
		t.Fatal(err)
	}
	script := []byte("#!/bin/sh\n" + body + "\nexit 0\n: <<'PADDING'\n" + hex.EncodeToString(padding) + "\nPADDING\n")
	var buffer bytes.Buffer
	xzWriter, err := xz.NewWriter(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(xzWriter)
	if err := writer.WriteHeader(&tar.Header{Name: "ssserver", Mode: 0755, Size: int64(len(script)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write(script)
	_ = writer.Close()
	_ = xzWriter.Close()
	return buffer.Bytes()
}

func ssRustFixtureUpdater(t *testing.T, gnu, musl []byte, mutate func(*githubRelease)) (*CoreUpdater, *[]string) {
	t.Helper()
	release := githubRelease{TagName: "v1.25.0"}
	bodies := map[string][]byte{}
	for libc, body := range map[string][]byte{"gnu": gnu, "musl": musl} {
		if body == nil {
			continue
		}
		name := "shadowsocks-v1.25.0.x86_64-unknown-linux-" + libc + ".tar.xz"
		digest := sha256.Sum256(body)
		release.Assets = append(release.Assets, githubReleaseAsset{Name: name, Size: int64(len(body)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
			BrowserDownloadURL: "https://github.com/shadowsocks/shadowsocks-rust/releases/download/v1.25.0/" + name})
		bodies[name] = body
	}
	if mutate != nil {
		mutate(&release)
	}
	metadata, _ := json.Marshal(release)
	var requests []string
	updater := NewCoreUpdater()
	updater.goarch, updater.libc = "amd64", "gnu"
	updater.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := metadata
		if request.URL.Host != "api.github.com" {
			name := filepath.Base(request.URL.Path)
			requests = append(requests, name)
			var ok bool
			body, ok = bodies[name]
			if !ok {
				t.Fatalf("unexpected asset: %s", request.URL)
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
	})}
	return updater, &requests
}

func TestSSRustInstallFallbackAndFailureCleanup(t *testing.T) {
	good := ssRustArchiveFixture(t, "echo 'shadowsocks 1.25.0'")
	glibc := ssRustArchiveFixture(t, strings.ReplaceAll(ssRustGLIBCFixture, "`GLIBC", "\\`GLIBC"))
	for _, test := range []struct {
		name, body string
		fallback   bool
	}{
		{name: "compatible GNU", body: "echo 'shadowsocks 1.25.0'"},
		{name: "missing GLIBC", fallback: true},
		{name: "empty version", body: "exit 0"},
		{name: "wrong version", body: "echo 'shadowsocks 1.25.1'"},
		{name: "version prefix is insufficient", body: "echo 'shadowsocks 1.25.01'"},
		{name: "CLI failure", body: "echo 'unsupported --version' >&2; exit 1"},
		{name: "CPU failure", body: "echo 'Illegal instruction' >&2; exit 132"},
		{name: "GLIBC fragment is insufficient", body: "echo 'GLIBC_2.39 not found' >&2; exit 1"},
		{name: "diagnostic encoding and secrets", body: "printf 'password=fixture-secret \\377\\033\\001 failed\\n' >&2; exit 1"},
		{name: "output limit", body: "dd if=/dev/zero bs=70000 count=1; exit 1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			primary := glibc
			if test.body != "" {
				primary = ssRustArchiveFixture(t, test.body)
			}
			updater, requests := ssRustFixtureUpdater(t, primary, good, nil)
			directory := t.TempDir()
			spec := EngineSpec{Binary: filepath.Join(directory, "ssserver")}
			original := []byte("original binary must survive failures")
			if err := os.WriteFile(spec.Binary, original, 0755); err != nil {
				t.Fatal(err)
			}
			output, err := updater.install(context.Background(), core.EngineShadowsocksRust, spec, "stable", "", nil, false)
			succeeds := test.fallback || test.name == "compatible GNU"
			if (err == nil) != succeeds {
				t.Fatalf("success=%v, err=%v output=%s", succeeds, err, output)
			}
			wantRequests := 1
			if test.fallback {
				wantRequests = 2
				if !strings.Contains(output, "same-release musl") || !strings.Contains(output, "GLIBC_2.39") {
					t.Fatalf("missing fallback evidence: %s", output)
				}
			}
			if len(*requests) != wantRequests {
				t.Fatalf("requests=%v", *requests)
			}
			if !succeeds {
				if !strings.Contains(output, "stage=version probe") || !strings.Contains(output, "libc=gnu") {
					t.Fatalf("lost diagnostic context: %s", output)
				}
				after, _ := os.ReadFile(spec.Binary)
				if !bytes.Equal(after, original) {
					t.Fatal("failed candidate replaced existing binary")
				}
				if !utf8.ValidString(output) || len(output) > 2048 || strings.Contains(output, "fixture-secret") || strings.ContainsFunc(output, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
					t.Fatalf("unsafe diagnostic: %q", output)
				}
			}
			candidates, _ := filepath.Glob(filepath.Join(directory, ".qcontrolhub-core-*"))
			if len(candidates) != 0 {
				t.Fatalf("candidate leak: %v", candidates)
			}
		})
	}

	for _, failure := range []string{"missing musl", "fallback wrong version", "fallback checksum", "checksum", "archive", "missing primary"} {
		t.Run(failure, func(t *testing.T) {
			primary, fallback := glibc, good
			if failure == "missing musl" {
				fallback = nil
			}
			if failure == "fallback wrong version" {
				fallback = ssRustArchiveFixture(t, "echo 'shadowsocks 1.25.1'")
			}
			if failure == "archive" {
				primary = bytes.Repeat([]byte("bad archive"), 200000)
			}
			updater, requests := ssRustFixtureUpdater(t, primary, fallback, func(release *githubRelease) {
				if failure == "missing primary" {
					release.Assets = nil
				}
				if failure == "checksum" || failure == "fallback checksum" {
					for i := range release.Assets {
						if strings.Contains(release.Assets[i].Name, "-gnu.") && failure == "checksum" || strings.Contains(release.Assets[i].Name, "-musl.") && failure == "fallback checksum" {
							release.Assets[i].Digest = "sha256:" + strings.Repeat("0", 64)
						}
					}
				}
			})
			directory := t.TempDir()
			output, err := updater.install(context.Background(), core.EngineShadowsocksRust, EngineSpec{Binary: filepath.Join(directory, "ssserver")}, "stable", "", nil, false)
			if err == nil {
				t.Fatalf("accepted %s: %s", failure, output)
			}
			if failure == "fallback wrong version" && (!strings.Contains(output, "GLIBC_2.39") || !strings.Contains(output, "shadowsocks 1.25.1") || len(*requests) != 2) {
				t.Fatalf("lost fallback diagnostics: %s requests=%v", output, *requests)
			}
			if failure == "fallback checksum" && (!strings.Contains(err.Error(), "SHA-256") || len(*requests) != 2) {
				t.Fatalf("fallback integrity check missing: %v requests=%v", err, *requests)
			}
			if !strings.HasPrefix(failure, "fallback ") && len(*requests) > 1 {
				t.Fatalf("unrelated failure triggered fallback: %v", *requests)
			}
			entries, _ := os.ReadDir(directory)
			if len(entries) != 0 {
				t.Fatalf("failed install leaked files: %v", entries)
			}
		})
	}
}

func TestCoreVersionProbeCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{true: "cancel", false: "deadline"}[canceled], func(t *testing.T) {
			candidate := filepath.Join(t.TempDir(), "ssserver")
			if err := os.WriteFile(candidate, []byte("#!/bin/sh\necho 'probe started' >&2\nexec sleep 30\n"), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			want := context.DeadlineExceeded
			if canceled {
				ctx, cancel = context.WithCancel(context.Background())
				defer cancel()
				time.AfterFunc(100*time.Millisecond, cancel)
				want = context.Canceled
			}
			output, err := verifyCoreCandidate(ctx, core.EngineShadowsocksRust, candidate, "v1.25.0")
			if !errors.Is(err, want) || !strings.Contains(output, "probe started") || ssRustNeedsMusl(candidate, output, err) {
				t.Fatalf("probe output=%q err=%v, want %v", output, err, want)
			}
		})
	}
}

func TestSSRustFailedPreparationReceiptAndRetry(t *testing.T) {
	requireAgentRoot(t)
	asset := ssRustArchiveFixture(t, "echo 'ss-rust loader failure; password=receipt-secret' >&2; exit 1")
	updater, requests := ssRustFixtureUpdater(t, asset, nil, nil)
	directory := t.TempDir()
	spec := EngineSpec{Binary: filepath.Join(directory, "ssserver"), ConfigPath: filepath.Join(directory, "config.json"), Service: "qagent-shadowsocks-rust.service"}
	const original = `{"server":"127.0.0.1","server_port":21001,"password":"unchanged","method":"aes-256-gcm"}`
	if err := os.WriteFile(spec.ConfigPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	client := &Client{executor: &Executor{Specs: map[core.Engine]EngineSpec{core.EngineShadowsocksRust: spec}, Updater: updater},
		config: ClientConfig{StatePath: filepath.Join(directory, "state.json")}}
	for attempt := 1; attempt <= 2; attempt++ {
		task := core.Task{ID: fmt.Sprintf("tsk_%016x", attempt), Action: core.ActionValidate, Engine: core.EngineShadowsocksRust,
			InstallIfMissing: true, ConfigVersion: 1, ConfigContent: original}
		result := client.resultForTask(context.Background(), task)
		if result.Success || !strings.Contains(result.Error, "stable core installation failed; configuration was not executed") ||
			!strings.Contains(result.Output, "ss-rust loader failure") || strings.Contains(result.Output, "receipt-secret") {
			t.Fatalf("lost/unsafe failure receipt: %+v", result)
		}
		// The durable reconnect receipt must retain the useful diagnostic too.
		cached, ok := client.cachedTaskResult(task)
		if !ok || cached.Output != result.Output || cached.Error != result.Error {
			t.Fatalf("lost cached diagnostic: %+v", cached)
		}
		config, _ := os.ReadFile(spec.ConfigPath)
		if string(config) != original {
			t.Fatal("failed preparation touched configuration")
		}
	}
	if len(*requests) != 2 {
		t.Fatalf("unexpected retry requests: %v", *requests)
	}
	// Recover with a working release, then submit two first-use validations
	// together through the real dispatch lock. Only the first may download.
	good := ssRustArchiveFixture(t, "echo 'shadowsocks 1.25.0'")
	working, downloads := ssRustFixtureUpdater(t, good, nil, nil)
	client.executor.Updater = working
	results := make(chan core.TaskResultRequest, 2)
	for attempt := 3; attempt <= 4; attempt++ {
		task := core.Task{ID: fmt.Sprintf("tsk_%016x", attempt), Action: core.ActionValidate, Engine: core.EngineShadowsocksRust,
			InstallIfMissing: true, ConfigVersion: 1, ConfigContent: original}
		go func() { results <- client.resultForTask(context.Background(), task) }()
	}
	for range 2 {
		select {
		case result := <-results:
			if !result.Success {
				t.Fatalf("recovery failed: %+v", result)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent first preparation did not finish")
		}
	}
	if len(*downloads) != 1 {
		t.Fatalf("concurrent preparation downloaded more than once: %v", *downloads)
	}
}
