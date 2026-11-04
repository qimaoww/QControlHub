package serverconfig

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Content assertions and simulated edits accept native whitespace. Separate
// round-trip tests below assert exact bytes for fields outside a mutation.
func containsNativeJSON(content, fragment string) bool {
	if strings.Contains(content, fragment) {
		return true
	}
	var document, value bytes.Buffer
	if json.Compact(&document, []byte(content)) != nil {
		return false
	}
	if json.Compact(&value, []byte("{"+fragment+"}")) != nil {
		return false
	}
	return strings.Contains(document.String(), strings.TrimSuffix(strings.TrimPrefix(value.String(), "{"), "}"))
}

func replaceNativeNumber(t *testing.T, content, key, before, after string) string {
	t.Helper()
	pattern := regexp.MustCompile(`("` + regexp.QuoteMeta(key) + `"\s*:\s*)` + regexp.QuoteMeta(before) + `\b`)
	match := pattern.FindStringSubmatchIndex(content)
	if match == nil {
		t.Fatalf("fixture does not contain %s=%s", key, before)
	}
	return content[:match[0]] + content[match[2]:match[3]] + after + content[match[1]:]
}

func sameAccountingPlan(left, right AccountingPlan) bool {
	canonical := func(content string) string {
		var value any
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil {
			data, _ := json.Marshal(value)
			return string(data)
		}
		_ = yaml.Unmarshal([]byte(content), &value)
		data, _ := yaml.Marshal(value)
		return string(data)
	}
	left.Content, right.Content = canonical(left.Content), canonical(right.Content)
	return reflect.DeepEqual(left, right)
}

func TestProtocolFormPreservesNestedNativeExtensions(t *testing.T) {
	protocol, _ := FindProtocol("xray", ProtocolVLESS)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.Tag, input.Port = "main", 12345
	generated, err := Generate("xray", input)
	if err != nil {
		t.Fatal(err)
	}
	current := strings.Replace(generated, `"settings": {`, `"settings": {"custom_counter":9007199254740993,`, 1)
	input.Port++
	next, err := Generate("xray", input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := MutateGenerated("xray", current, next, "main", "modify")
	if err != nil || !strings.Contains(result, "9007199254740993") || !containsNativeJSON(result, `"port":12346`) {
		t.Fatalf("form lost a nested extension: %v", err)
	}
}
