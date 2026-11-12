//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/networkdiag"
)

// RunNetworkToolPreparation is a fixed, root-owned helper. Serialize it with
// existing dependency preparation so package-manager locks cannot race.
func RunNetworkToolPreparation(ctx context.Context, tool string) error {
	if tool != "nexttrace" && tool != "iperf3" {
		return errors.New("不支持的诊断依赖")
	}
	lock, err := acquireIPQualityDependencyLock(ctx, "/run/qagent-ip-quality-dependencies")
	if err != nil {
		return err
	}
	defer lock.Close()
	return networkdiag.PrepareTool(ctx, tool)
}

func prepareNetworkTool(ctx context.Context, tool string) error {
	if tool != "nexttrace" && tool != "iperf3" {
		return errors.New("不支持的诊断依赖")
	}
	// The built-in engine must be checked in the actual Agent context. The
	// package installation helper deliberately has a different capability set.
	if tool == "nexttrace" {
		return networkdiag.PrepareTool(ctx, tool)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute+10*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = ipQualityExecutable(executable)
	if err != nil {
		return err
	}
	if _, err := os.Stat("/run/systemd/system"); errors.Is(err, os.ErrNotExist) {
		output, err := runIPQualityCommand(ctx, executable, []string{"prepare-network-tool", tool})
		if err != nil {
			return fmt.Errorf("%s: %w", core.DiagnosticText(output), err)
		}
		return nil
	}
	runner, err := ipQualityExecutable("/usr/bin/systemd-run")
	if err != nil {
		return fmt.Errorf("systemd 安装服务不可用：%w", err)
	}
	suffix, err := randomSuffix(8)
	if err != nil {
		return err
	}
	unit := "qagent-network-tools-" + suffix + ".service"
	args := ipQualityDependencyHelperArguments(executable, unit)
	args[len(args)-1] = "prepare-network-tool"
	args = append(args, tool)
	output, runErr := runIPQualityCommand(ctx, runner, args)
	cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if systemctl, err := ipQualityExecutable("/usr/bin/systemctl"); err == nil {
		if _, err := runIPQualityCommand(cleanup, systemctl, []string{"stop", unit}); err != nil && ctx.Err() != nil {
			return fmt.Errorf("安装服务停止未确认，将由存活期限回收：%w", err)
		}
	}
	if runErr != nil {
		return fmt.Errorf("安装服务失败：%w；%s", runErr, strings.TrimSpace(core.DiagnosticText(output)))
	}
	return nil
}
