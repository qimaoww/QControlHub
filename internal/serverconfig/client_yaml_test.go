package serverconfig

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSingleLineYAMLClientFieldOrder(t *testing.T) {
	proxy := map[string]any{
		"password": "secret: #[]\nnext", "cipher": "aes-128-gcm", "udp": true,
		"port": 443, "server": "2001:db8::1", "type": "ss", "name": "香港: [入口]",
		"plugin-opts": map[string]any{"host": "example.test", "mode": "tls"},
	}
	exported, err := marshalSingleLineYAML(proxy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(exported, "\r\n") {
		t.Fatal("export contains a physical line break")
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(exported), &document); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for index := 0; index < len(document.Content[0].Content); index += 2 {
		keys = append(keys, document.Content[0].Content[index].Value)
	}
	if !reflect.DeepEqual(keys, []string{"name", "type", "server", "port", "cipher", "password", "udp", "plugin-opts"}) {
		t.Fatalf("unexpected field order: %v", keys)
	}
	var decoded map[string]any
	if err := yaml.Unmarshal([]byte(exported), &decoded); err != nil || !reflect.DeepEqual(decoded, proxy) {
		t.Fatal("reordering changed client values or nested options")
	}
}
