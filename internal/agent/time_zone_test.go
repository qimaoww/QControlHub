package agent

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAgentTimeZoneRestartDecisionIsProcessScopedAndIdempotent(t *testing.T) {
	for _, test := range []struct {
		name, zone, processTZ, localZone string
		want                             bool
	}{
		{"matching process", "Asia/Shanghai", "Asia/Shanghai", "Asia/Shanghai", false},
		{"missing process setting", "Asia/Shanghai", "", "Asia/Shanghai", true},
		{"different process setting", "UTC", "Asia/Shanghai", "Asia/Shanghai", true},
		{"different effective location", "UTC", "UTC", "Asia/Shanghai", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := agentTimeZoneRestartNeeded(test.zone, test.processTZ, test.localZone); got != test.want {
				t.Fatalf("restart needed=%v, want %v", got, test.want)
			}
		})
	}
}

func TestPrepareAgentTimeZoneDoesNotMutateProcessOrHost(t *testing.T) {
	previousLocation := time.Local
	client := &Client{}
	restart, err := client.prepareAgentTimeZone(core.AgentTimeZoneCommand{Revision: 1, TimeZone: "America/New_York"})
	if err != nil || !restart {
		t.Fatalf("prepare restart=%v error=%v, want process replacement", restart, err)
	}
	if time.Local != previousLocation || client.timeZoneRevision != 0 {
		t.Fatal("preparing a time zone changed the running process before replacement")
	}
	if _, err := client.prepareAgentTimeZone(core.AgentTimeZoneCommand{Revision: 2, TimeZone: "not/a-zone"}); err == nil {
		t.Fatal("invalid IANA zone was accepted")
	}
	client.timeZoneRevision = 3
	if _, err := client.prepareAgentTimeZone(core.AgentTimeZoneCommand{Revision: 2, TimeZone: "UTC"}); err == nil {
		t.Fatal("stale revision was accepted")
	}
}

func TestAgentTimeZoneRestartSetsOnlyAgentEnvironment(t *testing.T) {
	previousLocation := time.Local
	t.Setenv("TZ", "UTC")
	var executable string
	var environment []string
	client := &Client{reexecFunc: func(path string, _ []string, env []string) error {
		executable, environment = path, append([]string(nil), env...)
		return errors.New("simulated process replacement failure")
	}}
	err := client.restartForAgentTimeZone("Asia/Tokyo")
	if err == nil || !strings.Contains(err.Error(), "simulated process replacement failure") {
		t.Fatalf("restart error = %v", err)
	}
	if executable == "" {
		t.Fatal("Agent executable was not passed to process replacement")
	}
	if !containsEnvironmentValue(environment, "TZ", "Asia/Tokyo") {
		t.Fatal("replacement environment did not contain the requested Agent process zone")
	}
	if time.Local != previousLocation {
		t.Fatal("process replacement preparation mutated the live Go location")
	}
	if os.Getenv("TZ") != "UTC" {
		t.Fatal("failed process replacement changed the running Agent environment")
	}
}

func containsEnvironmentValue(environment []string, key, want string) bool {
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if found && name == key {
			return value == want
		}
	}
	return false
}
