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
)

func (collector *CoreLogCollector) runSource(ctx context.Context, source coreLogJournalSource) {
	resume := ""
	for ctx.Err() == nil {
		nextResume, err := collector.follow(ctx, source, resume)
		if nextResume != "" {
			resume = nextResume
		}
		if ctx.Err() != nil {
			return
		}
		slog.Warn("managed core journal reader stopped", "error", err)
		for _, engine := range source.unitEngines {
			collector.setSourceStatus(engine, "journal", "failed", coreLogErrorCode(err))
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
	for _, engine := range source.unitEngines {
		collector.setSourceStatus(engine, "journal", "active", "")
	}
	lastResume := resume
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 16<<10), 256<<10)
	for scanner.Scan() {
		entry, cursor, ok := decodeJournalCoreLog(scanner.Bytes(), source.unitEngines)
		if validJournalCursor(cursor) {
			lastResume = "--after-cursor=" + cursor
		}
		if ok {
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
		if message != "" {
			return lastResume, errors.New(message)
		}
		return lastResume, waitErr
	}
	return lastResume, io.EOF
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
		if message != "" {
			return "", errors.New(message)
		}
		return "", err
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
