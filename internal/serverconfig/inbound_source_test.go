package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestNativeInboundSourceKeepsCustomOptionsAndExactNumbers(t *testing.T) {
	for _, tc := range []struct {
		engine core.Engine
		source string
	}{
		{core.EngineXray, `{"inbounds":[{"tag":"manual","protocol":"socks","port":21001,"settings":{"auth":"password","accounts":[{"user":"a","pass":"test-a"},{"user":"b","pass":"test-b"}]},"custom":{"exact":9007199254740993}},{"tag":"other","protocol":"http","port":21002}],"routing":{"domainStrategy":"AsIs"},"custom_root":9007199254740993}`},
		{core.EngineSingBox, `{"inbounds":[{"tag":"manual","type":"socks","listen_port":21001,"users":[{"username":"a","password":"test-a"},{"username":"b","password":"test-b"}],"custom":{"exact":9007199254740993}},{"tag":"other","type":"http","listen_port":21002}],"route":{"final":"direct"},"custom_root":9007199254740993}`},
		{core.EngineMihomo, "listeners:\n  - name: manual\n    type: socks\n    port: 21001\n    custom: {exact: 9007199254740993}\n  - name: other\n    type: http\n    port: 21002\nrules:\n  - MATCH,DIRECT\n"},
		{core.EngineShadowsocksRust, `{"servers":[{"id":"manual","server_port":21001,"method":"aes-256-gcm","password":"test-a","custom":{"exact":9007199254740993}},{"id":"other","server_port":21002,"method":"aes-256-gcm","password":"test-b"}],"dns":{"type":"system"}}`},
	} {
		t.Run(string(tc.engine), func(t *testing.T) {
			fragment, _, err := InboundSource(tc.engine, tc.source, "manual")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(fragment, "9007199254740993") {
				t.Fatal("native option was rounded or lost")
			}
			other, _, err := InboundSource(tc.engine, tc.source, "other")
			if err != nil {
				t.Fatal(err)
			}
			modified := strings.Replace(fragment, "21001", "23101", 1)
			result, err := ReplaceInboundSource(tc.engine, tc.source, "manual", modified)
			if err != nil {
				t.Fatal(err)
			}
			after, _, err := InboundSource(tc.engine, result, "manual")
			if err != nil || !strings.Contains(after, "23101") || !strings.Contains(after, "9007199254740993") {
				t.Fatalf("native update lost options: %v", err)
			}
			otherAfter, _, err := InboundSource(tc.engine, result, "other")
			if err != nil || otherAfter != other {
				t.Fatal("another inbound was changed")
			}
			if _, err = ReplaceInboundSource(tc.engine, tc.source, "missing", modified); err == nil {
				t.Fatal("missing inbound was appended")
			}
			if _, err = ReplaceInboundSource(tc.engine, tc.source, "manual", strings.Replace(fragment, "manual", "other", 1)); err == nil {
				t.Fatal("another identity replaced the selected inbound")
			}
		})
	}
}

func TestNativeSingleShadowsocksRustInbound(t *testing.T) {
	source := `{"server":"127.0.0.1","server_port":21001,"method":"aes-256-gcm","password":"test-native","timeout":600,"dns":{"type":"system"}}`
	fragment, _, err := InboundSource(core.EngineShadowsocksRust, source, "ss-rust")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := ReplaceInboundSource(core.EngineShadowsocksRust, source, "ss-rust", strings.Replace(fragment, "21001", "23101", 1))
	if err != nil || !strings.Contains(updated, `"timeout": 600`) || !strings.Contains(updated, "23101") {
		t.Fatalf("single inbound lost native globals: %v", err)
	}
	if _, _, err := InboundSource(core.EngineShadowsocksRust, updated, "ss-rust"); err != nil {
		t.Fatal("single inbound identity changed")
	}
}
