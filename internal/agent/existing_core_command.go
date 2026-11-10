package agent

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// splitServiceWords is a data parser, not a shell. It preserves empty arguments,
// quoting and escapes, and never evaluates substitutions or executes scripts.
func splitServiceWords(value string) ([]string, error) {
	var out []string
	var word strings.Builder
	var quote rune
	started := false
	chars := []rune(value)
	for i := 0; i < len(chars); i++ {
		c := chars[i]
		if c == 0 || c == '\n' || c == '\r' {
			return nil, errors.New("command contains control characters")
		}
		if c == '\\' && quote != '\'' {
			i++
			if i == len(chars) {
				return nil, errors.New("unterminated command escape")
			}
			switch chars[i] {
			case 's':
				word.WriteByte(' ')
			case 't':
				word.WriteByte('\t')
			case 'x':
				if i+2 >= len(chars) {
					return nil, errors.New("incomplete hex escape")
				}
				n, err := strconv.ParseUint(string(chars[i+1:i+3]), 16, 8)
				if err != nil || n == 0 || n == 10 || n == 13 {
					return nil, errors.New("invalid command hex escape")
				}
				word.WriteByte(byte(n))
				i += 2
			default:
				word.WriteRune(chars[i])
			}
			started = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			started = true
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			started = true
			continue
		}
		if unicode.IsSpace(c) {
			if started {
				out = append(out, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		word.WriteRune(c)
		started = true
	}
	if quote != 0 {
		return nil, errors.New("unterminated command quote")
	}
	if started {
		out = append(out, word.String())
	}
	return out, nil
}

// Unknown options fail closed: silently dropping even a valid runtime switch
// could change routing, listeners or the service's resource lookup semantics.
func parseCoreServiceCommand(engine core.Engine, executable string, argv []string, cwd string) (EngineSpec, error) {
	spec := EngineSpec{ServiceBinary: executable}
	if len(argv) == 0 || argv[0] != executable {
		return spec, errors.New("service argv does not identify its executable")
	}
	if cwd == "" {
		cwd = "/"
	}
	if !filepath.IsAbs(cwd) {
		return spec, errors.New("service working directory is not absolute")
	}
	args := argv[1:]
	runSeen := false
	var config, dir, home, acl string
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if flag == "run" && (engine == core.EngineXray || engine == core.EngineSingBox) {
			if runSeen {
				return spec, errors.New("repeated run command")
			}
			runSeen = true
			continue
		}
		value := ""
		inline := false
		if before, after, ok := strings.Cut(flag, "="); ok {
			flag, value, inline = before, after, true
		}
		key := ""
		switch engine {
		case core.EngineMihomo:
			switch flag {
			case "-f", "--config":
				key = "config"
			case "-d", "--dir":
				key = "home"
			default:
				return spec, fmt.Errorf("unmapped Mihomo option %s", flag)
			}
		case core.EngineXray:
			switch flag {
			case "-c", "-config", "--config":
				key = "config"
			case "-confdir", "--confdir":
				key = "dir"
			default:
				return spec, fmt.Errorf("unmapped Xray option %s", flag)
			}
		case core.EngineSingBox:
			switch flag {
			case "-c", "--config":
				key = "config"
			case "-C", "--config-directory":
				key = "dir"
			case "-D", "--directory":
				key = "home"
			default:
				return spec, fmt.Errorf("unmapped sing-box option %s", flag)
			}
		case core.EngineShadowsocksRust:
			switch flag {
			case "-c", "--config":
				key = "config"
			case "--acl":
				key = "acl"
			default:
				return spec, fmt.Errorf("unmapped Shadowsocks Rust option %s", flag)
			}
		default:
			return spec, errors.New("unsupported core")
		}
		if seen[key] {
			return spec, fmt.Errorf("repeated %s option requires an ordered source mapping", key)
		}
		seen[key] = true
		if !inline {
			i++
			if i >= len(args) {
				return spec, fmt.Errorf("missing %s option value", flag)
			}
			value = args[i]
		}
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return spec, fmt.Errorf("invalid %s path", key)
		}
		switch key {
		case "config":
			config = value
		case "dir":
			dir = value
		case "home":
			home = value
		case "acl":
			acl = value
		}
	}
	resolve := func(path, base string) string {
		if path == "" {
			return ""
		}
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(base, path)
	}
	home = resolve(home, cwd)
	// sing-box changes directory before reading -c/-C. Mihomo resolves -f
	// against the process cwd, and provider/resource paths against its home.
	base := cwd
	if engine == core.EngineSingBox && home != "" {
		base = home
	}
	spec.ConfigPath = resolve(config, base)
	spec.ConfigDirectory = resolve(dir, base)
	spec.WorkingDirectory = cwd
	if home != "" {
		spec.WorkingDirectory = home
	}
	spec.ACLPath = resolve(acl, cwd)
	if engine == core.EngineMihomo && spec.ConfigPath == "" && home != "" {
		spec.ConfigPath = filepath.Join(home, "config.yaml")
	}
	if spec.ConfigPath == "" && spec.ConfigDirectory == "" {
		return spec, errors.New("service has no explicit configuration source")
	}
	if (engine == core.EngineXray || engine == core.EngineSingBox) && !runSeen {
		return spec, errors.New("service command is not run")
	}
	return spec, nil
}
