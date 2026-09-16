//go:build linux

package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const existingDiscoveryCoreHelperName = "existing-discovery-core"

var existingDiscoveryCoreHelper []byte

func TestMain(tests *testing.M) {
	helper, err := buildExistingDiscoveryCoreHelper()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	existingDiscoveryCoreHelper = helper
	os.Exit(tests.Run())
}

func buildExistingDiscoveryCoreHelper() ([]byte, error) {
	directory, err := os.MkdirTemp("", ".qcontrolhub-existing-discovery-core-")
	if err != nil {
		return nil, fmt.Errorf("create discovery core helper directory: %w", err)
	}
	defer os.RemoveAll(directory)
	sourcePath := filepath.Join(directory, "main.go")
	const source = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type controlMutation struct {
	At      int    ` + "`" + `json:"at"` + "`" + `
	Path    string ` + "`" + `json:"path"` + "`" + `
	Content string ` + "`" + `json:"content"` + "`" + `
}

type helperControl struct {
	Mutations []controlMutation ` + "`" + `json:"mutations"` + "`" + `
}

func applyControl(executable string) {
	controlPath := executable + ".control.json"
	contents, err := os.ReadFile(controlPath)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		os.Exit(3)
	}
	var control helperControl
	if json.Unmarshal(contents, &control) != nil {
		os.Exit(3)
	}
	countPath := executable + ".invocations"
	count := 0
	if value, err := os.ReadFile(countPath); err == nil {
		_, _ = fmt.Sscanf(string(value), "%d", &count)
	}
	count++
	if os.WriteFile(countPath, []byte(fmt.Sprintf("%d\n", count)), 0o600) != nil {
		os.Exit(3)
	}
	for _, mutation := range control.Mutations {
		if mutation.At > 0 && count < mutation.At {
			continue
		}
		if os.WriteFile(mutation.Path, []byte(mutation.Content), 0o600) != nil {
			os.Exit(3)
		}
	}
}

func main() {
	arguments := os.Args[1:]
	if len(arguments) == 1 && arguments[0] == "version" {
		fmt.Println("sing-box version 1.14.0")
		fmt.Println("Tags: with_wireguard,with_tailscale,with_openvpn,with_gvisor")
		return
	}
	executable, err := os.Executable()
	if err != nil {
		os.Exit(3)
	}
	applyControl(executable)
	if len(arguments) >= 2 && arguments[0] == "run" {
		switch arguments[1] {
		case "-dump":
			assetDirectory := os.Getenv("XRAY_LOCATION_ASSET")
			if assetDirectory == "" {
				assetDirectory = filepath.Dir(executable)
			}
			contents, err := os.ReadFile(filepath.Join(assetDirectory, "xray-dump.json"))
			if os.IsNotExist(err) {
				for index := 2; index+1 < len(arguments); index++ {
					if arguments[index] == "-config" {
						contents, err = os.ReadFile(arguments[index+1])
						break
					}
				}
			}
			if err != nil || !json.Valid(contents) {
				os.Exit(1)
			}
			_, _ = os.Stdout.Write(contents)
			return
		case "-test":
			for index := 2; index+1 < len(arguments); index++ {
				if arguments[index] != "-config" {
					continue
				}
				contents, err := os.ReadFile(arguments[index+1])
				if err != nil || !json.Valid(contents) {
					os.Exit(1)
				}
				return
			}
			os.Exit(2)
		}
	}
	if len(arguments) != 3 && len(arguments) != 5 {
		os.Exit(2)
	}
	if arguments[0] != "check" {
		os.Exit(2)
	}
	if arguments[1] == "-C" {
		if len(arguments) != 3 || !filepath.IsAbs(arguments[2]) {
			os.Exit(2)
		}
		info, err := os.Stat(arguments[2])
		if err != nil || !info.IsDir() {
			os.Exit(1)
		}
		entries, err := os.ReadDir(arguments[2])
		if err != nil {
			os.Exit(1)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			contents, err := os.ReadFile(filepath.Join(arguments[2], entry.Name()))
			if err != nil || !json.Valid(contents) {
				os.Exit(1)
			}
		}
		return
	}
	if arguments[1] == "-D" {
		if len(arguments) != 5 || arguments[3] != "-C" || !filepath.IsAbs(arguments[2]) || !filepath.IsAbs(arguments[4]) {
			os.Exit(2)
		}
		if outputPath := os.Getenv("QCH_TEST_ARGS_OUT"); outputPath != "" {
			_ = os.WriteFile(outputPath, []byte(strings.Join(arguments, "\x00")), 0o600)
		}
		info, err := os.Stat(arguments[2])
		if err != nil || !info.IsDir() {
			os.Exit(1)
		}
		entries, err := os.ReadDir(arguments[4])
		if err != nil {
			os.Exit(1)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			contents, err := os.ReadFile(filepath.Join(arguments[4], entry.Name()))
			if err != nil || !json.Valid(contents) {
				os.Exit(1)
			}
		}
		return
	}
	if arguments[1] != "-c" || !filepath.IsAbs(arguments[2]) {
		os.Exit(2)
	}
	contents, err := os.ReadFile(arguments[2])
	if err != nil || !json.Valid(contents) {
		os.Exit(1)
	}
	if len(arguments) == 5 {
		if arguments[3] != "-C" || !filepath.IsAbs(arguments[4]) {
			os.Exit(2)
		}
		info, err := os.Stat(arguments[4])
		if err != nil || !info.IsDir() {
			os.Exit(1)
		}
	}
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		return nil, fmt.Errorf("write discovery core helper source: %w", err)
	}
	binaryPath := filepath.Join(directory, existingDiscoveryCoreHelperName)
	command := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binaryPath, sourcePath)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("build discovery core helper: %w: %s", err, strings.TrimSpace(string(output)))
	}
	helper, err := os.ReadFile(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("read discovery core helper: %w", err)
	}
	if len(helper) < 4 || string(helper[:4]) != "\x7fELF" {
		return nil, errors.New("discovery core helper is not an ELF executable")
	}
	return helper, nil
}
