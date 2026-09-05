package serverconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

const ssRustScopedFixture = `{"dns":{"custom":true},"ipv6_first":true,"mode":"tcp_and_udp","no_delay":false,"acl":"/global.acl","outbound_bind_addr":"192.0.2.10","unknown":9007199254740993,"servers":[{"id":"one","server":"::","server_port":20001,"method":"aes-256-gcm","password":"password-one","mode":"tcp_only","unknown":9007199254740993},{"id":"two","server":"::","server_port":20002,"method":"aes-256-gcm","password":"password-two","acl":"/two.acl"}]}`

func TestSSRustFieldScopesMatchRuntime(t *testing.T) {
	for scope, keys := range map[string][]string{
		"inbound":   {"server", "server_port", "password", "method", "plugin", "plugin_opts", "plugin_args", "plugin_mode"},
		"override":  {"mode", "acl", "outbound_bind_addr", "outbound_bind_interface", "outbound_fwmark", "outbound_proxy", "outbound_udp_allow_fragmentation", "inbound_udp_allow_fragmentation"},
		"global":    {"dns", "ipv6_first", "timeout", "udp_timeout", "fast_open", "no_delay", "keep_alive", "security"},
		"structure": {"servers"},
	} {
		for _, key := range keys {
			if got := ssRustFieldScope(key); got != scope {
				t.Errorf("%s scope = %s, want %s", key, got, scope)
			}
		}
	}
}

func TestSSRustInboundFieldsIsolatePortsAndRetainDefaults(t *testing.T) {
	for key, value := range map[string]string{
		"mode": `"udp_only"`, "acl": `"/one.acl"`, "outbound_bind_addr": `"2001:db8::1"`,
		"outbound_bind_interface": `"eth1"`, "outbound_fwmark": `0`,
		"outbound_proxy":                   `["socks5://127.0.0.1:1080"]`,
		"outbound_udp_allow_fragmentation": `false`, "inbound_udp_allow_fragmentation": `true`,
		"plugin": `"obfs-server"`, "plugin_opts": `"obfs=http"`, "plugin_args": `["--test"]`, "plugin_mode": `"tcp_only"`,
	} {
		t.Run(key, func(t *testing.T) {
			modified, err := MergeSSRustInboundField(ssRustScopedFixture, "one", key, value, false)
			if err != nil {
				t.Fatal(err)
			}
			var before, after map[string]json.RawMessage
			_ = json.Unmarshal([]byte(ssRustScopedFixture), &before)
			_ = json.Unmarshal([]byte(modified), &after)
			var oldEntries, newEntries []map[string]any
			_ = json.Unmarshal(before["servers"], &oldEntries)
			_ = json.Unmarshal(after["servers"], &newEntries)
			if !reflect.DeepEqual(oldEntries[1], newEntries[1]) || strings.Count(modified, "9007199254740993") != 2 {
				t.Fatal("another port or unknown numeric field changed")
			}
			delete(before, "servers")
			delete(after, "servers")
			assertSSRustJSONEqual(t, before, after)
			fragment, present, _, err := SSRustInboundField(modified, "one", key)
			if err != nil || !present {
				t.Fatalf("read override: %v, %v", present, err)
			}
			var expected, actual any
			_ = json.Unmarshal([]byte(value), &expected)
			_ = json.Unmarshal([]byte(fragment), &actual)
			if !reflect.DeepEqual(expected, actual) {
				t.Fatalf("read = %s, want %s", fragment, value)
			}
			removed, err := MergeSSRustInboundField(modified, "one", key, "", true)
			if err != nil {
				t.Fatal(err)
			}
			_, present, inherited, err := SSRustInboundField(removed, "one", key)
			if err != nil || present {
				t.Fatalf("remove override: %v, %v", present, err)
			}
			if key == "mode" && inherited != `"tcp_and_udp"` {
				t.Fatalf("lost inherited mode: %s", inherited)
			}
		})
	}
}

func TestSSRustLegacyPortFieldNormalizesWithoutEditingDefaults(t *testing.T) {
	legacy := `{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"password-one","plugin":"my-plugin","mode":"tcp_only","acl":"/global.acl","dns":"9.9.9.9","custom":true}`
	_, present, inherited, err := SSRustInboundField(legacy, "ss-rust", "mode")
	if err != nil || present || inherited != `"tcp_only"` {
		t.Fatalf("legacy default mistaken for override: %v %q %v", present, inherited, err)
	}
	modified, err := MergeSSRustInboundField(legacy, "ss-rust", "mode", `"udp_only"`, false)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"mode": `"tcp_only"`, "acl": `"/global.acl"`, "dns": `"9.9.9.9"`, "custom": `true`} {
		got, _, _ := configschema.Fragment(core.EngineShadowsocksRust, modified, key)
		if got != want {
			t.Fatalf("global %s = %s", key, got)
		}
	}
	inputs := ParseAll(core.EngineShadowsocksRust, modified)
	if len(inputs) != 1 || inputs[0].Tag != "ss-rust" {
		t.Fatalf("legacy selection lost: %+v", inputs)
	}
	got, _, _, _ := SSRustInboundField(modified, "ss-rust", "plugin")
	if got != `"my-plugin"` {
		t.Fatal("legacy plugin not moved with port")
	}
}

func TestSSRustPortFieldsRejectUnsupportedOrAmbiguousScope(t *testing.T) {
	for _, key := range []string{"dns", "ipv6_first", "timeout", "udp_timeout", "fast_open", "no_delay", "keep_alive", "security", "servers", "unknown"} {
		if _, err := MergeSSRustInboundField(ssRustScopedFixture, "one", key, "true", false); err == nil {
			t.Errorf("accepted port-level %s", key)
		}
	}
	for _, tc := range []struct{ content, tag string }{
		{ssRustScopedFixture, ""}, {ssRustScopedFixture, "missing"},
		{`{"servers":null}`, "one"}, {`{"servers":[null]}`, "one"},
		{`{"servers":[{"id":"one"},{"id":"one"}]}`, "one"},
		{`{"server_port":1234,"servers":[]}`, "one"},
		{`{"servers":[],"shadowsocks":[]}`, "one"},
	} {
		if _, err := MergeSSRustInboundField(tc.content, tc.tag, "mode", `"tcp_only"`, false); err == nil {
			t.Errorf("accepted ambiguous target %s / %s", tc.content, tc.tag)
		}
	}
	if _, err := MergeSSRustInboundField(ssRustScopedFixture, "one", "mode", `"tcp_only" true`, false); err == nil {
		t.Fatal("accepted trailing JSON")
	}
	alias := strings.Replace(ssRustScopedFixture, `"servers":`, `"shadowsocks":`, 1)
	modified, err := MergeSSRustInboundField(alias, "two", "mode", `"tcp_only"`, false)
	if err != nil || strings.Contains(modified, `"servers"`) {
		t.Fatalf("alias lost: %s / %v", modified, err)
	}
}

func TestSSRustFieldEditsRejectInvalidValuesBeforeSaving(t *testing.T) {
	for key, values := range map[string][]string{
		"mode": {`"invalid"`, `true`, `null`}, "outbound_bind_addr": {`"eth0"`, `123`},
		"acl": {`"relative.acl"`, `false`}, "server_port": {`0`, `65536`, `1.5`, `-1`},
		"outbound_fwmark": {`4294967296`, `-1`}, "timeout": {`"300"`, `-1`},
		"no_delay": {`"false"`, `0`}, "dns": {`[]`, `false`}, "plugin_args": {`[1]`, `"--arg"`},
	} {
		for _, value := range values {
			if err := ValidateSSRustFieldValue(key, value, false); err == nil {
				t.Errorf("accepted %s=%s", key, value)
			}
		}
	}
	for _, key := range []string{"server", "server_port", "method", "password"} {
		if _, err := MergeSSRustInboundField(ssRustScopedFixture, "one", key, "", true); err == nil {
			t.Errorf("deleted mandatory %s", key)
		}
	}
	if _, err := MergeSSRustInboundField(ssRustScopedFixture, "one", "server_port", "20002", false); err == nil {
		t.Fatal("accepted duplicate port")
	}
	if err := ValidateSSRustFieldValue("timeout", "9007199254740993", false); err != nil {
		t.Fatal(err)
	}
}

func TestSSRustScopedPresetDoesNotChangeGlobals(t *testing.T) {
	for _, current := range []string{ssRustScopedFixture,
		`{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"password-one","mode":"udp_only","timeout":75,"fast_open":true}`,
	} {
		for _, operation := range []string{"modify", "add", "delete"} {
			plan := ParseAll(core.EngineShadowsocksRust, current)[0]
			tag := plan.Tag
			if operation == "add" {
				tag, plan.Port = "", 20003
			}
			plan.SSRustDNS, plan.SSRustIPv6First, plan.SSRustOutboundBindAddr = "", false, ""
			plan.Credential = "password-long-enough-for-test"
			generated, err := Generate(core.EngineShadowsocksRust, plan)
			if err != nil {
				t.Fatal(err)
			}
			modified, err := MutateSSRustPort(current, generated, tag, operation)
			if err != nil {
				t.Fatal(err)
			}
			if operation != "delete" && current == ssRustScopedFixture && strings.Count(modified, "9007199254740993") != 2 {
				t.Fatal("preset changed unknown numeric data")
			}
			catalog, _ := configschema.CatalogFor(core.EngineShadowsocksRust)
			for _, field := range catalog.Fields {
				if field.Scope != "global" && field.Scope != "override" {
					continue
				}
				before, bp, _ := configschema.Fragment(core.EngineShadowsocksRust, current, field.Key)
				after, ap, _ := configschema.Fragment(core.EngineShadowsocksRust, modified, field.Key)
				if bp != ap || before != after {
					t.Fatalf("%s changed global %s: %s -> %s", operation, field.Key, before, after)
				}
			}
		}
	}
}

func assertSSRustJSONEqual(t *testing.T, before, after map[string]json.RawMessage) {
	t.Helper()
	for key, value := range before {
		var a, b any
		_ = json.Unmarshal(value, &a)
		_ = json.Unmarshal(after[key], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("global %s changed", key)
		}
	}
	if len(before) != len(after) {
		t.Error("root keys changed")
	}
}
