package agent

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var serviceVariable = regexp.MustCompile(`\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))`)
var serviceVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse assignments as data. Substitutions, source, functions and shell
// operators are never evaluated, even in a root-owned environment file.
func parseServiceAssignments(content string, initial map[string]string) (map[string]string, error) {
	values := map[string]string{}
	for key, value := range initial {
		values[key] = value
	}
	for index, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, raw, ok := strings.Cut(line, "=")
		if !ok || !serviceVariableName.MatchString(key) {
			return nil, fmt.Errorf("line %d is not a data assignment", index+1)
		}
		words, err := splitServiceWords(raw)
		if err != nil || len(words) > 1 {
			return nil, fmt.Errorf("line %d has an unsupported assignment", index+1)
		}
		value := ""
		if len(words) == 1 {
			value = words[0]
		}
		if strings.ContainsAny(value, "`\x00") || strings.Contains(value, "$(") {
			return nil, fmt.Errorf("line %d requires shell evaluation", index+1)
		}
		values[key] = value
	}
	return values, nil
}

func expandServiceArgument(value string, environment map[string]string) (string, error) {
	if strings.Contains(value, "$(") || strings.ContainsAny(value, "`%") {
		return "", errors.New("command substitution or service specifier requires a verified runtime mapping")
	}
	var missing bool
	expanded := serviceVariable.ReplaceAllStringFunc(value, func(token string) string {
		key := strings.Trim(strings.TrimPrefix(token, "$"), "{}")
		v, ok := environment[key]
		if !ok {
			missing = true
		}
		return v
	})
	if missing || strings.Contains(expanded, "$") {
		return "", errors.New("service argument references an unresolved environment variable")
	}
	return expanded, nil
}

func parseSystemdEnvironmentFile(content string) (map[string]string, error) {
	values := map[string]string{}
	content = strings.ReplaceAll(content, "\\\n", "")
	for index, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !serviceVariableName.MatchString(key) {
			return nil, fmt.Errorf("environment file line %d is not a valid assignment", index+1)
		}
		value, err := parseSystemdEnvironmentValue(raw)
		if err != nil {
			return nil, fmt.Errorf("environment file line %d: %w", index+1, err)
		}
		values[key] = value
	}
	return values, nil
}

// EnvironmentFile preserves interior unquoted whitespace. Its values are not
// shell commands and dollar signs have no expansion semantics here.
func parseSystemdEnvironmentValue(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	quote := byte(0)
	if raw[0] == '\'' || raw[0] == '"' {
		quote = raw[0]
		raw = raw[1:]
	}
	var out strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == 0 || c == '\r' || c == '\n' {
			return "", errors.New("multiline environment value requires an explicit mapping")
		}
		if quote != 0 && c == quote {
			if strings.TrimSpace(raw[i+1:]) != "" {
				return "", errors.New("ambiguous environment value suffix")
			}
			return out.String(), nil
		}
		if c == '\\' && quote != '\'' {
			i++
			if i == len(raw) {
				return "", errors.New("unterminated environment escape")
			}
			next := raw[i]
			if quote == '"' && next != '"' && next != '\\' && next != '$' && next != '`' {
				out.WriteByte('\\')
			}
			out.WriteByte(next)
		} else {
			out.WriteByte(c)
		}
	}
	if quote != 0 {
		return "", errors.New("unterminated environment quote")
	}
	return out.String(), nil
}
