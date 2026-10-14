package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type redactedCoreError struct {
	err     error
	message string
}

func (e redactedCoreError) Error() string { return e.message }
func (e redactedCoreError) Unwrap() error { return e.err }

func coreInlineDiagnosticKeys(engine core.Engine, content string) map[string]string {
	root, refs, _ := coreCertificateReferences(engine, content)
	fileFields := make(map[string]bool, len(refs))
	for _, ref := range refs {
		fileFields[ref.field] = true
	}
	secrets := make(map[string]string)
	var remember func(any, string)
	remember = func(value any, position string) {
		switch node := value.(type) {
		case string:
			if node != "" && !fileFields[position] {
				secrets[position] = node
			}
		case []any:
			for i, child := range node {
				remember(child, fmt.Sprintf("%s[%d]", position, i))
			}
		}
	}
	var visit func(any, string)
	visit = func(value any, field string) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				position := key
				if field != "" {
					position = field + "." + key
				}
				if field == "proxy-providers" {
					// Match the quoted provider-name locations used by the file
					// collector, including names containing dots or brackets.
					position = fmt.Sprintf("%s[%q]", field, key)
				}
				switch key {
				case "private-key", "private_key", "privateKey", "mldsa65Seed", "mldsa65_seed", "ech-key":
					remember(child, position)
				case "key", "client_key":
					if engine == core.EngineXray && (strings.Contains(field, ".tlsSettings.certificates[") || strings.Contains(field, ".xtlsSettings.certificates[")) || engine == core.EngineSingBox && (strings.HasSuffix(field, ".tls") || strings.HasSuffix(field, ".tls.ech")) {
						remember(child, position)
					}
				}
				visit(child, position)
			}
		case []any:
			for i, child := range node {
				visit(child, fmt.Sprintf("%s[%d]", field, i))
			}
		}
	}
	visit(root, "")
	return secrets
}

func coreDiagnosticRedactor(engine core.Engine, content string) func(string) string {
	var secrets []string
	for _, secret := range coreInlineDiagnosticKeys(engine, content) {
		secrets = append(secrets, secret)
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return func(value string) string {
		for _, secret := range secrets {
			value = strings.ReplaceAll(value, secret, "[redacted]")
			// Native cores can quote multiline PEM as a JSON string.
			quoted, _ := json.Marshal(secret)
			value = strings.ReplaceAll(value, string(quoted[1:len(quoted)-1]), "[redacted]")
		}
		return core.RedactDiagnostic(value)
	}
}

func coreDiagnosticFieldLocations(engine core.Engine, content string) string {
	_, refs, _ := coreCertificateReferences(engine, content)
	var fields []string
	for _, ref := range refs {
		fields = append(fields, ref.field)
	}
	for field := range coreInlineDiagnosticKeys(engine, content) {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	if len(fields) > 16 {
		fields = append(fields[:16], "...")
	}
	return strings.Join(fields, ", ")
}
