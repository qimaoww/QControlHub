package core

import (
	"strings"
	"testing"
)

func TestPanelSettingsValidation(t *testing.T) {
	t.Parallel()
	settings := DefaultPanelSettings()
	if err := settings.Validate(); err != nil {
		t.Fatalf("default settings are invalid: %v", err)
	}
	if settings.CoreLogMinimumLevel != "debug" {
		t.Fatalf("default core log minimum level = %q", settings.CoreLogMinimumLevel)
	}
	settings.PanelName = strings.Repeat("界", 40)
	if err := settings.Validate(); err != nil {
		t.Fatalf("40-character panel name rejected: %v", err)
	}
	settings.PanelName += "面"
	if err := settings.Validate(); err == nil {
		t.Fatal("41-character panel name was accepted")
	}
	settings = DefaultPanelSettings()
	settings.TaskPollIntervalMS = 750
	if err := settings.Validate(); err == nil {
		t.Fatal("unsupported task polling interval was accepted")
	}

	settings = DefaultPanelSettings()
	settings.CoreLogMinimumLevel = "off"
	if err := settings.Validate(); err != nil {
		t.Fatalf("disabled core log persistence was rejected: %v", err)
	}
	settings.CoreLogMinimumLevel = "trace"
	if err := settings.Validate(); err == nil {
		t.Fatal("unsupported core log minimum level was accepted")
	}

	settings = DefaultPanelSettings()
	settings.WebhookURL = "https://hooks.example.com/qcontrolhub"
	if err := settings.Validate(); err != nil {
		t.Fatalf("valid webhook URL rejected: %v", err)
	}
	for _, invalid := range []string{"ftp://example.com/hook", "not-a-url", "https://", "http://exa mple.com"} {
		settings.WebhookURL = invalid
		if err := settings.Validate(); err == nil {
			t.Fatalf("invalid webhook URL %q was accepted", invalid)
		}
	}
	settings.WebhookURL = ""
	if err := settings.Validate(); err != nil {
		t.Fatalf("empty webhook URL must stay valid: %v", err)
	}

	settings = DefaultPanelSettings()
	settings.AgentCoreLogMaxMiB = 1
	settings.AgentCoreLogRotateCount = 0
	if err := settings.Validate(); err != nil {
		t.Fatalf("minimum local core log policy rejected: %v", err)
	}
	settings.AgentHeartbeatIntervalSeconds = 30
	settings.AgentOfflineThresholdSeconds = 60
	if err := settings.Validate(); err == nil {
		t.Fatal("offline threshold below three heartbeat intervals was accepted")
	}
}

func TestAgentPolicyValidation(t *testing.T) {
	t.Parallel()
	policy := AgentPolicy{HeartbeatIntervalSeconds: 15, MetricsIntervalSeconds: 1, CoreLogMaxMiB: 1, CoreLogRotateCount: 0}
	if err := policy.Validate(); err != nil {
		t.Fatalf("valid minimum policy rejected: %v", err)
	}
	policy.CoreLogMaxMiB = 3
	if err := policy.Validate(); err == nil {
		t.Fatal("unsupported local log capacity was accepted")
	}
}
