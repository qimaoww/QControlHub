package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func serviceCommand(ctx context.Context, service string, action core.Action) (string, error) {
	return defaultSystemdServiceManager().command(ctx, service, action)
}

func serviceCommandAndVerify(ctx context.Context, service string, action core.Action) (string, error) {
	return serviceCommandAndVerifyWithManager(ctx, defaultSystemdServiceManager(), service, action)
}

func serviceCommandAndVerifyWithManager(ctx context.Context, manager *ServiceManager, service string, action core.Action) (string, error) {
	output, err := manager.command(ctx, service, action)
	if err != nil {
		return output, err
	}
	if action == core.ActionStop && manager.Kind() == ServiceManagerSystemd {
		status, statusErr := manager.status(ctx, service)
		if statusErr != nil {
			return output, withServiceFailureDiagnostics(ctx, manager, service, statusErr)
		}
		if status == "failed" {
			if err := manager.resetFailed(ctx, service); err != nil {
				return output, withServiceFailureDiagnostics(ctx, manager, service, err)
			}
		}
	}
	expected := "active"
	stableFor := 500 * time.Millisecond
	verificationTimeout := 12 * time.Second
	if action == core.ActionStop {
		expected = "inactive"
		stableFor = 0
		verificationTimeout = 5 * time.Second
	}
	verifyContext, verifyCancel := context.WithTimeout(ctx, verificationTimeout)
	status, statusErr := waitForServiceState(verifyContext, expected, stableFor, 100*time.Millisecond, func(probeContext context.Context) (string, error) {
		return manager.status(probeContext, service)
	})
	verifyCancel()
	if statusErr != nil {
		cause := fmt.Errorf("verify %s service %s after %s: %w", manager.Kind(), service, action, statusErr)
		return output, withServiceFailureDiagnostics(ctx, manager, service, cause)
	}
	if status != expected {
		cause := fmt.Errorf("%s service %s is %s after %s, expected %s", manager.Kind(), service, status, action, expected)
		return output + "\nservice status: " + status, withServiceFailureDiagnostics(ctx, manager, service, cause)
	}
	return output + "\nservice status: " + status, nil
}

func withServiceFailureDiagnostics(ctx context.Context, manager *ServiceManager, service string, cause error) error {
	if manager == nil || manager.Kind() != ServiceManagerSystemd {
		return cause
	}
	diagnosticContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, _ := run(diagnosticContext, manager.executable, "show", service, "--no-pager",
		"--property=ActiveState", "--property=SubState", "--property=Result",
		"--property=ExecMainCode", "--property=ExecMainStatus", "--property=NRestarts")
	output = strings.TrimSpace(strings.ToValidUTF8(output, "�"))
	journal := serviceFailureJournal(diagnosticContext, service)
	if len(output) > 2048 {
		output = strings.ToValidUTF8(output[:2048], "�")
	}
	if output == "" && journal == "" {
		return cause
	}
	details := ""
	if output != "" {
		details = "unit state:\n" + output
	}
	if journal != "" {
		if details != "" {
			details += "\n"
		}
		details += "recent unit logs:\n" + journal
	}
	return fmt.Errorf("%w; %s", cause, details)
}

func serviceFailureJournal(ctx context.Context, service string) string {
	if !safeServiceName(service) {
		return ""
	}
	arguments := []string{"--unit=" + service, "--since=-30s", "--lines=20", "--no-pager", "--output=cat"}
	outputs := make([]string, 0, 2)
	for _, prefix := range [][]string{{"--namespace=qagent-cores"}, nil} {
		current, _ := run(ctx, journalctlPath, append(prefix, arguments...)...)
		current = strings.TrimSpace(strings.ToValidUTF8(current, "�"))
		if current == "" || len(outputs) > 0 && current == outputs[0] {
			continue
		}
		outputs = append(outputs, current)
	}
	output := strings.Join(outputs, "\n")
	if len(output) > 4096 {
		output = strings.ToValidUTF8(output[len(output)-4096:], "�")
	}
	return output
}

type serviceStatusProbe func(context.Context) (string, error)

func waitForServiceState(ctx context.Context, expected string, stableFor, pollEvery time.Duration, probe serviceStatusProbe) (string, error) {
	if probe == nil || pollEvery <= 0 || stableFor < 0 {
		return "", errors.New("invalid service verification settings")
	}
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	var stableSince time.Time
	lastStatus := "unknown"
	for {
		status, err := probe(ctx)
		if err != nil {
			return lastStatus, err
		}
		lastStatus = status
		if status == expected {
			if stableFor == 0 {
				return status, nil
			}
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= stableFor {
				return status, nil
			}
		} else {
			stableSince = time.Time{}
			if status == "failed" || status == "inactive" || (expected == "inactive" && status == "active") {
				return status, nil
			}
		}
		select {
		case <-ctx.Done():
			return lastStatus, ctx.Err()
		case <-ticker.C:
		}
	}
}

func serviceStatus(ctx context.Context, service string) (string, error) {
	return serviceStatusWithManager(ctx, defaultSystemdServiceManager(), service)
}

func serviceStatusWithManager(ctx context.Context, manager *ServiceManager, service string) (string, error) {
	return manager.status(ctx, service)
}
