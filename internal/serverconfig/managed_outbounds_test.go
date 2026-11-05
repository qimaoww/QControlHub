package serverconfig

import (
	"github.com/qimaoww/qcontrolhub/internal/core"
	"strings"
	"testing"
)

func TestManagedOutboundMutationsPreserveNativeSourceAndReferences(t *testing.T) {
	for _, tt := range []struct {
		engine                          core.Engine
		source, section, fragment, next string
	}{
		{core.EngineXray, `{"future":9007199254740993,"inbounds":[],"outbounds":[{"tag":"default","protocol":"freedom"},{"tag":"old","protocol":"freedom","future":9007199254740993}]}`, "outbounds", `{"tag":"new","protocol":"freedom","future":9007199254740993}`, "new"},
		{core.EngineSingBox, `{"future":9007199254740993,"inbounds":[],"outbounds":[{"tag":"default","type":"direct"},{"tag":"old","type":"direct"}],"route":{"final":"default"}}`, "outbounds", `{"tag":"new","type":"direct","future":9007199254740993}`, "new"},
		{core.EngineMihomo, "# root comment\nfuture: 9007199254740993\nlisteners: []\nproxies: [{name: old, type: direct}]\nrules: ['MATCH,DIRECT']\n", "proxies", "name: new\ntype: direct\nfuture: 9007199254740993\n", "new"},
	} {
		t.Run(string(tt.engine), func(t *testing.T) {
			before, err := ManagedOutboundFragment(tt.engine, tt.source, tt.section, "old")
			if err != nil || !strings.Contains(before, "old") {
				t.Fatalf("fragment: %s %v", before, err)
			}
			state := ManagedState{Disabled: []ManagedResource{{tt.section, "old"}}}
			result, state, err := MutateManagedOutbound(tt.engine, tt.source, tt.section, "old", tt.fragment, "modify", state)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result, "9007199254740993") || state.Disabled[0].Tag != tt.next {
				t.Fatalf("precision/state lost: %s %+v", result, state)
			}
			if tt.engine == core.EngineMihomo && !strings.Contains(result, "# root comment") {
				t.Fatal("YAML comment lost")
			}
			result, state, err = MutateManagedOutbound(tt.engine, result, tt.section, tt.next, "", "delete", state)
			if err != nil || len(state.Disabled) != 0 || strings.Contains(result, "\"new\"") {
				t.Fatalf("delete: %s %+v %v", result, state, err)
			}
			result, _, err = MutateManagedOutbound(tt.engine, result, tt.section, "", tt.fragment, "add", state)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = MutateManagedOutbound(tt.engine, result, tt.section, "", tt.fragment, "add", state); err == nil {
				t.Fatal("duplicate exit accepted")
			}
		})
	}
}

func TestManagedOutboundRejectsDefaultSharedAndStaleDeletion(t *testing.T) {
	source := `{"inbounds":[],"outbounds":[{"tag":"default","protocol":"freedom"},{"tag":"shared","protocol":"freedom"}],"routing":{"rules":[{"inboundTag":["one","two"],"outboundTag":"shared"}]}}`
	for _, tag := range []string{"default", "shared", "missing"} {
		if _, _, err := MutateManagedOutbound(core.EngineXray, source, "outbounds", tag, "", "delete", ManagedState{}); err == nil {
			t.Fatalf("unsafe delete accepted: %s", tag)
		}
	}
	if _, _, err := MutateManagedOutbound(core.EngineXray, source, "inbounds", "", "{}", "add", ManagedState{}); err == nil {
		t.Fatal("outbound operation could edit inbounds")
	}
	yaml := "listeners: [{name: one, type: socks, port: 23111, proxy: shared}]\nproxies: [{name: shared, type: direct}]\nrules: ['MATCH,DIRECT']\n"
	if _, _, err := MutateManagedOutbound(core.EngineMihomo, yaml, "proxies", "shared", "", "delete", ManagedState{}); err == nil {
		t.Fatal("listener proxy reference dropped")
	}
}
