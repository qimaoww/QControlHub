package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSSRustNamesPersistAndKeepPortIdentity(t *testing.T) {
	const original = `{"dns":"9.9.9.9","unknown":9007199254740993,"servers":[{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"password-one","acl":"/first.acl"},{"server":"::","server_port":20002,"method":"aes-256-gcm","password":"password-two"}]}`
	for _, scoped := range []bool{true, false} {
		t.Run(map[bool]string{true: "scoped", false: "legacy"}[scoped], func(t *testing.T) {
			mutate := func(current, generated, tag, operation string) (string, error) {
				if scoped {
					return MutateSSRustPort(current, generated, tag, operation)
				}
				return MutateGenerated(core.EngineShadowsocksRust, current, generated, tag, operation)
			}
			plan := ParseAll(core.EngineShadowsocksRust, original)[0]
			plan.Credential = "password-one-long-enough"
			plan.Tag = "香港 · ATT"
			generated, err := Generate(core.EngineShadowsocksRust, plan)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := mutate(original, generated, "ss-rust-1", "modify")
			if err != nil {
				t.Fatal(err)
			}
			inputs := ParseAll(core.EngineShadowsocksRust, changed)
			if len(inputs) != 2 || inputs[0].Tag != plan.Tag || inputs[1].Tag != "ss-rust-2" || inputs[1].Port != 20002 {
				t.Fatalf("names lost: %+v", inputs)
			}
			if scoped && !strings.Contains(changed, "9007199254740993") {
				t.Fatal("unknown integer changed")
			}
			if !strings.Contains(changed, "/first.acl") {
				t.Fatal("rename changed ACL")
			}
			plan.Tag = inputs[1].Tag
			duplicate, _ := Generate(core.EngineShadowsocksRust, plan)
			if _, err := mutate(changed, duplicate, inputs[0].Tag, "modify"); err == nil {
				t.Fatal("duplicate name accepted")
			}
			deleted, err := mutate(changed, generated, inputs[0].Tag, "delete")
			if err != nil {
				t.Fatal(err)
			}
			remaining := ParseAll(core.EngineShadowsocksRust, deleted)
			if len(remaining) != 1 || remaining[0].Tag != "ss-rust-2" {
				t.Fatalf("delete renamed another port: %+v", remaining)
			}
			plan.Tag, plan.Port = "new-port", 20003
			generated, _ = Generate(core.EngineShadowsocksRust, plan)
			added, err := mutate(deleted, generated, "", "add")
			if err != nil {
				t.Fatal(err)
			}
			inputs = ParseAll(core.EngineShadowsocksRust, added)
			if len(inputs) != 2 || inputs[1].Tag != "new-port" || inputs[0].Tag != "ss-rust-2" {
				t.Fatalf("add lost identity: %+v", inputs)
			}
		})
	}
}

func TestSSRustSingleServerRenameAndNameValidation(t *testing.T) {
	const original = `{"server":"::","server_port":20001,"method":"aes-256-gcm","password":"password-one","plugin":"custom-plugin","dns":"9.9.9.9"}`
	plan := ParseAll(core.EngineShadowsocksRust, original)[0]
	plan.Credential = "password-one-long-enough"
	for _, name := range []string{"", " ", "bad\nname", "bad\x00name", strings.Repeat("名", 65)} {
		plan.Tag = name
		if _, err := Generate(core.EngineShadowsocksRust, plan); err == nil {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
	plan.Tag = strings.Repeat("名", 64)
	generated, err := Generate(core.EngineShadowsocksRust, plan)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := MutateSSRustPort(original, generated, "ss-rust", "modify")
	if err != nil {
		t.Fatal(err)
	}
	inputs := ParseAll(core.EngineShadowsocksRust, changed)
	if len(inputs) != 1 || inputs[0].Tag != plan.Tag || !strings.Contains(changed, "custom-plugin") {
		t.Fatalf("single rename failed: %s", changed)
	}
	if _, err := MergeSSRustInboundField(changed, plan.Tag, "mode", `"tcp_only"`, false); err != nil {
		t.Fatal(err)
	}
}
