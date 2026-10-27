package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var diagnosticCredentialKey = regexp.MustCompile(`(?i)(?:^auth$|^pass$|^psk$|password|passwd|passphrase|private[-_]?key|pre[-_]?shared[-_]?key|secret|token|credential|authorization|cookie|api[-_]?key|webhook[-_]?url|sudoku[-_]?client[-_]?key|decryption|mldsa65[-_]?seed|short[-_]?ids?|uuid|^authentication$)`)
var diagnosticBearer = regexp.MustCompile(`(?i)\bBearer\s+[a-z0-9._~+/=-]+`)
var diagnosticAPIKey = regexp.MustCompile(`qch_key_[A-Za-z0-9_-]{43}`)
var diagnosticUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
var diagnosticURLCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/@]+@`)
var diagnosticURLSecret = regexp.MustCompile(`(?i)([?&](?:token|key|api_key|secret|password|access_token)=)[^\s&#"']+`)
var diagnosticAssignments = regexp.MustCompile(`(?im)((?:["']?(?:pass|password|passwd|passphrase|private[-_]?key|pre[-_]?shared[-_]?key|psk|key|decryption|secret|token|api[-_]?key|webhook[-_]?url|authorization|cookie|uuid|mldsa65[-_]?seed)["']?)\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;\}\]]+)`)

// RedactDiagnosticValue preserves resource IDs, versions, structure and error
// context. Serialized configuration/fragment/file strings are parsed as JSON
// or YAML before fallback text redaction, including arrays of credentials.
func RedactDiagnosticValue(value any) any { return redactDiagnosticValue(value, 0) }
func redactDiagnosticValue(value any, depth int) any {
	if depth > 64 {
		return "[redacted: nesting limit]"
	}
	switch v := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, item := range v {
			users, userMap := item.(map[string]any)
			if diagnosticCredentialKey.MatchString(key) || (key == "key" && v["type"] == "sudoku") {
				result[key] = redactCredentialShape(item, depth+1)
			} else if key == "users" && userMap {
				result[key] = redactDiagnosticUserMap(users, depth+1)
			} else {
				result[key] = redactDiagnosticValue(item, depth+1)
				if key == "clients" || key == "users" {
					redactDiagnosticClientIDs(result[key])
				}
			}
		}
		// Private credential sidecars may use an unlabelled scalar content field.
		if path, ok := v["path"].(string); ok && (strings.HasSuffix(path, ".key") || strings.Contains(strings.ToLower(path), "private")) {
			if _, has := result["content"]; has {
				result["content"] = "[redacted]"
			}
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = redactDiagnosticValue(item, depth+1)
		}
		return result
	case string:
		var structured any
		trimmed := strings.TrimSpace(v)
		if len(trimmed) > 1 && (trimmed[0] == '{' || trimmed[0] == '[') {
			d := json.NewDecoder(strings.NewReader(v))
			d.UseNumber()
			if d.Decode(&structured) == nil {
				b, err := json.Marshal(redactDiagnosticValue(structured, depth+1))
				if err == nil {
					return string(b)
				}
			}
		}
		// Only parse mappings/sequences; plain log messages stay plain text.
		var document yaml.Node
		if (strings.Contains(v, ":") || strings.HasPrefix(trimmed, "- ")) && yaml.Unmarshal([]byte(v), &document) == nil && len(document.Content) == 1 && (document.Content[0].Kind == yaml.MappingNode || document.Content[0].Kind == yaml.SequenceNode) {
			redactDiagnosticYAML(document.Content[0], depth+1)
			var output bytes.Buffer
			e := yaml.NewEncoder(&output)
			e.SetIndent(2)
			if e.Encode(document.Content[0]) == nil {
				_ = e.Close()
				return output.String()
			}
		}
		return RedactDiagnostic(v)
	default:
		return value
	}
}

// Mihomo Hy2/AnyTLS/Mieru use username:password dictionaries; TUIC uses
// UUID:password dictionaries. UUID keys are credentials, not resource IDs.
func diagnosticUserKey(key string) string {
	if RedactDiagnostic(key) == key {
		return key
	}
	digest := sha256.Sum256([]byte(key))
	return fmt.Sprintf("[redacted user %x]", digest[:8])
}

func redactDiagnosticUserMap(value map[string]any, depth int) any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[diagnosticUserKey(key)] = redactCredentialShape(item, depth+1)
	}
	return result
}
func redactDiagnosticClientIDs(value any) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			redactDiagnosticClientIDs(item)
		}
	case map[string]any:
		if _, ok := v["id"]; ok {
			v["id"] = "[redacted]"
		}
	}
}
func redactCredentialShape(value any, depth int) any {
	if depth > 64 {
		return "[redacted: nesting limit]"
	}
	switch v := value.(type) {
	case []any:
		r := make([]any, len(v))
		for i, x := range v {
			r[i] = redactCredentialShape(x, depth+1)
		}
		return r
	case map[string]any:
		r := map[string]any{}
		for k, x := range v {
			r[k] = redactCredentialShape(x, depth+1)
		}
		return r
	case nil:
		return nil
	default:
		return "[redacted]"
	}
}
func redactDiagnosticYAML(n *yaml.Node, depth int) {
	n.Anchor = ""
	n.HeadComment = ""
	n.LineComment = ""
	n.FootComment = ""
	if depth > 64 {
		n.Kind = yaml.ScalarNode
		n.Tag = "!!str"
		n.Value = "[redacted: nesting limit]"
		n.Content = nil
		return
	}
	if n.Kind == yaml.AliasNode {
		n.Kind = yaml.ScalarNode
		n.Tag = "!!str"
		n.Value = "[redacted alias]"
		n.Alias = nil
		return
	}
	if n.Kind == yaml.MappingNode {
		protocol := ""
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "type" {
				protocol = n.Content[i+1].Value
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			n.Content[i].HeadComment = ""
			n.Content[i].LineComment = ""
			n.Content[i].FootComment = ""
			n.Content[i].Anchor = ""
			if diagnosticCredentialKey.MatchString(n.Content[i].Value) || (n.Content[i].Value == "key" && protocol == "sudoku") {
				redactYAMLCredential(n.Content[i+1])
			} else if n.Content[i].Value == "users" && n.Content[i+1].Kind == yaml.MappingNode {
				users := n.Content[i+1]
				redactDiagnosticYAML(users, depth+1)
				for j := 0; j+1 < len(users.Content); j += 2 {
					users.Content[j].Value = diagnosticUserKey(users.Content[j].Value)
					redactYAMLCredential(users.Content[j+1])
				}
			} else {
				redactDiagnosticYAML(n.Content[i+1], depth+1)
				if n.Content[i].Value == "clients" || n.Content[i].Value == "users" {
					redactYAMLClientIDs(n.Content[i+1])
				}
			}
		}
	} else if n.Kind == yaml.SequenceNode {
		for _, child := range n.Content {
			redactDiagnosticYAML(child, depth+1)
		}
	} else if n.Tag == "!!str" {
		n.Value = RedactDiagnostic(n.Value)
	}
}
func redactYAMLCredential(n *yaml.Node) {
	n.Anchor = ""
	n.Kind = yaml.ScalarNode
	n.Tag = "!!str"
	n.Value = "[redacted]"
	n.Content = nil
	n.Alias = nil
	n.HeadComment = ""
	n.LineComment = ""
	n.FootComment = ""
}

func redactYAMLClientIDs(n *yaml.Node) {
	if n.Kind == yaml.SequenceNode {
		for _, item := range n.Content {
			redactYAMLClientIDs(item)
		}
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "id" {
				redactYAMLCredential(n.Content[i+1])
			}
		}
	}
}
