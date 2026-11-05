//go:build linux

package agent

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Uses the existing pinned core fixture, loopback peers and temporary files.
// No host services, firewall, routes or production resources are touched.
func TestManagedStateAgentNativeDeployments(t *testing.T) {
	directory := os.Getenv("QCH_TEST_MANAGED_CORE_DIR")
	if directory == "" {
		t.Skip("requires pinned native core fixture")
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "managed-route-ok") }))
	defer origin.Close()
	port := func() int {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		value := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		return value
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		t.Run(string(engine), func(t *testing.T) {
			first, second := port(), port()
			section := "inbounds"
			source := ""
			switch engine {
			case core.EngineXray:
				source = fmt.Sprintf("{\"log\":{\"loglevel\":\"warning\"},\"inbounds\":[{\"tag\":\"first\",\"listen\":\"127.0.0.1\",\"port\":%d,\"protocol\":\"http\"},{\"tag\":\"second\",\"listen\":\"127.0.0.1\",\"port\":%d,\"protocol\":\"http\"}],\"outbounds\":[{\"tag\":\"direct\",\"protocol\":\"freedom\"}]}", first, second)
			case core.EngineSingBox:
				source = fmt.Sprintf("{\"log\":{\"level\":\"warn\"},\"inbounds\":[{\"tag\":\"first\",\"listen\":\"127.0.0.1\",\"listen_port\":%d,\"type\":\"http\"},{\"tag\":\"second\",\"listen\":\"127.0.0.1\",\"listen_port\":%d,\"type\":\"http\"}],\"outbounds\":[{\"tag\":\"direct\",\"type\":\"direct\"}],\"route\":{\"final\":\"direct\"}}", first, second)
			case core.EngineMihomo:
				section = "listeners"
				source = fmt.Sprintf("log-level: warning\nlisteners:\n  - {name: first, type: http, listen: 127.0.0.1, port: %d}\n  - {name: second, type: http, listen: 127.0.0.1, port: %d}\nrules: ['MATCH,DIRECT']\n", first, second)
			case core.EngineShadowsocksRust:
				section = "servers"
				source = fmt.Sprintf("{\"servers\":[{\"id\":\"first\",\"server\":\"127.0.0.1\",\"server_port\":%d,\"method\":\"aes-256-gcm\",\"password\":\"native-fixture\"},{\"id\":\"second\",\"server\":\"127.0.0.1\",\"server_port\":%d,\"method\":\"aes-256-gcm\",\"password\":\"native-fixture\"}]}", first, second)
			}
			root := t.TempDir()
			file := filepath.Join(root, "config.json")
			if engine == core.EngineMihomo {
				file = filepath.Join(root, "config.yaml")
			}
			binary := string(engine)
			args := []string{}
			switch engine {
			case core.EngineXray:
				args = []string{"run", "-config", file}
			case core.EngineSingBox:
				args = []string{"run", "-c", file}
			case core.EngineMihomo:
				args = []string{"-d", root, "-f", file}
			case core.EngineShadowsocksRust:
				binary = "ssserver"
				args = []string{"-c", file}
			}
			var mu sync.Mutex
			var process *exec.Cmd
			var done chan struct{}
			stopProcess := func() {
				if process != nil {
					_ = process.Process.Kill()
					<-done
					process = nil
				}
			}
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/stop":
					stopProcess()
				case "/restart", "/start":
					stopProcess()
					log, err := os.OpenFile(filepath.Join(root, "core.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
					if err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
					process = exec.Command(filepath.Join(directory, binary), args...)
					process.Stdout = log
					process.Stderr = log
					if err = process.Start(); err != nil {
						log.Close()
						process = nil
						http.Error(w, err.Error(), 500)
						return
					}
					done = make(chan struct{})
					command, finished := process, done
					go func() { _ = command.Wait(); _ = log.Close(); close(finished) }()
				case "/is-active":
					if process != nil {
						select {
						case <-done:
							fmt.Fprintln(w, "failed")
							return
						default:
							fmt.Fprintln(w, "active")
							return
						}
					}
					fmt.Fprintln(w, "inactive")
				default:
					http.Error(w, "unsupported service operation", 400)
				}
			}))
			t.Cleanup(func() { service.Close(); mu.Lock(); defer mu.Unlock(); stopProcess() })
			helper := filepath.Join(root, "service-helper")
			writeExecutable(t, helper, "#!/bin/sh\nexec /usr/bin/curl --silent --show-error --fail "+service.URL+"/\"$1\"\n")
			spec := EngineSpec{Binary: filepath.Join(directory, binary), ConfigPath: file, WorkingDirectory: root, Service: "qch-managed-test.service"}
			executor := &Executor{Specs: map[core.Engine]EngineSpec{engine: spec}, Services: &ServiceManager{kind: ServiceManagerSystemd, executable: helper}}
			start := func(content string) func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				output, err := executor.Execute(ctx, core.Task{Action: core.ActionDeploy, Engine: engine, ConfigContent: content})
				if err != nil {
					log, _ := os.ReadFile(filepath.Join(root, "core.log"))
					t.Fatalf("native Agent deployment: %v\n%s\n%s", err, output, log)
				}
				// The next deployment must replace its own old listeners safely.
				return func() {}
			}
			listening := func(p int) bool {
				conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)), 100*time.Millisecond)
				if err != nil {
					return false
				}
				conn.Close()
				return true
			}
			wait := func(p int) {
				for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
					if listening(p) {
						return
					}
					time.Sleep(25 * time.Millisecond)
				}
				output, _ := os.ReadFile(filepath.Join(root, "core.log"))
				t.Fatalf("listener %d unavailable: %s", p, output)
			}
			proxyPort := second
			if engine == core.EngineShadowsocksRust {
				proxyPort = port()
				file := filepath.Join(root, "client.json")
				if err := os.WriteFile(file, []byte(fmt.Sprintf("{\"server\":\"127.0.0.1\",\"server_port\":%d,\"local_address\":\"127.0.0.1\",\"local_port\":%d,\"method\":\"aes-256-gcm\",\"password\":\"native-fixture\"}", second, proxyPort)), 0600); err != nil {
					t.Fatal(err)
				}
				client := exec.Command(filepath.Join(directory, "sslocal"), "-c", file)
				if err := client.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Process.Kill(); _ = client.Wait() })
			}
			checkRoute := func() {
				scheme := "http"
				if engine == core.EngineShadowsocksRust {
					scheme = "socks5"
				}
				proxyURL, _ := url.Parse(fmt.Sprintf("%s://127.0.0.1:%d", scheme, proxyPort))
				transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
				response, err := client.Get(origin.URL)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				content, _ := io.ReadAll(response.Body)
				if string(content) != "managed-route-ok" {
					t.Fatalf("unexpected route response: %s", content)
				}
			}
			stop := start(source)
			wait(first)
			wait(second)
			wait(proxyPort)
			checkRoute()
			stop()
			runtime, err := serverconfig.CompileManagedConfig(engine, source, serverconfig.ManagedState{Disabled: []serverconfig.ManagedResource{{Section: section, Tag: "first"}}})
			if err != nil {
				t.Fatal(err)
			}
			stop = start(runtime)
			wait(second)
			if listening(first) {
				t.Fatal("disabled listener remains open")
			}
			checkRoute()
			stop()
			stop = start(source)
			wait(first)
			wait(second)
			checkRoute()
			stop()

			empty, err := serverconfig.CompileManagedConfig(engine, source, serverconfig.ManagedState{Disabled: []serverconfig.ManagedResource{{Section: section, Tag: "first"}, {Section: section, Tag: "second"}}})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := serverconfig.PrepareIndependentEgress(engine, empty)
			if err != nil || plan.Source != "disabled" {
				t.Fatalf("all-disabled source is not inert: %v %s", err, empty)
			}
			if strings.TrimSpace(empty) == "" {
				t.Fatal("all-disabled removed the complete global configuration")
			}
			start(empty)
			if listening(first) || listening(second) {
				t.Fatal("all-disabled Agent deployment kept a listener")
			}
			if status, err := executor.Services.status(context.Background(), spec.Service); err != nil || status != "inactive" {
				t.Fatalf("empty deployment did not stop instance: %s %v", status, err)
			}
			start(source)
			wait(first)
			wait(second)
			checkRoute()

		})
	}
}
