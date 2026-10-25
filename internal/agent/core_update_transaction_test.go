package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type coreUpdateTestTransaction struct {
	preflight func(context.Context, *os.Root, string, map[string]string) (string, error)
	stop      func(context.Context) (string, error)
	activate  func(context.Context) (string, error)
	recover   func(context.Context) (string, error)
	close     func()
}

func (transaction *coreUpdateTestTransaction) Preflight(ctx context.Context, root *os.Root, name string, assets map[string]string) (string, error) {
	return transaction.preflight(ctx, root, name, assets)
}
func (transaction *coreUpdateTestTransaction) Stop(ctx context.Context) (string, error) {
	return transaction.stop(ctx)
}
func (transaction *coreUpdateTestTransaction) Activate(ctx context.Context) (string, error) {
	return transaction.activate(ctx)
}
func (transaction *coreUpdateTestTransaction) Recover(ctx context.Context) (string, error) {
	return transaction.recover(ctx)
}
func (transaction *coreUpdateTestTransaction) Close() { transaction.close() }

func TestCoreUpdateTransactionOrderAndRecovery(t *testing.T) {
	asset := mihomoVersionFixture(t, true)
	digest := sha256.Sum256(asset)
	release, err := json.Marshal(githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{{
		Name: "mihomo-linux-amd64-v1.19.30.gz", Size: int64(len(asset)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-linux-amd64-v1.19.30.gz",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		want []string
	}{
		{"incompatible shared configuration", []string{"preflight", "close"}},
		{"stop fails", []string{"preflight", "stop", "recover", "close"}},
		{"shared activation fails", []string{"preflight", "stop", "activate", "stop", "recover", "close"}},
		{"task canceled during activation", []string{"preflight", "stop", "activate", "stop", "recover", "close"}},
		{"all instances upgraded", []string{"preflight", "stop", "activate", "close"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			spec := EngineSpec{Binary: filepath.Join(directory, "mihomo")}
			if err := os.WriteFile(spec.Binary, []byte("old-binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			updater := NewCoreUpdater()
			updater.goarch, updater.libc = "amd64", "gnu"
			updater.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				body := asset
				if request.URL.Host == "api.github.com" {
					body = release
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
			})}
			var calls []string
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			assertOld := func() {
				content, err := os.ReadFile(spec.Binary)
				if err != nil || string(content) != "old-binary" {
					t.Fatalf("old binary unavailable during transaction: %q, %v", content, err)
				}
			}
			transaction := &coreUpdateTestTransaction{
				preflight: func(_ context.Context, root *os.Root, candidate string, _ map[string]string) (string, error) {
					calls = append(calls, "preflight")
					assertOld()
					if _, err := root.Stat(candidate); err != nil {
						t.Fatal(err)
					}
					if test.name == "incompatible shared configuration" {
						return "", errors.New("target rejected shared deployed configuration")
					}
					return "all configurations checked", nil
				},
				stop: func(ctx context.Context) (string, error) {
					calls = append(calls, "stop")
					if ctx.Err() != nil {
						t.Fatalf("compensation reused the canceled task context: %v", ctx.Err())
					}
					if test.name == "stop fails" {
						return "", errors.New("shared stop failed")
					}
					return "all affected instances stopped", nil
				},
				activate: func(ctx context.Context) (string, error) {
					calls = append(calls, "activate")
					if version := binaryVersion(ctx, core.EngineMihomo, spec.Binary); !strings.Contains(version, "1.19.30") {
						t.Fatalf("activation did not receive the verified candidate: %s", version)
					}
					if test.name == "task canceled during activation" {
						cancel()
						return "", context.Canceled
					}
					if test.name == "shared activation fails" {
						return "", errors.New("shared startup failed")
					}
					return "all affected instances active", nil
				},
				recover: func(ctx context.Context) (string, error) {
					calls = append(calls, "recover")
					if ctx.Err() != nil {
						t.Fatal("recovery context is canceled")
					}
					assertOld()
					return "original instances recovered", nil
				},
				close: func() { calls = append(calls, "close") },
			}
			output, err := updater.installWithTransaction(ctx, core.EngineMihomo, spec, "1.19.30", "", nil, true, transaction)
			if (err == nil) != (test.name == "all instances upgraded") {
				t.Fatalf("unexpected task result: err=%v output=%s", err, output)
			}
			if !reflect.DeepEqual(calls, test.want) {
				t.Fatalf("transaction calls=%v, want %v", calls, test.want)
			}
			if err != nil {
				assertOld()
			}
		})
	}
}

func TestCoreResourceRollbackFailureStillRestoresBinaryAndRemainingAssets(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	backups := map[string]string{}
	for _, name := range []string{"xray", "geoip.dat", "geosite.dat"} {
		for path, contents := range map[string]string{name: "old-" + name, name + ".candidate": "new-" + name} {
			if err := os.WriteFile(filepath.Join(directory, path), []byte(contents), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		backups[name], err = replaceCoreBinary(root, name, name+".candidate")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := root.Remove(backups["geoip.dat"]); err != nil {
		t.Fatal(err)
	}
	_, err = rollbackCoreUpdateFiles(root, "xray", backups["xray"], map[string]string{
		"geoip.dat": backups["geoip.dat"], "geosite.dat": backups["geosite.dat"],
	})
	if err == nil || !strings.Contains(err.Error(), "geoip.dat") {
		t.Fatalf("missing resource rollback failure: %v", err)
	}
	for _, name := range []string{"xray", "geosite.dat"} {
		content, err := root.ReadFile(name)
		if err != nil || string(content) != "old-"+name {
			t.Fatalf("an earlier resource failure skipped %s recovery: %s %v", name, content, err)
		}
	}
}

func TestSSRustUpdateProbePreservesTargetOptionsAndProtocols(t *testing.T) {
	content := `{"mode":"tcp_and_udp","outbound_fwmark":20260601,"servers":[{"server":"::1","server_port":12345,"method":"2022-blake3-aes-128-gcm","password":"isolated-test-key"},{"server":"127.0.0.1","server_port":12346,"mode":"udp_only","method":"target-removed-cipher","password":"retained"}]}`
	output, endpoints, err := ssRustUpdateProbeConfiguration(content)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(endpoints, []string{"tcp:24001", "udp:24001", "udp:24002"}) {
		t.Fatalf("wrong private listener requirements: %v", endpoints)
	}
	for _, retained := range []string{"2022-blake3-aes-128-gcm", "target-removed-cipher", "isolated-test-key", "retained", "20260601"} {
		if !strings.Contains(output, retained) {
			t.Fatalf("update probe dropped a target-sensitive option: %s", retained)
		}
	}
	for _, invalid := range []string{`{"server":"example.test","server_port":1}`, `{"server":"127.0.0.1","server_port":1,"mode":"unknown"}`, `{"servers":[]}`} {
		if _, _, err := ssRustUpdateProbeConfiguration(invalid); err == nil {
			t.Fatalf("unverifiable input was treated as compatible: %s", invalid)
		}
	}
}
