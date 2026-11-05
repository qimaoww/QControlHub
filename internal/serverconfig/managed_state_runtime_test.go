package serverconfig

import (
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
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Uses the existing pinned core fixture, loopback peers and temporary files.
// No host services, firewall, routes or production resources are touched.
func TestManagedStateNativeListenersAndRoutes(t *testing.T) {
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
			start := func(content string) func() {
				file := filepath.Join(root, "config.json")
				if engine == core.EngineMihomo {
					file = filepath.Join(root, "config.yaml")
				}
				if err := os.WriteFile(file, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
				binary := string(engine)
				if engine == core.EngineShadowsocksRust {
					binary = "ssserver"
				}
				args := []string{}
				switch engine {
				case core.EngineXray:
					args = []string{"run", "-config", file}
				case core.EngineSingBox:
					args = []string{"run", "-c", file}
				case core.EngineMihomo:
					args = []string{"-d", root, "-f", file}
				case core.EngineShadowsocksRust:
					args = []string{"-c", file}
				}
				log, err := os.Create(filepath.Join(root, "core.log"))
				if err != nil {
					t.Fatal(err)
				}
				command := exec.Command(filepath.Join(directory, binary), args...)
				command.Stdout = log
				command.Stderr = log
				if err := command.Start(); err != nil {
					log.Close()
					t.Fatal(err)
				}
				stopped := false
				stop := func() {
					if stopped {
						return
					}
					stopped = true
					_ = command.Process.Kill()
					_ = command.Wait()
					_ = log.Close()
				}
				t.Cleanup(stop)
				return stop
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
			runtime, err := CompileManagedConfig(engine, source, ManagedState{Disabled: []ManagedResource{{section, "first"}}})
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
			if listening(first) || listening(second) {
				t.Fatal("native cleanup retained a listener")
			}
			empty, err := CompileManagedConfig(engine, source, ManagedState{Disabled: []ManagedResource{{section, "first"}, {section, "second"}}})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PrepareIndependentEgress(engine, empty)
			if err != nil || plan.Source != "disabled" {
				t.Fatalf("all-disabled source is not inert: %v %s", err, empty)
			}
			if strings.TrimSpace(empty) == "" {
				t.Fatal("all-disabled removed the complete global configuration")
			}
		})
	}
}
