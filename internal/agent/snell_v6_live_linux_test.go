package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"go.yaml.in/yaml/v4"
)

func TestSnellV6OfficialInteroperability(t *testing.T) {
	if os.Getenv("QCH_LIVE_CORE_VALIDATION_TEST") != "1" {
		t.Skip("requires official Snell and Mihomo binaries")
	}
	official := os.Getenv("QCH_SNELL_BINARY")
	if official == "" {
		t.Skip("QCH_SNELL_BINARY is not set")
	}
	mihomo := filepath.Join(os.Getenv("QCH_LIVE_CORE_ROOT"), "bin", "mihomo")
	for _, key := range []string{serverconfig.ProtocolSnellV6, serverconfig.ProtocolSnellV6Unshaped} {
		t.Run(key, func(t *testing.T) {
			protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, key)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Port = availableTCPPort(t)
			dir := t.TempDir()
			config := filepath.Join(dir, "snell.conf")
			content := fmt.Sprintf("[snell-server]\nlisten = 127.0.0.1:%d\npsk = %s\nmode = %s\n", input.Port, input.Credential, input.SnellMode)
			if err := os.WriteFile(config, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(dir, "official.log")
			log, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(official, "-c", config)
			command.Stdout, command.Stderr = log, log
			if err := command.Start(); err != nil {
				log.Close()
				t.Fatal(err)
			}
			process := &trafficMihomoProcess{command: command, done: make(chan error, 1), logPath: logPath}
			go func() { process.done <- command.Wait(); log.Close() }()
			defer process.stop(t)
			// The proxied HTTP request below establishes readiness and interoperability.
			clientPort, client := snellLiveClient(t, mihomo, input)
			defer client.stop(t)
			snellAssertTCPAndUDP(t, clientPort)
		})
	}
}

func TestSnellPresetsUDPAndFailureControls(t *testing.T) {
	if os.Getenv("QCH_LIVE_CORE_VALIDATION_TEST") != "1" {
		t.Skip("requires official Mihomo binary")
	}
	binary := filepath.Join(os.Getenv("QCH_LIVE_CORE_ROOT"), "bin", "mihomo")
	// ShadowTLS TCP and certificate verification are exercised by the full
	// existing traffic matrix; this test exercises UDP, mode and PSK failures.
	for _, key := range []string{serverconfig.ProtocolSnell, serverconfig.ProtocolSnellV6, serverconfig.ProtocolSnellV6Unshaped} {
		t.Run(key, func(t *testing.T) {
			p, _ := serverconfig.FindProtocol(core.EngineMihomo, key)
			input, err := serverconfig.NewPlan(p)
			if err != nil {
				t.Fatal(err)
			}
			input.Listen = "127.0.0.1"
			input.Port = availableTCPPort(t)
			source, err := serverconfig.Generate(core.EngineMihomo, input)
			if err != nil {
				t.Fatal(err)
			}
			server := startMihomoForTrafficTest(t, binary, t.TempDir(), "config.yaml", source, input.Port)
			defer server.stop(t)
			port, client := snellLiveClient(t, binary, input)
			defer client.stop(t)
			snellAssertTCPAndUDP(t, port)
			bad := input
			bad.Credential = "incorrect-credential-with-entropy"
			badPort, badClient := snellLiveClient(t, binary, bad)
			defer badClient.stop(t)
			snellAssertCannotProxy(t, badPort)
			if input.SnellVersion == 6 {
				bad = input
				if input.SnellMode == "default" {
					bad.Protocol = serverconfig.ProtocolSnellV6Unshaped
					bad.SnellMode = "unshaped"
				} else {
					bad.Protocol = serverconfig.ProtocolSnellV6
					bad.SnellMode = "default"
				}
				badPort, badClient = snellLiveClient(t, binary, bad)
				defer badClient.stop(t)
				snellAssertCannotProxy(t, badPort)
			}
		})
	}
}

func snellLiveClient(t *testing.T, binary string, input serverconfig.Input) (int, *trafficMihomoProcess) {
	t.Helper()
	profile, err := serverconfig.BuildClientProfileNamed(input, "127.0.0.1", "", "LIVE")
	if err != nil {
		t.Fatal(err)
	}
	var proxy map[string]any
	if err := yaml.Unmarshal([]byte(profile.Mihomo), &proxy); err != nil {
		t.Fatal(err)
	}
	port := availableTCPPort(t)
	content, err := yaml.Marshal(map[string]any{"mixed-port": port, "mode": "rule", "log-level": "info", "proxies": []any{proxy}, "rules": []string{"MATCH,LIVE"}})
	if err != nil {
		t.Fatal(err)
	}
	return port, startMihomoForTrafficTest(t, binary, t.TempDir(), "config.yaml", string(content), port)
}

func snellAssertTCPAndUDP(t *testing.T, port int) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "snell-cross-implementation") }))
	defer backend.Close()
	assertProxyTraffic(t, port, backend.URL, "snell-cross-implementation", func() string { return "" }, func() string { return "" })
	for _, network := range []struct{ network, address string }{{"udp4", "127.0.0.1:0"}, {"udp6", "[::1]:0"}} {
		echo, _ := mieruUDPBackend(t, network.network, network.address, func(b []byte) []byte { return b })
		association := mieruSOCKSUDP(t, port)
		defer association.close()
		payload := []byte{0, 1, 2, 3, 255, 10, 42}
		got, err := association.exchange(echo, payload, 10*time.Second)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("%s UDP over TCP: %x %v", network.network, got, err)
		}
	}
}

func snellAssertCannotProxy(t *testing.T, port int) {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "must-not-arrive") }))
	defer target.Close()
	request, err := http.NewRequest("GET", target.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(mustSnellProxyURL(t, port))}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 800 * time.Millisecond}
	response, err := client.Do(request)
	if err == nil {
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if bytes.Contains(body, []byte("must-not-arrive")) {
			t.Fatal("incorrect mode/credential forwarded traffic")
		}
	}
}

func mustSnellProxyURL(t *testing.T, port int) *url.URL {
	t.Helper()
	u, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSnellV6LiveCoreUpgrade(t *testing.T) {
	if os.Getenv("QCH_TEST_LIVE_GITHUB") != "1" {
		t.Skip("requires verified official downloads")
	}
	old := os.Getenv("QCH_SNELL_OLD_MIHOMO")
	if old == "" {
		t.Skip("QCH_SNELL_OLD_MIHOMO is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	spec, manager := liveCoreServiceFixture(t, core.EngineMihomo)
	data, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec.Binary, data, 0700); err != nil {
		t.Fatal(err)
	}
	p, _ := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolSnellV6)
	input, err := serverconfig.NewPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	input.Listen = "127.0.0.1"
	input.Port = availableTCPPort(t)
	content, err := serverconfig.Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMihomoSnellCapabilities(ctx, spec.Binary, content); err == nil {
		t.Fatal("old real core passed v6 capability gate")
	}
	updater := NewCoreUpdater()
	out, err := updater.Install(ctx, core.EngineMihomo, spec, core.CoreVersionStable, "", manager)
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if err := validateMihomoSnellCapabilities(ctx, spec.Binary, content); err != nil {
		t.Fatal(err)
	}
	version, err := run(ctx, spec.Binary, "-v")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(strings.TrimSpace(version))
	server := startMihomoForTrafficTest(t, spec.Binary, t.TempDir(), "config.yaml", content, input.Port)
	defer server.stop(t)
	port, client := snellLiveClient(t, spec.Binary, input)
	defer client.stop(t)
	snellAssertTCPAndUDP(t, port)
	// Missing-core installation exercises the same stable resolver independently.
	missing, missingManager := liveCoreServiceFixture(t, core.EngineMihomo)
	if _, err := updater.Install(ctx, core.EngineMihomo, missing, core.CoreVersionStable, "", missingManager); err != nil {
		t.Fatal(err)
	}
	if err := validateMihomoSnellCapabilities(ctx, missing.Binary, content); err != nil {
		t.Fatal(err)
	}
}

func TestSnellShadowTLSUDPAndRejection(t *testing.T) {
	if os.Getenv("QCH_LIVE_CORE_VALIDATION_TEST") != "1" {
		t.Skip("requires official Mihomo")
	}
	binary := filepath.Join(os.Getenv("QCH_LIVE_CORE_ROOT"), "bin", "mihomo")
	cover := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "cover") }))
	cover.EnableHTTP2 = true
	cover.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	cover.StartTLS()
	defer cover.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cover.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", caFile)
	for _, key := range []string{serverconfig.ProtocolSnellShadowTLS, serverconfig.ProtocolSnellV6ShadowTLS, serverconfig.ProtocolSnellV6UnshapedShadowTLS} {
		t.Run(key, func(t *testing.T) {
			protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, key)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Listen, input.Port = "127.0.0.1", availableTCPPort(t)
			input.SnellObfsHost, input.SnellShadowTLSHandshake = "example.com", cover.Listener.Addr().String()
			input.Credential = "psk spaces,quotes\"and\\slashes"
			input.SnellShadowTLSPassword = "shadow spaces,quotes\"and\\slashes"
			source, err := serverconfig.Generate(core.EngineMihomo, input)
			if err != nil {
				t.Fatal(err)
			}
			server := startMihomoForTrafficTest(t, binary, t.TempDir(), "config.yaml", source, input.Port)
			defer server.stop(t)
			port, client := snellLiveClient(t, binary, input)
			defer client.stop(t)
			snellAssertTCPAndUDP(t, port)
			for _, change := range []func(*serverconfig.Input){
				func(p *serverconfig.Input) { p.SnellShadowTLSPassword = "incorrect-shadow-password" },
				func(p *serverconfig.Input) { p.SnellObfsHost = "wrong-certificate.example.test" },
			} {
				bad := input
				change(&bad)
				badPort, badClient := snellLiveClient(t, binary, bad)
				snellAssertCannotProxy(t, badPort)
				badClient.stop(t)
			}
		})
	}
}

// Run only inside disposable nodes prepared with the production service units.
func TestSnellManagedServiceMatrix(t *testing.T) {
	kind := os.Getenv("QCH_SNELL_SERVICE_MANAGER")
	if kind == "" {
		t.Skip("requires a disposable systemd or OpenRC node")
	}
	manager, err := NewServiceManager(kind)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	defer manager.command(context.Background(), "qagent-mihomo", core.ActionStop)
	binary := "/usr/local/lib/qagent/cores/mihomo"
	keys := []string{serverconfig.ProtocolSnell, serverconfig.ProtocolSnellShadowTLS, serverconfig.ProtocolSnellV6, serverconfig.ProtocolSnellV6Unshaped, serverconfig.ProtocolSnellV6ShadowTLS, serverconfig.ProtocolSnellV6UnshapedShadowTLS, serverconfig.ProtocolSnellV6}
	cover := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "cover") }))
	cover.EnableHTTP2 = true
	cover.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	cover.StartTLS()
	defer cover.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cover.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", ca)
	content := "listeners: []\nrules: ['MATCH,DIRECT']\n"
	var inputs []serverconfig.Input
	for _, key := range keys {
		p, _ := serverconfig.FindProtocol(core.EngineMihomo, key)
		input, err := serverconfig.NewPlan(p)
		if err != nil {
			t.Fatal(err)
		}
		input.Listen, input.Port = "127.0.0.1", availableTCPPort(t)
		if p.SnellShadowTLS {
			input.SnellObfsHost, input.SnellShadowTLSHandshake = "example.com", cover.Listener.Addr().String()
		}
		generated, err := serverconfig.Generate(core.EngineMihomo, input)
		if err != nil {
			t.Fatal(err)
		}
		content, err = serverconfig.MutateGenerated(core.EngineMihomo, content, generated, "", "add")
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input)
	}
	executor := &Executor{Specs: DefaultSpecsForServiceManager(kind), Services: manager}
	if out, err := executor.Execute(ctx, core.Task{Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigContent: content}); err != nil {
		t.Fatalf("deploy: %v %s", err, out)
	}
	for _, input := range inputs {
		t.Run(input.Protocol, func(t *testing.T) {
			port, client := snellLiveClient(t, binary, input)
			defer client.stop(t)
			snellAssertTCPAndUDP(t, port)
		})
	}
	if status, err := manager.status(ctx, "qagent-mihomo"); err != nil || status != "active" {
		t.Fatalf("service %s: %v", status, err)
	}
	t.Logf("%s production unit: seven simultaneous listeners, TCP and UDP passed", kind)
}

// The API integration test supplies claimed tasks and deployed exports through
// a local runner. Never enable this outside a disposable node.
func TestSnellManagedAPITask(t *testing.T) {
	path := os.Getenv("QCH_SNELL_API_PAYLOAD")
	if path == "" {
		t.Skip("requires a local API task payload")
	}
	var payload struct {
		Task     *core.Task
		Profiles []string
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	kind := os.Getenv("QCH_SNELL_SERVICE_MANAGER")
	manager, err := NewServiceManager(kind)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Task != nil {
		executor := &Executor{Specs: DefaultSpecsForServiceManager(kind), Services: manager}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if output, err := executor.Execute(ctx, *payload.Task); err != nil {
			t.Fatalf("API task execution: %v\n%s", err, output)
		}
	}
	for i, source := range payload.Profiles {
		var proxy map[string]any
		if err := yaml.Unmarshal([]byte(source), &proxy); err != nil {
			t.Fatal(err)
		}
		name, _ := proxy["name"].(string)
		port := availableTCPPort(t)
		content, err := yaml.Marshal(map[string]any{"mixed-port": port, "mode": "rule", "proxies": []any{proxy}, "rules": []string{"MATCH," + name}})
		if err != nil {
			t.Fatal(err)
		}
		client := startMihomoForTrafficTest(t, "/usr/local/lib/qagent/cores/mihomo", t.TempDir(), "config.yaml", string(content), port)
		defer client.stop(t)
		defer func() {
			if t.Failed() {
				t.Log(client.log(t))
			}
		}()
		if udp, _ := proxy["udp"].(bool); udp {
			snellAssertTCPAndUDP(t, port)
		} else {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "tcp-only") }))
			assertProxyTraffic(t, port, target.URL, "tcp-only", func() string { return "" }, func() string { return client.log(t) })
			target.Close()
		}
		t.Logf("deployed export %d (%s): proxy traffic passed", i+1, proxy["type"])
	}
}
