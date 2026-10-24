//go:build linux

package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (collector *CoreLogCollector) runSource(ctx context.Context, source coreLogJournalSource) {
	resume := ""
	recoverySince := time.Now().UTC()
	source.recoverySince = &recoverySince
	for ctx.Err() == nil {
		nextResume, err := collector.follow(ctx, source, resume)
		if nextResume != "" {
			resume = nextResume
		}
		if ctx.Err() != nil {
			return
		}
		slog.Warn("managed core journal reader stopped", "error", err)
		for unit, engine := range source.unitEngines {
			collector.reportJournalSourceFailure(source, unit, engine, err)
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (collector *CoreLogCollector) follow(ctx context.Context, source coreLogJournalSource, resume string) (string, error) {
	if resume == "" {
		var err error
		resume, err = captureJournalPosition(ctx, source)
		if err != nil {
			return "", err
		}
	}
	command := exec.CommandContext(ctx, journalctlPath, journalFollowArguments(source.arguments, resume)...)
	configureCommand(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return resume, err
	}
	output := &boundedOutput{limit: 8 << 10}
	command.Stderr = output
	if err := command.Start(); err != nil {
		return resume, err
	}
	// The separately captured tail position makes this readiness declaration
	// safe: records written after that cursor (or the bounded timestamp used for
	// an empty journal) remain readable while journalctl completes its startup.
	// A started follower is still no proof of delivery, so report waiting until
	// the first decoded record arrives instead of claiming active.
	collector.reportJournalSourceArmed(source)
	lastResume := resume
	recoverySince := time.Now().UTC()
	if source.recoverySince != nil {
		recoverySince = *source.recoverySince
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 16<<10), 256<<10)
	for scanner.Scan() {
		entry, cursor, ok := decodeJournalCoreLog(scanner.Bytes(), source.unitEngines)
		if validJournalCursor(cursor) {
			lastResume = "--after-cursor=" + cursor
		}
		if ok {
			recoverySince = entry.LoggedAt
			if source.recoverySince != nil {
				*source.recoverySince = recoverySince
			}
			collector.markJournalDelivery(entry, source.kind)
			collector.appendJournal(entry, cursor)
		}
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return lastResume, ctx.Err()
	}
	if scanErr != nil {
		return lastResume, scanErr
	}
	if waitErr != nil {
		message := strings.TrimSpace(output.String())
		if strings.HasPrefix(resume, "--after-cursor=") && strings.Contains(message, "Failed to seek to cursor") {
			// Journal vacuum can retire the saved cursor while readers restart.
			// Resume at the last observed timestamp, including one second of
			// overlap; the existing per-source cursor set removes duplicates.
			recoverySince = recoverySince.Add(-time.Second)
			lastResume = fmt.Sprintf("--since=@%d.%06d", recoverySince.Unix(), recoverySince.Nanosecond()/int(time.Microsecond))
		}
		if message != "" {
			return lastResume, errors.New(message)
		}
		return lastResume, waitErr
	}
	return lastResume, io.EOF
}

// reportJournalSourceArmed publishes the armed-but-undelivered state of a
// journal follower. The source may still be quiet, so the reason is either
// awaiting-first-line or service-inactive when the unit is provably stopped.
func (collector *CoreLogCollector) reportJournalSourceArmed(source coreLogJournalSource) {
	for unit, engine := range source.unitEngines {
		code := core.CoreLogErrorAwaitingFirstLog
		if collector.coreLogServiceIdleForSource(source, unit) {
			code = core.CoreLogErrorServiceInactive
		}
		collector.reportJournalSourceStatus(source, unit, engine, core.CoreLogStatusWaiting, code)
	}
}

// coreLogServiceIdleForSource limits the service probe to the collector's own
// journal sources. A shared follower covers every instance on the node and
// probing each instance unit on every (re)start would scale with instance
// count; instance health is reported separately per instance instead.
func (collector *CoreLogCollector) coreLogServiceIdleForSource(source coreLogJournalSource, unit string) bool {
	if source.report != nil {
		return false
	}
	return collector.coreLogServiceIdle(unit)
}

// reportJournalSourceFailure keeps a stopped unit out of the failed state: an
// idle service cannot write records, so an empty or unreachable journal is not
// a read failure. Permission and helper failures keep their failed code.
func (collector *CoreLogCollector) reportJournalSourceFailure(source coreLogJournalSource, unit string, engine core.Engine, err error) {
	code := coreLogErrorCode(err)
	if code == "collector-failed" && collector.coreLogServiceIdleForSource(source, unit) {
		collector.reportJournalSourceStatus(source, unit, engine, core.CoreLogStatusWaiting, core.CoreLogErrorServiceInactive)
		return
	}
	collector.reportJournalSourceStatus(source, unit, engine, core.CoreLogStatusFailed, code)
}

func (collector *CoreLogCollector) reportJournalSourceStatus(source coreLogJournalSource, unit string, engine core.Engine, status, code string) {
	if source.report != nil {
		source.report(unit, engine, status, code)
		return
	}
	collector.setSourceStatus(engine, source.kind, status, code)
}

// markJournalDelivery records a decoded record for the instance that owns it. A
// shared follower must credit the instance collector, not the parent, so a
// broken instance never masks a healthy sibling.
func (collector *CoreLogCollector) markJournalDelivery(entry core.CoreLogEntry, kind string) {
	target := collector
	if entry.InstanceID != "main" {
		collector.instancesMu.Lock()
		child := collector.instances[string(entry.Engine)+"/"+entry.InstanceID]
		collector.instancesMu.Unlock()
		if child != nil {
			target = child.collector
		}
	}
	target.markCoreLogDelivery(entry.Engine, kind)
}

func captureJournalPosition(ctx context.Context, source coreLogJournalSource) (string, error) {
	// Record the bounded fallback before probing the journal tail. A fresh
	// namespace may not contain any entry and therefore cannot return a cursor;
	// --since still covers every record created while the follower is starting.
	capturedAt := time.Now().UTC()
	command := exec.CommandContext(ctx, journalctlPath, journalCursorArguments(source.arguments)...)
	configureCommand(command)
	stdout := &boundedOutput{limit: 16 << 10}
	stderr := &boundedOutput{limit: 8 << 10}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		var exit *exec.ExitError
		empty := errors.As(err, &exit) && exit.ExitCode() == 1 && message == "" && strings.TrimSpace(stdout.String()) == ""
		if !empty {
			if message != "" {
				return "", errors.New(message)
			}
			return "", err
		}
		// journalctl --quiet exits 1 for an empty/expired namespace. The
		// timestamp fixed before this probe still arms a no-history reader.
	}
	if stdout.Truncated() || stderr.Truncated() {
		return "", errors.New("journal cursor response exceeded the output limit")
	}
	lines := strings.Split(stdout.String(), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, "-- cursor:") {
			continue
		}
		cursor := strings.TrimSpace(strings.TrimPrefix(line, "-- cursor:"))
		if validJournalCursor(cursor) {
			return "--after-cursor=" + cursor, nil
		}
		return "", errors.New("journal cursor response was invalid")
	}
	seconds := capturedAt.Unix()
	microseconds := capturedAt.Nanosecond() / int(time.Microsecond)
	return fmt.Sprintf("--since=@%d.%06d", seconds, microseconds), nil
}

func journalCursorArguments(arguments []string) []string {
	result := make([]string, 0, len(arguments)+4)
	for _, argument := range arguments {
		if argument == "--follow" || strings.HasPrefix(argument, "--lines") ||
			strings.HasPrefix(argument, "--output") || strings.HasPrefix(argument, "--after-cursor=") ||
			strings.HasPrefix(argument, "--since=") || strings.HasPrefix(argument, "--unit=") {
			continue
		}
		result = append(result, argument)
	}
	return append(result, "--no-pager", "--quiet", "--lines=0", "--show-cursor")
}

func journalFollowArguments(arguments []string, resume string) []string {
	result := make([]string, 0, len(arguments)+1)
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "--lines") || strings.HasPrefix(argument, "--after-cursor=") ||
			strings.HasPrefix(argument, "--since=") {
			continue
		}
		result = append(result, argument)
	}
	return append(result, resume)
}

func validJournalCursor(cursor string) bool {
	if cursor == "" || len(cursor) > 4096 {
		return false
	}
	for _, character := range cursor {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
