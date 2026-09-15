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
	return matchDiscoveredExistingArgv(engine, executable, strings.Fields(argv), configs)
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
	if configPath == "" {
		if configDirectory == "" {
			return "", "", "", false
		}
		return "", configDirectory, workDirectory, true
	}
	for _, candidate := range configs {
		if configPath == candidate {
			return configPath, configDirectory, workDirectory, true
		}
	}
	return "", "", "", false
}

func parseExistingArgv(engine core.Engine, executable string, fields []string) (string, string, string, bool) {
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
	if !filepath.IsAbs(serviceBinary) || strings.ContainsAny(serviceBinary, " \t\r\n") {
		return "", errors.New("service executable path is unsafe")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(serviceBinary)); err != nil {
		return "", err
	}
	info, err := os.Lstat(serviceBinary)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if err := validateExistingCoreExecutable(serviceBinary); err != nil {
			return "", err
		}
		return serviceBinary, nil
	}
	target, err := os.Readlink(serviceBinary)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(serviceBinary), target)
	}
	target = filepath.Clean(target)
	targetInfo, err := os.Lstat(target)
	if err != nil {
		return "", err
	}
	if targetInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("service executable uses more than one symlink")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(target)); err != nil {
		return "", err
	}
	if err := validateExistingCoreExecutable(target); err == nil {
		return target, nil
	}
	contents, err := os.ReadFile(target)
	if err != nil || len(contents) > 1024 {
		return "", errors.New("service executable wrapper is unsupported")
	}
	lines := strings.Split(string(contents), "\n")
	if len(lines) != 3 || lines[0] != "#!/bin/sh" || lines[2] != "" {
		return "", errors.New("service executable wrapper is not the fixed two-line form")
	}
	const prefix = "exec "
	const suffix = " \"$@\""
	if !strings.HasPrefix(lines[1], prefix) || !strings.HasSuffix(lines[1], suffix) {
		return "", errors.New("service executable wrapper is not an unconditional exec forwarder")
	}
	realBinary := strings.TrimSuffix(strings.TrimPrefix(lines[1], prefix), suffix)
	if !filepath.IsAbs(realBinary) || strings.ContainsAny(realBinary, " \t\r\n") {
		return "", errors.New("forwarded core path is unsafe")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(realBinary)); err != nil {
		return "", err
	}
	if err := validateExistingCoreExecutable(realBinary); err != nil {
		return "", err
	}
	return realBinary, nil
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
