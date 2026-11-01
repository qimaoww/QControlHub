package agent

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (c *Client) prepareAgentTimeZone(command core.AgentTimeZoneCommand) (bool, error) {
	if command.Revision < 1 || command.Revision < c.timeZoneRevision || !core.ValidTimeZone(command.TimeZone) {
		return false, errors.New("invalid or stale IANA time zone")
	}
	if command.Revision == c.timeZoneRevision && command.TimeZone != c.timeZone {
		return false, errors.New("conflicting Agent time zone revision")
	}
	if agentTimeZoneRestartNeeded(command.TimeZone, os.Getenv("TZ"), time.Local.String()) {
		return true, nil
	}
	c.timeZoneRevision, c.timeZone = command.Revision, command.TimeZone
	return false, nil
}

func agentTimeZoneRestartNeeded(zone, processTZ, localZone string) bool {
	return processTZ != zone || localZone != zone
}

func (c *Client) restartForAgentTimeZone(zone string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve Agent executable: %w", err)
	}
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "TZ=") {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, "TZ="+zone)
	reexec := c.reexecFunc
	if reexec == nil {
		reexec = syscall.Exec
	}
	if err := reexec(executable, os.Args, environment); err != nil {
		return fmt.Errorf("restart Agent process to apply time zone: %w", err)
	}
	return errors.New("Agent process replacement unexpectedly returned")
}
