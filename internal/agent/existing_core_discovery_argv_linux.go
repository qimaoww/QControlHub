//go:build linux

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func engineDisplayName(engine core.Engine) string {
	switch engine {
	case core.EngineMihomo:
		return "Mihomo"
	case core.EngineXray:
		return "Xray"
	case core.EngineSingBox:
		return "sing-box"
	case core.EngineShadowsocksRust:
		return "Shadowsocks Rust"
	default:
		return string(engine)
	}
}

func managedCoreAssetName(engine core.Engine) string {
	if engine == core.EngineShadowsocksRust {
		return "shadowsocks-rust"
	}
	return string(engine)
}

func parseDiscoveredExistingArgv(engine core.Engine, executable, argv string, configs []string) (string, string, string, bool) {
	fields, err := splitServiceWords(argv)
	if err != nil {
		return "", "", "", false
	}
	return matchDiscoveredExistingArgv(engine, executable, fields, configs)
}

// matchDiscoveredExistingArgv maps one already-split command line onto a
// supported existing-core layout. A mapping without a main configuration file
// is directory-authoritative: there is nothing to match against the config
// whitelist, and the confdir alone decides the content. That directory still
// has to clear the protected path chain before anything in it is read.
func matchDiscoveredExistingArgv(engine core.Engine, executable string, fields, configs []string) (string, string, string, bool) {
	configPath, configDirectory, workDirectory, ok := parseExistingArgv(engine, executable, fields)
	if !ok {
		return "", "", "", false
	}
	return configPath, configDirectory, workDirectory, true

}

func parseExistingArgv(engine core.Engine, executable string, fields []string) (string, string, string, bool) {
	if file, dir, work, ok := parseLegacyExistingArgv(engine, executable, fields); ok {
		return file, dir, work, true
	}
	spec, err := parseCoreServiceCommand(engine, executable, fields, "/")
	if err != nil {
		return "", "", "", false
	}
	work := spec.WorkingDirectory
	if work == "/" {
		work = ""
	}
	return spec.ConfigPath, spec.ConfigDirectory, work, true
}

func parseLegacyExistingArgv(engine core.Engine, executable string, fields []string) (string, string, string, bool) {
	if len(fields) < 2 || fields[0] != executable {
		return "", "", "", false
	}
	switch engine {
	case core.EngineShadowsocksRust:
		if (len(fields) == 3 || len(fields) == 5) && fields[1] == "-c" && safeExistingAbsolutePath(fields[2]) {
			if len(fields) == 3 || (fields[3] == "--acl" && fields[4] == filepath.Join(filepath.Dir(fields[2]), "block_cn.acl")) {
				return fields[2], "", "", true
			}
		}
		return "", "", "", false
	case core.EngineXray:
		configPath, configDirectory, ok := parseXrayExistingArgv(fields[1:])
		return configPath, configDirectory, "", ok
	case core.EngineSingBox:
		return parseSingBoxExistingArgv(fields[1:])
	default:
		return "", "", "", false
	}
}

// parseXrayExistingArgv recognizes the exact Xray invocation shapes that can be
// mapped safely: a single configuration file (run -config|-c <file>), a file
// combined with a confdir (run -config|-c <file> -confdir <dir>), and the
// directory-authoritative form used by installers that ship no main file at all
// (run -confdir <dir>), which reports an empty configuration path. Every path
// must be absolute and free of whitespace and the argument list must be exact;
// unknown flags or a repeated flag fail closed.
func parseXrayExistingArgv(args []string) (string, string, bool) {
	if len(args) == 0 || args[0] != "run" {
		return "", "", false
	}
	args = args[1:]
	configFlag := func(flag string) bool { return flag == "-config" || flag == "-c" }
	switch len(args) {
	case 2:
		if args[0] == "-confdir" {
			if !safeExistingAbsolutePath(args[1]) {
				return "", "", false
			}
			return "", args[1], true
		}
		if !configFlag(args[0]) || !safeExistingAbsolutePath(args[1]) {
			return "", "", false
		}
		return args[1], "", true
	case 4:
		if !configFlag(args[0]) || args[2] != "-confdir" {
			return "", "", false
		}
		if !safeExistingAbsolutePath(args[1]) || !safeExistingAbsolutePath(args[3]) {
			return "", "", false
		}
		return args[1], args[3], true
	}
	return "", "", false
}

// parseSingBoxExistingArgv recognizes the exact sing-box invocation shapes
// that can be mapped safely. It supports the legacy run-then-flag forms
// (run -c <file>, run --config <file>, run -c <file> -C <dir> and the
// --config spelling of the same directory form) and the official package form
// (-D <working-directory> -C <config-directory> run). Every path must be
// absolute and free of whitespace, and the argument list must be exact;
// unknown flags, repeated -D/-C, or an ambiguous relative path fail closed.
func parseSingBoxExistingArgv(args []string) (string, string, string, bool) {
	if len(args) == 0 {
		return "", "", "", false
	}
	switch args[0] {
	case "run":
		if len(args) == 3 && (args[1] == "-c" || args[1] == "--config") {
			configPath := args[2]
			if !safeExistingAbsolutePath(configPath) {
				return "", "", "", false
			}
			return configPath, "", "", true
		}
		if len(args) == 5 && (args[1] == "-c" || args[1] == "--config") && args[3] == "-C" {
			configPath := args[2]
			configDirectory := args[4]
			if !safeExistingAbsolutePath(configPath) || !safeExistingAbsolutePath(configDirectory) {
				return "", "", "", false
			}
			return configPath, configDirectory, "", true
		}
	case "-D":
		if len(args) != 5 || args[2] != "-C" || args[4] != "run" {
			return "", "", "", false
		}
		workDirectory := args[1]
		configDirectory := args[3]
		if !safeExistingAbsolutePath(workDirectory) || !safeExistingAbsolutePath(configDirectory) {
			return "", "", "", false
		}
		return filepath.Join(configDirectory, "config.json"), configDirectory, workDirectory, true
	}
	return "", "", "", false
}

func safeExistingAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && path != "" && !strings.ContainsAny(path, " \t\r\n")
}

func resolveDiscoveredExistingBinary(serviceBinary string) (string, error) {
	if validateExistingCoreExecutable(serviceBinary) == nil && validateProtectedDirectoryChain(filepath.Dir(serviceBinary)) == nil {
		return serviceBinary, nil
	}
	real, err := resolveImportSource(serviceBinary)
	if err != nil {
		return "", err
	}
	if err := validateExistingCoreExecutable(real); err == nil {
		return real, nil
	}
	info, err := os.Stat(real)
	if err != nil || info.Size() > 4096 {
		return "", errors.New("service wrapper exceeds the supported limit")
	}
	if err := validatePrivilegedExecutable(real); err != nil {
		return "", err
	}
	data, _, err := readImportSource(real, 4096)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) != 3 || lines[0] != "#!/bin/sh" || lines[2] != "" {
		return "", errors.New("service wrapper must be a fixed exec forwarder")
	}
	const suffix = " \"$@\""
	if !strings.HasPrefix(lines[1], "exec ") || !strings.HasSuffix(lines[1], suffix) {
		return "", errors.New("service wrapper must unconditionally forward its arguments")
	}
	command := strings.TrimSuffix(strings.TrimPrefix(lines[1], "exec "), suffix)
	words, err := splitServiceWords(command)
	if err != nil || len(words) != 1 || strings.ContainsAny(command, "$`;") {
		return "", errors.New("service wrapper has an unverified command")
	}
	target, err := resolveImportSource(words[0])
	if err != nil {
		return "", err
	}
	if err := validateExistingCoreExecutable(target); err != nil {
		return "", err
	}
	return target, nil
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func limitDiscoveryIssue(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "�"))
	if len(value) <= 512 {
		return value
	}
	return strings.ToValidUTF8(value[:512], "�")
}
