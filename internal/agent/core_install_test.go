package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMihomoInstallFallsBackOnlyAfterDefaultVersionFailure(t *testing.T) {
	defaultContents := mihomoVersionFixture(t, false)
	compatibleContents := mihomoVersionFixture(t, true)
	asset := func(name string, contents []byte) githubReleaseAsset {
		digest := sha256.Sum256(contents)
		return githubReleaseAsset{
			Name: name, Size: int64(len(contents)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
			BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/" + name,
		}
	}
	defaultAsset := asset("mihomo-linux-amd64-v1.19.30.gz", defaultContents)
	compatibleAsset := asset("mihomo-linux-amd64-compatible-v1.19.30.gz", compatibleContents)
	releaseBody, err := json.Marshal(githubRelease{
		TagName: "v1.19.30", Assets: []githubReleaseAsset{compatibleAsset, defaultAsset},
	})
	if err != nil {
		t.Fatal(err)
	}
	downloads := map[string][]byte{defaultAsset.Name: defaultContents, compatibleAsset.Name: compatibleContents}
	requested := make([]string, 0, 2)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "api.github.com" {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(releaseBody)), Header: make(http.Header), Request: request}, nil
		}
		name := filepath.Base(request.URL.Path)
		contents, ok := downloads[name]
		if !ok {
			t.Fatalf("unexpected asset request %s", request.URL.String())
		}
		requested = append(requested, name)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(contents)), Header: make(http.Header), Request: request}, nil
	})}
	updater := &CoreUpdater{
		client: client, apiBase: githubAPIBase, goarch: "amd64", libc: "gnu", trustedURL: trustedCoreReleaseURL,
		downloadAttempts: 1, downloadAttemptTimeout: 10 * time.Second,
	}

	directory := t.TempDir()
	serviceHelper := filepath.Join(directory, "service-helper")
	if err := os.WriteFile(serviceHelper, []byte("#!/bin/sh\ncase \"$1\" in\nis-active) echo active ;;\nrestart) : ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	spec := EngineSpec{Binary: filepath.Join(directory, "mihomo"), Service: "qagent-mihomo.service"}
	manager := &ServiceManager{kind: ServiceManagerSystemd, executable: serviceHelper, enableExecutable: serviceHelper}
	output, err := updater.Install(context.Background(), core.EngineMihomo, spec, core.CoreVersionStable, "", manager)
	if err != nil {
		t.Fatalf("install with compatible fallback: %v\n%s", err, output)
	}
	if len(requested) != 2 || requested[0] != defaultAsset.Name || requested[1] != compatibleAsset.Name {
		t.Fatalf("Mihomo asset requests = %v, want default then compatible", requested)
	}
	if !strings.Contains(output, "fallback: default Mihomo binary failed version verification") || !strings.Contains(output, compatibleAsset.Digest) {
		t.Fatalf("fallback install output = %q", output)
	}
	version, err := run(context.Background(), spec.Binary, "-v")
	if err != nil || !strings.Contains(version, "v1.19.30") {
		t.Fatalf("installed compatible binary version = %q, %v", version, err)
	}
}

func mihomoVersionFixture(t *testing.T, succeeds bool) []byte {
	t.Helper()
	var script bytes.Buffer
	if succeeds {
		script.WriteString("#!/bin/sh\necho 'Mihomo Meta v1.19.30 linux amd64 compatible'\nexit 0\n")
	} else {
		script.WriteString("#!/bin/sh\necho 'unsupported CPU' >&2\nexit 1\n")
	}
	// Keep both the extracted executable and compressed release asset above the
	// production minimum without relying on a compiled fixture.
	script.WriteString(": <<'QCH_PADDING'\n")
	padding := make([]byte, 2<<20)
	if _, err := rand.Read(padding); err != nil {
		t.Fatal(err)
	}
	hex.NewEncoder(&script).Write(padding)
	script.WriteString("\nQCH_PADDING\n")
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(script.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}
