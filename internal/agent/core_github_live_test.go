package agent

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/githubfetch"
)

// Opt-in real downloads. The service-manager fixture keeps these tests away
// from host services; extraction and binary version verification are real.
func TestLiveGitHubCoreDownloads(t *testing.T) {
	if os.Getenv("QCH_TEST_LIVE_GITHUB") != "1" {
		t.Skip("set QCH_TEST_LIVE_GITHUB=1 for real public GitHub downloads")
	}
	updater := NewCoreUpdater()
	transport := updater.client.Transport.(*githubfetch.Transport)
	base := transport.Base
	var mu sync.Mutex
	var wireURLs []string
	transport.Base = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		wireURLs = append(wireURLs, r.URL.String())
		mu.Unlock()
		t.Logf("wire %s %s", r.Method, r.URL.Hostname())
		return base.RoundTrip(r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		t.Run(string(engine), func(t *testing.T) {
			spec, manager := liveCoreServiceFixture(t, engine)
			for _, phase := range []string{"install", "upgrade replacement"} {
				output, err := updater.Install(ctx, engine, spec, core.CoreVersionStable, "", manager)
				if err != nil {
					t.Fatalf("%s: %v\n%s", phase, err, output)
				}
				t.Logf("%s: %s", phase, output)
			}
		})
	}
	for _, tc := range []struct {
		engine core.Engine
		source string
	}{
		{core.EngineMihomo, ""}, {core.EngineMihomo, string(core.CoreSourceMirror)},
		{core.EngineXray, ""}, {core.EngineSingBox, ""}, {core.EngineShadowsocksRust, ""},
	} {
		t.Run(string(tc.engine)+" development "+tc.source, func(t *testing.T) {
			release, err := updater.resolveRelease(ctx, tc.engine, core.CoreVersionDevelopment, tc.source)
			if err != nil {
				if strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "no installable") || strings.Contains(err.Error(), "prerelease") || strings.Contains(err.Error(), "SHA-256 digest") {
					t.Skipf("development channel not validated: %v", err)
				}
				t.Fatal(err)
			}
			spec, manager := liveCoreServiceFixture(t, tc.engine)
			output, err := updater.Install(ctx, tc.engine, spec, core.CoreVersionDevelopment, tc.source, manager)
			if err != nil {
				t.Fatalf("development install: %v\n%s", err, output)
			}
			t.Logf("verified development %s %s\n%s", release.Tag, release.Asset.Name, output)
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(wireURLs) == 0 {
		t.Fatal("no requests")
	}
	if expected := os.Getenv("QCH_TEST_GITHUB_CHANNEL"); expected != "" {
		for _, raw := range wireURLs {
			proxied := strings.HasPrefix(raw, githubfetch.Prefix)
			if proxied != (expected == "ipv6-proxy") {
				t.Fatalf("expected %s channel; request %s", expected, raw)
			}
		}
	}
}

func liveCoreServiceFixture(t *testing.T, engine core.Engine) (EngineSpec, *ServiceManager) {
	t.Helper()
	directory := t.TempDir()
	helper := filepath.Join(directory, "service-fixture")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ncase \"$1\" in\nis-active) echo active ;;\nrestart) : ;;\n*) exit 1 ;;\nesac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return EngineSpec{Binary: filepath.Join(directory, "core"), Service: "qagent-" + string(engine) + ".service"}, &ServiceManager{kind: ServiceManagerSystemd, executable: helper, enableExecutable: helper}
}
