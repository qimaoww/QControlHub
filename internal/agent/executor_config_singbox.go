package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// normalizeImportedSingBoxLogDestination moves every existing file log onto
// the managed service's console stream. The Agent's bounded journal/OpenRC
// transport is the only node-local cache; durable history belongs to the panel.
func normalizeImportedSingBoxLogDestination(content string) (string, error) {
	output, destination, err := singBoxLogOutput(content)
	if err != nil {
		return "", err
	}
	if destination != singBoxLogDestinationFile {
		return content, nil
	}
	if strings.ContainsAny(output, "\x00\r\n") {
		return "", errors.New("sing-box log output contains a control character")
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return "", err
	}
	var logging map[string]json.RawMessage
	if err := json.Unmarshal(root["log"], &logging); err != nil {
		return "", fmt.Errorf("sing-box log configuration is not an object: %w", err)
	}
	logging["output"] = json.RawMessage(`"stdout"`)
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

func readExistingConfigurationSources(spec EngineSpec) (string, string, error) {
	// A mapping with no main configuration file is directory-authoritative: the
	// confdir alone supplies every source, so there is no primary to read.
	if spec.ConfigPath == "" {
		if spec.ConfigDirectory == "" {
			return "", "", errors.New("configuration mapping has neither a file nor a directory")
		}
		return mergeExistingConfigurationDirectory(spec, "")
	}
	primary, err := readConfigurationFile(spec.ConfigPath)
	if err != nil {
		return "", "", err
	}
	if spec.ConfigDirectory == "" {
		digest := sha256.Sum256([]byte(spec.ConfigPath + "\x00" + primary))
		return primary, hex.EncodeToString(digest[:]), nil
	}
	return mergeExistingConfigurationDirectory(spec, primary)
}

func mergeExistingConfigurationDirectory(spec EngineSpec, primary string) (string, string, error) {
	if err := validateProtectedDirectoryChain(spec.ConfigDirectory); err != nil {
		return "", "", fmt.Errorf("configuration directory parent chain is unsafe: %w", err)
	}
	entries, err := os.ReadDir(spec.ConfigDirectory)
	if err != nil {
		return "", "", err
	}
	// A config directory is authoritative on its own: when the primary file is
	// the directory's own config.json, it is a fragment of that directory and
	// must not be merged twice (the core reads it once). A mapping with no
	// primary at all is directory-authoritative for the same reason.
	directoryPrimary := spec.ConfigPath == "" ||
		filepath.Clean(spec.ConfigPath) == filepath.Clean(filepath.Join(spec.ConfigDirectory, "config.json"))
	sources := make([]existingConfigSource, 0, len(entries)+1)
	if !directoryPrimary {
		sources = append(sources, existingConfigSource{path: spec.ConfigPath, content: primary})
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return "", "", fmt.Errorf("configuration directory entry %q is not a regular non-symlink JSON file", entry.Name())
		}
		path := filepath.Join(spec.ConfigDirectory, entry.Name())
		contents, err := readConfigurationFile(path)
		if err != nil {
			return "", "", fmt.Errorf("read configuration directory entry %q: %w", entry.Name(), err)
		}
		sources = append(sources, existingConfigSource{path: path, content: contents})
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].path < sources[j].path })
	total := 0
	digest := sha256.New()
	var merged any
	for _, source := range sources {
		total += len(source.content)
		if total > core.MaxConfigBytes {
			return "", "", fmt.Errorf("combined configuration sources exceed %d bytes", core.MaxConfigBytes)
		}
		digest.Write([]byte(source.path))
		digest.Write([]byte{0})
		digest.Write([]byte(source.content))
		digest.Write([]byte{0})
		decoded, err := decodeExtendedJSON(source.content)
		if err != nil {
			return "", "", fmt.Errorf("decode configuration source %q: %w", source.path, err)
		}
		if merged == nil {
			merged = decoded
		} else {
			merged, err = mergeSingBoxJSON(decoded, merged)
			if err != nil {
				return "", "", fmt.Errorf("merge configuration source %q: %w", source.path, err)
			}
		}
	}
	contents, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return "", "", err
	}
	contents = append(contents, '\n')
	if len(contents) > core.MaxConfigBytes {
		return "", "", fmt.Errorf("merged configuration exceeds %d bytes", core.MaxConfigBytes)
	}
	return string(contents), hex.EncodeToString(digest.Sum(nil)), nil
}

// decodeExtendedJSON decodes exactly one JSON value from content while
// tolerating sing-box extended JSON comments (//, # and /* */) that appear
// outside string literals. It rejects trailing non-whitespace so malformed or
// ambiguous sources still fail closed.
func decodeExtendedJSON(content string) (any, error) {
	cleaned, err := stripExtendedJSONComments(content)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(cleaned))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data")
	}
	return decoded, nil
}

// stripExtendedJSONComments removes sing-box extended JSON comments such as
// //, # and /* */ outside string literals. It is string-aware, so comment
// shaped text inside a JSON string is preserved byte for byte. An unterminated
// block comment fails closed instead of being treated as legal trailing data.
func stripExtendedJSONComments(content string) (string, error) {
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); {
		switch content[i] {
		case '"':
			out = append(out, content[i])
			i++
			for i < len(content) {
				out = append(out, content[i])
				if content[i] == '\\' {
					i++
					if i < len(content) {
						out = append(out, content[i])
						i++
					}
					continue
				}
				if content[i] == '"' {
					i++
					break
				}
				i++
			}
		case '/':
			if i+1 < len(content) && content[i+1] == '/' {
				for i < len(content) && content[i] != '\n' {
					i++
				}
				out = append(out, ' ')
			} else if i+1 < len(content) && content[i+1] == '*' {
				i += 2
				commentEnd := i
				for commentEnd+1 < len(content) && !(content[commentEnd] == '*' && content[commentEnd+1] == '/') {
					commentEnd++
				}
				if commentEnd+1 >= len(content) {
					return "", errors.New("unexpected end of JSON comment")
				}
				commentEnd += 2
				i = commentEnd
				out = append(out, ' ')
			} else {
				out = append(out, content[i])
				i++
			}
		case '#':
			for i < len(content) && content[i] != '\n' {
				i++
			}
			out = append(out, ' ')
		default:
			out = append(out, content[i])
			i++
		}
	}
	return string(out), nil
}

type existingConfigSource struct {
	path    string
	content string
}

// mergeSingBoxJSON mirrors sing-box's ordered badjson merge: an existing
// destination wins scalar conflicts, objects merge recursively, and source
// arrays append after the destination array. Paths are sorted before this
// function is called.
func mergeSingBoxJSON(source, destination any) (any, error) {
	if source == nil {
		return destination, nil
	}
	if destination == nil {
		return source, nil
	}
	switch current := destination.(type) {
	case []any:
		if values, ok := source.([]any); ok {
			return append(current, values...), nil
		}
		return append(current, source), nil
	case map[string]any:
		values, ok := source.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cannot merge JSON object with %T", source)
		}
		for key, value := range values {
			if previous, exists := current[key]; exists {
				var err error
				value, err = mergeSingBoxJSON(value, previous)
				if err != nil {
					return nil, err
				}
			}
			current[key] = value
		}
		return current, nil
	default:
		return destination, nil
	}
}

func validateExistingSourceInvocation(ctx context.Context, engine core.Engine, spec EngineSpec) error {
	if spec.ConfigDirectory == "" {
		return nil
	}
	if engine == core.EngineXray {
		return errors.New("Xray config directories must be dumped with the source core merger")
	}
	if engine != core.EngineSingBox {
		return nil
	}
	args := []string{"check"}
	runDirectory := filepath.Dir(spec.ConfigPath)
	if spec.WorkingDirectory != "" {
		args = append(args, "-D", spec.WorkingDirectory)
		runDirectory = spec.WorkingDirectory
	}
	if filepath.Clean(spec.ConfigPath) == filepath.Clean(filepath.Join(spec.ConfigDirectory, "config.json")) {
		args = append(args, "-C", spec.ConfigDirectory)
	} else {
		args = append(args, "-c", spec.ConfigPath, "-C", spec.ConfigDirectory)
	}
	_, err := runInDirectory(ctx, runDirectory, spec.Binary, args...)
	return err
}
