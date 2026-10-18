package core

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var diagnosticCredentialKey = regexp.MustCompile(`(?i)(?:^auth$|^pass$|password|passwd|passphrase|private[-_]?key|preshared[-_]?key|secret|token|credential|authorization|api[-_]?key|mldsa65[-_]?seed|short[-_]?ids?|uuid|authentication)`)
var diagnosticBearer = regexp.MustCompile(`(?i)\bBearer\s+[a-z0-9._~+/=-]+`)
var diagnosticAPIKey = regexp.MustCompile(`qch_key_[A-Za-z0-9_-]{43}`)
var diagnosticUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
var diagnosticURLCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/@]+@`)
var diagnosticURLSecret = regexp.MustCompile(`(?i)([?&](?:token|key|api_key|secret|password|access_token)=)[^\s&#"']+`)
var diagnosticAssignments = regexp.MustCompile(`(?im)((?:["']?(?:pass|password|passwd|passphrase|private[-_]?key|preshared[-_]?key|secret|token|api[-_]?key|authorization|uuid|mldsa65[-_]?seed)["']?)\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;\}\]]+)`)

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
			if diagnosticCredentialKey.MatchString(key) {
				result[key] = redactCredentialShape(item)
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
func redactCredentialShape(value any) any {
	switch v := value.(type) {
	case []any:
		r := make([]any, len(v))
		for i, x := range v {
			r[i] = redactCredentialShape(x)
		}
		return r
	case map[string]any:
		r := map[string]any{}
		for k, x := range v {
			r[k] = redactCredentialShape(x)
		}
		return r
	case nil:
		return nil
	default:
		return "[redacted]"
	}
}
func redactDiagnosticYAML(n *yaml.Node, depth int) {
	if depth > 64 {
		n.Kind = yaml.ScalarNode
		n.Tag = "!!str"
		n.Value = "[redacted: nesting limit]"
		n.Content = nil
		return
	}
	n.Anchor = ""
	n.HeadComment = ""
	n.LineComment = ""
	n.FootComment = ""
	if n.Kind == yaml.AliasNode {
		n.Kind = yaml.ScalarNode
		n.Tag = "!!str"
		n.Value = "[redacted alias]"
		n.Alias = nil
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			n.Content[i].HeadComment = ""
			n.Content[i].LineComment = ""
			n.Content[i].FootComment = ""
			n.Content[i].Anchor = ""
			if diagnosticCredentialKey.MatchString(n.Content[i].Value) {
				redactYAMLCredential(n.Content[i+1])
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
