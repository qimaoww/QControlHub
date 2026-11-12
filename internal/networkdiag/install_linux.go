//go:build linux

package networkdiag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// These digests are the v1.7.6 GitHub release asset digests. Installation is
// performed by the bounded privileged helper and never replaces a user's tool.
var nextTraceAssets = map[string]string{
	"amd64": "3868c8190c961b534b8f2d0ed528796fc48d46a818ee5268fe2dfc6aa44b3a5e",
	"arm64": "e4c4e818f6cebf8d8ee3df3976a304c8bac2bb3bea3160a23fd5bce18095c9e6",
	"armv7": "fa75a10ec7b82fa19c18af5e49ffaa4383433a29503001c792708dccec044f70",
}

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
	arch := runtime.GOARCH
	if arch == "arm" {
		arch = "armv7"
	}
	digest, ok := nextTraceAssets[arch]
	if !ok {
		return fmt.Errorf("NextTrace 未提供本项目已校验的架构：%s", arch)
	}
	if b, err := os.ReadFile(NextTracePath); err == nil {
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) == digest {
			return nil
		}
		return errors.New("私有工具路径已有其他文件，拒绝覆盖")
	}
	dir := filepath.Dir(NextTracePath)
	for _, p := range []string{"/usr/local/lib/qagent", dir, NextTracePath} {
		if info, e := os.Lstat(p); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("拒绝符号链接工具路径")
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/nxtrace/NTrace-core/releases/download/v1.7.6/nexttrace_linux_"+arch, nil)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 120 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("NextTrace download: HTTP %d", response.StatusCode)
	}
	f, err := os.CreateTemp(dir, ".nexttrace-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(response.Body, 100<<20))
	if err != nil {
		return err
	}
	if n == 100<<20 || hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("NextTrace release checksum mismatch")
	}
	if err = f.Chmod(0755); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	b, err := exec.CommandContext(ctx, f.Name(), "--no-color", "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(b), "NextTrace v1.7.6") {
		return errors.New("NextTrace release version mismatch")
	}
	return os.Rename(f.Name(), NextTracePath)
}
