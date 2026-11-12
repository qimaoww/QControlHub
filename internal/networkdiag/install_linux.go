//go:build linux

package networkdiag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func Prepare(ctx context.Context) error {
	if err := PrepareTool(ctx, "iperf3"); err != nil {
		return err
	}
	return PrepareTool(ctx, "nexttrace")
}

// PrepareTool accepts only fixed dependencies; task input cannot name packages,
// repositories, paths or command arguments.
func PrepareTool(ctx context.Context, tool string) error {
	if tool != "iperf3" && tool != "nexttrace" {
		return errors.New("不支持的诊断依赖")
	}
	if tool == "nexttrace" {
		// Compatibility for the fixed preparation helper: the engine ships in
		// the Agent. Never download or touch a legacy standalone CLI.
		if capability := nextTraceCapability(ctx); capability.Error != "" {
			return errors.New(capability.Error)
		}
		return nil
	}
	if os.Geteuid() != 0 {
		return errors.New("工具准备需要 root")
	}
	if tool == "iperf3" {
		if _, err := exec.LookPath("iperf3"); err != nil {
			var command string
			var args []string
			for _, plan := range [][]string{{"apt-get", "install", "-y", "--no-install-recommends", "iperf3"}, {"apk", "add", "--no-cache", "iperf3"}, {"dnf", "install", "-y", "iperf3"}, {"yum", "install", "-y", "iperf3"}, {"zypper", "--non-interactive", "install", "iperf3"}} {
				if _, e := exec.LookPath(plan[0]); e == nil {
					command = plan[0]
					args = plan[1:]
					break
				}
			}
			if command == "" {
				return errors.New("请使用发行版包管理器安装 iperf3")
			}
			if command == "apt-get" {
				update := exec.CommandContext(ctx, command, "update")
				update.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
				update.Stdout, update.Stderr = os.Stdout, os.Stderr
				if err := update.Run(); err != nil {
					return fmt.Errorf("更新 iperf3 软件源索引失败：%w", err)
				}
			}
			cmd := exec.CommandContext(ctx, command, args...)
			cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err = cmd.Run(); err != nil {
				return fmt.Errorf("install iperf3: %w", err)
			}
		}
		return nil
	}
	return nil
}
