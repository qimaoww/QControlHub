package api

import (
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestVersionOlder(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		current, latest   string
		older, comparable bool
	}{
		{"v1.2.3", "v1.2.4", true, true},
		{"1.3.0", "v1.2.9", false, true},
		{"v2.0.0-rc.1", "v2.0.0", false, false},
		{"b057422", "v1.2.3", false, false},
	} {
		older, comparable := versionOlder(test.current, test.latest)
		if older != test.older || comparable != test.comparable {
			t.Errorf("versionOlder(%q,%q) = %v,%v; want %v,%v", test.current, test.latest, older, comparable, test.older, test.comparable)
		}
	}
}

func TestAgentPolicyChanged(t *testing.T) {
	t.Parallel()
	left := core.DefaultPanelSettings()
	right := left
	right.PanelName = "cosmetic"
	if agentPolicyChanged(left, right) {
		t.Fatal("cosmetic settings change reconnects agents")
	}
	right.AgentCoreLogMaxMiB = 1
	if !agentPolicyChanged(left, right) {
		t.Fatal("local log policy change did not reconnect agents")
	}
}

func TestImageVersionStatus(t *testing.T) {
	t.Parallel()
	latest := "b0574228ed73fb1d2f0aad4a38f985626d3775ec"
	for _, test := range []struct {
		current               string
		available, comparable bool
	}{
		{"b057422", false, true},
		{latest, false, true},
		{"73ecb6d", true, true},
		{"dev", false, false},
	} {
		available, comparable := imageVersionStatus(test.current, latest)
		if available != test.available || comparable != test.comparable {
			t.Errorf("imageVersionStatus(%q) = %v,%v; want %v,%v", test.current, available, comparable, test.available, test.comparable)
		}
	}
}
