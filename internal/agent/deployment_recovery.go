package agent

import (
	"context"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// A missing or explicitly empty previous configuration cannot be recovered by
// restarting it. Heartbeats intentionally stop empty shared instances, too.
func recoverConfigurationDeployment(ctx context.Context, engine core.Engine, spec EngineSpec, services *ServiceManager, backup, previousStatus string) (string, error) {
	shouldRun := backup != "" && (previousStatus == "active" || previousStatus == "activating" || previousStatus == "reloading")
	if shouldRun {
		content, err := readConfigurationFile(backup)
		if err != nil {
			return "", fmt.Errorf("read rollback configuration: %w", err)
		}
		if plan, err := serverconfig.PrepareIndependentEgress(engine, content); err == nil && plan.Source == "disabled" {
			shouldRun = false
		}
	}
	var output string
	if !shouldRun {
		var err error
		output, err = serviceCommandAndVerifyWithManager(ctx, services, spec.Service, core.ActionStop)
		if err != nil {
			return output, fmt.Errorf("stop failed deployment before restoring inactive state: %w", err)
		}
	}
	rollbackOutput, err := rollbackDeploy(spec.ConfigPath, backup)
	output += "\n" + rollbackOutput
	if err != nil {
		return output, fmt.Errorf("restore configuration: %w", err)
	}
	if shouldRun {
		restartOutput, err := serviceCommandAndVerifyWithManager(ctx, services, spec.Service, core.ActionRestart)
		return output + "\nrollback restart: " + restartOutput, err
	}
	return output + "\nrollback: service remains inactive", nil
}
