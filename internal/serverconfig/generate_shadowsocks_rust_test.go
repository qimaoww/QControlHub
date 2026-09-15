package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestGenerateRejectsBadSS2022KeyLength(t *testing.T) {
	t.Parallel()
	_, err := Generate(core.EngineXray, Input{
		Protocol: ProtocolSS2022, Tag: "ss", Listen: "0.0.0.0", Port: 8388, Username: "default",
		Method: "2022-blake3-aes-256-gcm", Credential: "dG9vLXNob3J0", Transport: "raw",
	})
	if err == nil || !strings.Contains(err.Error(), "32 字节") {
		t.Fatalf("Generate() error = %v", err)
	}
}

func TestShadowsocksRustGeneratedConfigurationsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, protocol := range Protocols(core.EngineShadowsocksRust) {
		protocol := protocol
		t.Run(protocol.Key, func(t *testing.T) {
			t.Parallel()
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			content, err := Generate(core.EngineShadowsocksRust, input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(core.EngineShadowsocksRust, content)
			if !ok || parsed.Protocol != input.Protocol || parsed.Listen != input.Listen || parsed.Port != input.Port || parsed.Credential != input.Credential || parsed.Method != input.Method || parsed.Transport != "raw" {
				t.Fatalf("Parse(ss-rust/%s) = %+v, %v\n%s", protocol.Key, parsed, ok, content)
			}
		})
	}
}

func TestParseAllShadowsocksRustOfficialExtendedConfiguration(t *testing.T) {
	t.Parallel()
	content := `{"servers":[{"server":"::","server_port":26663,"password":"MDEyMzQ1Njc4OWFiY2RlZg==","method":"2022-blake3-aes-128-gcm"},{"server":"127.0.0.1","server_port":26664,"password":"correct-horse-battery-staple","method":"chacha20-ietf-poly1305"}],"timeout":300,"mode":"tcp_and_udp"}`
	inputs := ParseAll(core.EngineShadowsocksRust, content)
	if len(inputs) != 2 {
		t.Fatalf("ParseAll(ss-rust) returned %d entries: %+v", len(inputs), inputs)
	}
	if inputs[0].Tag != "ss-rust-1" || inputs[1].Tag != "ss-rust-2" || inputs[0].Protocol != ProtocolSS2022 || inputs[1].Protocol != ProtocolShadowsocks || inputs[0].Port != 26663 || inputs[1].Port != 26664 {
		t.Fatalf("ParseAll(ss-rust) = %+v", inputs)
	}
	parsed, ok := Parse(core.EngineShadowsocksRust, content)
	if !ok || parsed.Tag != "ss-rust-1" || parsed.Port != 26663 {
		t.Fatalf("Parse(ss-rust) = %+v, %t", parsed, ok)
	}
}
