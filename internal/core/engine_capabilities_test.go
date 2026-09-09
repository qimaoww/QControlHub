package core

import (
	"reflect"
	"testing"
)

func TestParseDefaultAgentEngines(t *testing.T) {
	for _, input := range []string{"", "xray,", "xray,xray", "unknown", "none,xray"} {
		if _, err := ParseDefaultAgentEngines(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	for input, want := range map[string][]Engine{
		"none": {}, " xray, sing-box ": {EngineXray, EngineSingBox},
		"mihomo,xray,sing-box,ss-rust": AllEngines(),
	} {
		got, err := ParseDefaultAgentEngines(input)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("parse %q = %v, %v", input, got, err)
		}
	}
	if got := IntersectEngines([]Engine{EngineXray, EngineMihomo}, []Engine{EngineMihomo}); !reflect.DeepEqual(got, []Engine{EngineMihomo}) {
		t.Fatalf("intersection = %v", got)
	}
}
