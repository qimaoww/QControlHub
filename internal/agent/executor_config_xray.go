package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// readExistingXraySourceDigest reads and fingerprints every exact source that
// Xray will merge. It deliberately does not apply the sing-box JSON merger:
// Xray's own -dump output is the only configuration snapshot used for import.
func readExistingXraySourceDigest(spec EngineSpec) (string, error) {
	if spec.ConfigDirectory == "" {
		return "", errors.New("Xray configuration directory is empty")
	}
	if err := validateProtectedDirectoryChain(spec.ConfigDirectory); err != nil {
		return "", fmt.Errorf("configuration directory parent chain is unsafe: %w", err)
	}
	entries, err := os.ReadDir(spec.ConfigDirectory)
	if err != nil {
		return "", err
	}
	sources := make([]existingConfigSource, 0, len(entries)+1)
	// Unlike sing-box's official -C-only form, any Xray ConfigPath here came
	// from an explicit -config flag. If it also lives in the confdir, Xray reads
	// it once from the flag and once from the directory, so preserve both source
	// occurrences in the size budget and digest.
	if spec.ConfigPath != "" {
		primary, err := readConfigurationFile(spec.ConfigPath)
		if err != nil {
			return "", err
		}
		sources = append(sources, existingConfigSource{path: spec.ConfigPath, content: primary})
	}
	for _, entry := range entries {
		if !isXrayConfigurationFilename(entry.Name()) {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return "", fmt.Errorf("configuration directory entry %q is not a regular non-symlink Xray configuration file", entry.Name())
		}
		path := filepath.Join(spec.ConfigDirectory, entry.Name())
		contents, err := readConfigurationFile(path)
		if err != nil {
			return "", fmt.Errorf("read configuration directory entry %q: %w", entry.Name(), err)
		}
		sources = append(sources, existingConfigSource{path: path, content: contents})
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].path < sources[j].path })
	total := 0
	digest := sha256.New()
	for _, source := range sources {
		total += len(source.content)
		if total > core.MaxConfigBytes {
			return "", fmt.Errorf("combined configuration sources exceed %d bytes", core.MaxConfigBytes)
		}
		digest.Write([]byte(source.path))
		digest.Write([]byte{0})
		digest.Write([]byte(source.content))
		digest.Write([]byte{0})
	}
	if len(sources) == 0 {
		return "", errors.New("Xray configuration directory has no supported configuration sources")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func isXrayConfigurationFilename(name string) bool {
	// Xray's confdir regex is case-sensitive; keep this list exact so the
	// fingerprint covers precisely the files its loader selects.
	switch filepath.Ext(name) {
	case ".json", ".jsonc", ".toml", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

// dumpExistingXrayConfiguration invokes Xray's own config merger over the exact
// protected sources discovered from the service argv. stdout is the canonical
// JSON snapshot; diagnostics stay on stderr so they can never be mistaken for
// configuration content.
func dumpExistingXrayConfiguration(ctx context.Context, spec EngineSpec) (string, error) {
	args := []string{"run", "-dump"}
	if spec.ConfigPath != "" {
		args = append(args, "-config", spec.ConfigPath)
	}
	if spec.ConfigDirectory != "" {
		args = append(args, "-confdir", spec.ConfigDirectory)
	}
	commandContext, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(commandContext, spec.Binary, args...)
	command.Dir = spec.commandDirectory
	if command.Dir == "" {
		command.Dir = filepath.Dir(spec.Binary)
	}
	command.Env = commandEnvironment(spec.commandEnv)
	configureCommand(command)
	stdout := &boundedOutput{limit: core.MaxConfigBytes, onLimit: cancel}
	stderr := &boundedOutput{limit: 64 << 10, onLimit: cancel}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if stdout.Truncated() {
		return "", fmt.Errorf("Xray merged configuration exceeds %d bytes", core.MaxConfigBytes)
	}
	if stderr.Truncated() {
		return "", errors.New("Xray dump diagnostics exceeded the output limit")
	}
	diagnostics := strings.TrimSpace(strings.ToValidUTF8(stderr.String(), "�"))
	if err != nil {
		if diagnostics != "" {
			return "", fmt.Errorf("%w: %s", err, diagnostics)
		}
		return "", err
	}
	content := strings.TrimSpace(stdout.String())
	if content == "" {
		return "", errors.New("Xray returned an empty merged configuration")
	}
	if !utf8.ValidString(content) {
		return "", errors.New("Xray returned a non-UTF-8 merged configuration")
	}
	if err := core.ValidateConfig(core.EngineXray, content); err != nil {
		return "", fmt.Errorf("Xray returned malformed merged configuration: %w", err)
	}
	return content + "\n", nil
}

// normalizeImportedXrayLogDestinations moves existing file logging onto the
// managed service's stdout/stderr stream. Empty Xray destinations mean console;
// explicit "none" remains disabled. Other log policy fields are preserved.
func normalizeImportedXrayLogDestinations(content string) (string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return "", err
	}
	logValue, ok := root["log"]
	if !ok {
		return content, nil
	}
	var logging map[string]json.RawMessage
	if err := json.Unmarshal(logValue, &logging); err != nil {
		return "", fmt.Errorf("Xray log configuration is not an object: %w", err)
	}
	if logging == nil {
		return content, nil
	}
	changed := false
	for _, key := range []string{"access", "error"} {
		raw, ok := logging[key]
		if !ok {
			continue
		}
		var destination string
		if err := json.Unmarshal(raw, &destination); err != nil {
			return "", fmt.Errorf("Xray log.%s destination is not a string: %w", key, err)
		}
		// Xray recognizes only the exact lowercase literal "none". Whitespace
		// and case variants are file names, so they must be normalized too.
		if destination != "" && destination != "none" {
			logging[key] = json.RawMessage(`""`)
			changed = true
		}
	}
	if !changed {
		return content, nil
	}
	normalizedLog, err := json.Marshal(logging)
	if err != nil {
		return "", err
	}
	root["log"] = normalizedLog
	normalized, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(normalized) + "\n", nil
}
