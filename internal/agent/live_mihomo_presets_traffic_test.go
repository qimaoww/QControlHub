package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"gopkg.in/yaml.v3"
)

func TestMihomoSnellAndSudokuPresetsTransferRealTraffic(t *testing.T) {
	if os.Getenv("QCH_LIVE_CORE_VALIDATION_TEST") != "1" {
		t.Skip("QCH_LIVE_CORE_VALIDATION_TEST is not enabled")
	}
	binary := filepath.Join(os.Getenv("QCH_LIVE_CORE_ROOT"), "bin", "mihomo")
	if info, err := os.Stat(binary); err != nil || info.IsDir() {
		t.Fatalf("official Mihomo binary is unavailable at %s", binary)
	}
	const response = "qcontrolhub-live-preset-ok"
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, response)
	}))
	defer backend.Close()

	tests := []struct {
		name, protocol string
		edit           func(*serverconfig.Input)
	}{
		{name: "snell-v5", protocol: serverconfig.ProtocolSnell},
		{name: "snell-shadow-tls-v3", protocol: serverconfig.ProtocolSnellShadowTLS},
		{name: "sudoku-ed25519-websocket"},
		{name: "sudoku-stream", edit: func(input *serverconfig.Input) { input.SudokuHTTPMaskMode = "stream" }},
		{name: "sudoku-poll", edit: func(input *serverconfig.Input) { input.SudokuHTTPMaskMode = "poll" }},
		{name: "sudoku-auto", edit: func(input *serverconfig.Input) { input.SudokuHTTPMaskMode = "auto" }},
		{name: "sudoku-websocket", edit: func(input *serverconfig.Input) { input.SudokuHTTPMaskMode = "ws" }},
		{name: "sudoku-raw-tcp", edit: func(input *serverconfig.Input) { input.SudokuHTTPMaskEnabled = false }},
		{name: "sudoku-aes-128-gcm", edit: func(input *serverconfig.Input) { input.Method = "aes-128-gcm" }},
		{name: "sudoku-packed-downlink", edit: func(input *serverconfig.Input) { input.SudokuEnablePureDownlink = false }},
		{name: "sudoku-directional-table", edit: func(input *serverconfig.Input) { input.SudokuTableType = "up_ascii_down_entropy" }},
		{name: "sudoku-http-reuse", edit: func(input *serverconfig.Input) { input.SudokuMultiplex = "auto" }},
		{name: "sudoku-native-multiplex", edit: func(input *serverconfig.Input) { input.SudokuMultiplex = "on" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := test.protocol
			if key == "" {
				key = serverconfig.ProtocolSudoku
			}
			protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, key)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Listen = "127.0.0.1"
			input.Port = availableTCPPort(t)
			if test.edit != nil {
				test.edit(&input)
			}
			serverYAML, err := serverconfig.Generate(core.EngineMihomo, input)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := serverconfig.BuildClientProfileNamed(input, "127.0.0.1", "", "LIVE")
			if err != nil {
				t.Fatal(err)
			}
			var proxy map[string]any
			if err := yaml.Unmarshal([]byte(profile.URI), &proxy); err != nil {
				t.Fatal(err)
			}
			clientPort := availableTCPPort(t)
			clientYAMLBytes, err := yaml.Marshal(map[string]any{
				"mixed-port": clientPort, "mode": "rule", "log-level": "info",
				"proxies":      []any{proxy},
				"proxy-groups": []any{map[string]any{"name": "TEST", "type": "select", "proxies": []string{"LIVE"}}},
				"rules":        []string{"MATCH,TEST"},
			})
			if err != nil {
				t.Fatal(err)
			}

			directory := t.TempDir()
			server := startMihomoForTrafficTest(t, binary, filepath.Join(directory, "server"), "config.yaml", serverYAML, input.Port)
			defer server.stop(t)
			client := startMihomoForTrafficTest(t, binary, filepath.Join(directory, "client"), "config.yaml", string(clientYAMLBytes), clientPort)
			defer client.stop(t)

			assertProxyTraffic(t, clientPort, backend.URL, response, func() string { return server.log(t) }, func() string { return client.log(t) })
		})
	}
}

func TestMihomoSudokuPresetInteroperatesWithUpstream(t *testing.T) {
	if os.Getenv("QCH_LIVE_CORE_VALIDATION_TEST") != "1" {
		t.Skip("QCH_LIVE_CORE_VALIDATION_TEST is not enabled")
	}
	upstreamBinary := os.Getenv("QCH_LIVE_SUDOKU_BINARY")
	if upstreamBinary == "" {
		t.Skip("QCH_LIVE_SUDOKU_BINARY is not configured")
	}
	mihomoBinary := filepath.Join(os.Getenv("QCH_LIVE_CORE_ROOT"), "bin", "mihomo")
	for _, binary := range []string{upstreamBinary, mihomoBinary} {
		if info, err := os.Stat(binary); err != nil || info.IsDir() {
			t.Fatalf("live binary is unavailable at %s", binary)
		}
	}

	const response = "qcontrolhub-sudoku-upstream-ok"
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, response)
	}))
	defer backend.Close()
	requestThrough := func(t *testing.T, proxyPort int, serverLog, clientLog func() string) {
		t.Helper()
		assertProxyTraffic(t, proxyPort, backend.URL, response, serverLog, clientLog)
	}
	newInput := func(t *testing.T) serverconfig.Input {
		t.Helper()
		protocol, ok := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolSudoku)
		if !ok {
			t.Fatal("Sudoku preset is unavailable")
		}
		input, err := serverconfig.NewPlan(protocol)
		if err != nil {
			t.Fatal(err)
		}
		input.Listen, input.Port = "127.0.0.1", availableTCPPort(t)
		return input
	}
	upstreamConfig := func(mode string, localPort int, input serverconfig.Input) string {
		config := map[string]any{
			"mode": mode, "transport": "tcp", "local_port": localPort,
			"key": input.Credential, "aead": input.Method,
			"padding_min": input.SudokuPaddingMin, "padding_max": input.SudokuPaddingMax,
			"ascii": input.SudokuTableType, "enable_pure_downlink": input.SudokuEnablePureDownlink,
			"multiplex": input.SudokuMultiplex,
			"httpmask":  map[string]any{"disable": !input.SudokuHTTPMaskEnabled, "mode": map[bool]string{true: "auto", false: "legacy"}[mode == "server"], "path_root": input.SudokuHTTPMaskPathRoot},
		}
		if mode == "server" {
			config["fallback_address"] = "127.0.0.1:9"
			config["suspicious_action"] = "silent"
		} else {
			config["server_address"] = net.JoinHostPort("127.0.0.1", fmt.Sprint(input.Port))
			config["key"] = input.SudokuClientKey
			config["rule_urls"] = []string{"global"}
			config["httpmask"] = map[string]any{"disable": !input.SudokuHTTPMaskEnabled, "mode": input.SudokuHTTPMaskMode, "path_root": input.SudokuHTTPMaskPathRoot}
		}
		content, _ := json.Marshal(config)
		return string(content)
	}

	t.Run("Mihomo server and upstream client", func(t *testing.T) {
		input := newInput(t)
		serverYAML, err := serverconfig.Generate(core.EngineMihomo, input)
		if err != nil {
			t.Fatal(err)
		}
		directory := t.TempDir()
		server := startMihomoForTrafficTest(t, mihomoBinary, filepath.Join(directory, "mihomo-server"), "config.yaml", serverYAML, input.Port)
		defer server.stop(t)
		clientPort := availableTCPPort(t)
		client := startSudokuForTrafficTest(t, upstreamBinary, filepath.Join(directory, "upstream-client"), upstreamConfig("client", clientPort, input), clientPort)
		defer client.stop(t)
		requestThrough(t, clientPort, func() string { return server.log(t) }, func() string { return client.log(t) })
	})

	t.Run("upstream server and Mihomo client", func(t *testing.T) {
		input := newInput(t)
		directory := t.TempDir()
		server := startSudokuForTrafficTest(t, upstreamBinary, filepath.Join(directory, "upstream-server"), upstreamConfig("server", input.Port, input), input.Port)
		defer server.stop(t)
		profile, err := serverconfig.BuildClientProfileNamed(input, "127.0.0.1", "", "LIVE")
		if err != nil {
			t.Fatal(err)
		}
		var proxy map[string]any
		if err := yaml.Unmarshal([]byte(profile.URI), &proxy); err != nil {
			t.Fatal(err)
		}
		clientPort := availableTCPPort(t)
		clientYAML, _ := yaml.Marshal(map[string]any{
			"mixed-port": clientPort, "mode": "rule", "log-level": "info", "proxies": []any{proxy},
			"proxy-groups": []any{map[string]any{"name": "TEST", "type": "select", "proxies": []string{"LIVE"}}},
			"rules":        []string{"MATCH,TEST"},
		})
		client := startMihomoForTrafficTest(t, mihomoBinary, filepath.Join(directory, "mihomo-client"), "config.yaml", string(clientYAML), clientPort)
		defer client.stop(t)
		requestThrough(t, clientPort, func() string { return server.log(t) }, func() string { return client.log(t) })
	})
}

func assertProxyTraffic(t *testing.T, proxyPort int, targetURL, expected string, serverLog, clientLog func() string) {
	t.Helper()
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	deadline := time.Now().Add(3 * time.Second)
	var lastStatus int
	var lastBody []byte
	var lastErr error
	for {
		request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, targetURL, nil)
		result, err := client.Do(request)
		if err == nil {
			lastStatus = result.StatusCode
			lastBody, lastErr = io.ReadAll(result.Body)
			_ = result.Body.Close()
			if lastErr == nil && lastStatus == http.StatusOK && string(lastBody) == expected {
				return
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("proxied response status=%d body=%q err=%v\nserver log:\n%s\nclient log:\n%s", lastStatus, lastBody, lastErr, serverLog(), clientLog())
		}
		time.Sleep(75 * time.Millisecond)
	}
}

func startSudokuForTrafficTest(t *testing.T, binary, directory, content string, port int) *trafficMihomoProcess {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(directory, "sudoku.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-c", configPath)
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
		_ = logFile.Close()
	}()
	process := &trafficMihomoProcess{command: command, done: done, logPath: logPath}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		connection, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return process
		}
		select {
		case waitErr := <-done:
			t.Fatalf("Sudoku exited before listening: %v\n%s", waitErr, process.log(t))
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	process.stop(t)
	t.Fatalf("Sudoku did not listen on port %d\n%s", port, process.log(t))
	return nil
}

type trafficMihomoProcess struct {
	command *exec.Cmd
	done    chan error
	logPath string
}

func startMihomoForTrafficTest(t *testing.T, binary, directory, configName, content string, port int) *trafficMihomoProcess {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, configName)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(directory, "mihomo.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-d", directory, "-f", configPath)
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
		_ = logFile.Close()
	}()
	process := &trafficMihomoProcess{command: command, done: done, logPath: logPath}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		connection, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return process
		}
		select {
		case waitErr := <-done:
			t.Fatalf("Mihomo exited before listening: %v\n%s", waitErr, process.log(t))
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	process.stop(t)
	t.Fatalf("Mihomo did not listen on port %d\n%s", port, process.log(t))
	return nil
}

func (process *trafficMihomoProcess) stop(t *testing.T) {
	t.Helper()
	if process == nil || process.command == nil || process.command.Process == nil {
		return
	}
	_ = process.command.Process.Signal(os.Interrupt)
	select {
	case <-process.done:
		return
	case <-time.After(2 * time.Second):
		_ = process.command.Process.Kill()
		<-process.done
	}
}

func (process *trafficMihomoProcess) log(t *testing.T) string {
	t.Helper()
	content, _ := os.ReadFile(process.logPath)
	return string(content)
}

func availableTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}
