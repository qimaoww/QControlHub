//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestIPQualityConcurrentPreparationLockCancellationAndRecovery(t *testing.T) {
	directory := t.TempDir()
	lock, err := acquireIPQualityDependencyLock(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if second, err := acquireIPQualityDependencyLock(ctx, directory); !errors.Is(err, context.DeadlineExceeded) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("concurrent preparation acquired lock: %v", err)
	}
	lock.Close()
	retry, err := acquireIPQualityDependencyLock(context.Background(), directory)
	if err != nil {
		t.Fatalf("could not recover after cancellation: %v", err)
	}
	retry.Close()
	if err := os.Remove(filepath.Join(directory, "install.lock")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(directory, "install.lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	if file, err := acquireIPQualityDependencyLock(context.Background(), directory); err == nil {
		file.Close()
		t.Fatal("accepted a FIFO lock")
	}
}

func TestIPQualityDependencyPreparation(t *testing.T) {
	for _, scenario := range []string{"complete", "missing-bash-curl", "partial", "busybox-grep", "old-bash", "no-ca", "repository", "lock", "permission", "unsupported", "unverified-install", "non-root", "unsafe", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			missing := map[string]bool{}
			switch scenario {
			case "missing-bash-curl":
				missing["bash"], missing["curl"] = true, true
			case "complete", "no-ca", "busybox-grep", "old-bash":
			default:
				missing["jq"] = true
			}
			var commands []string
			installed, caAvailable := false, scenario != "no-ca"
			p := ipQualityDependencyPreparer{
				lookup: func(name string) (string, error) {
					if scenario == "unsafe" && name == "curl" {
						return "", errors.New("writable executable directory")
					}
					if scenario == "unsupported" && (name == "apt-get" || name == "apk" || name == "dnf" || name == "yum" || name == "zypper") {
						return "", nil
					}
					if missing[name] && !installed {
						return "", nil
					}
					return "/usr/bin/" + name, nil
				},
				euid: func() int {
					if scenario == "non-root" {
						return 1000
					}
					return 0
				},
				caAvailable: func() bool { return caAvailable },
				run: func(ctx context.Context, path string, args []string) (string, error) {
					if filepath.Base(path) == "apt-get" {
						commands = append(commands, strings.Join(args, " "))
						switch scenario {
						case "repository":
							return "Failed to fetch https://user:secret@repo.example/index?token=secret Temporary failure resolving repo.example", errors.New("exit status 100")
						case "lock":
							return "Could not get lock /var/lib/dpkg/lock", errors.New("exit status 100")
						case "permission":
							return "Read-only file system", errors.New("exit status 100")
						case "canceled":
							cancel()
							return "", context.Canceled
						}
						if strings.Contains(strings.Join(args, " "), "install") && scenario != "unverified-install" {
							installed, caAvailable = true, true
						}
						return "", nil
					}
					if filepath.Base(path) == "grep" {
						if scenario == "busybox-grep" && !installed {
							return "BusyBox grep", nil
						}
						return "GNU grep", nil
					}
					if filepath.Base(path) == "bash" && scenario == "old-bash" && !installed {
						return "", errors.New("exit status 1")
					}
					if filepath.Base(path) == "nc" {
						return "usage: nc", errors.New("exit status 1")
					}
					return "", nil
				},
			}
			var stages []string
			err := p.prepare(ctx, func(stage string) { stages = append(stages, stage) })
			wantFailure := map[string]string{"repository": "依赖下载/软件源失败", "lock": "包管理器锁冲突", "permission": "系统权限", "unsupported": "未找到支持", "unverified-install": "仍不可执行", "non-root": "权限不足", "unsafe": "不安全", "canceled": "已取消"}[scenario]
			if wantFailure != "" {
				if err == nil || !strings.Contains(err.Error(), wantFailure) {
					t.Fatalf("want %q, got %v", wantFailure, err)
				}
				if strings.Contains(err.Error(), "user:secret") || strings.Contains(err.Error(), "token=secret") {
					t.Fatal("credentials leaked")
				}
				if scenario == "lock" && len(commands) != 3 {
					t.Fatalf("lock retry count: %v", commands)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "complete" && len(commands) != 0 {
				t.Fatalf("installed existing dependencies: %v", commands)
			}
			if scenario != "complete" && len(commands) != 2 {
				t.Fatalf("did not refresh/install: %v", commands)
			}
			before := len(commands)
			if err := p.prepare(ctx, func(string) {}); err != nil || len(commands) != before {
				t.Fatalf("repeat preparation was not idempotent: %v %v", commands, err)
			}
		})
	}
}

func TestIPQualityPackageMapping(t *testing.T) {
	for manager, want := range map[string][]string{
		"apt-get": {"netcat-openbsd", "dnsutils", "iproute2"}, "apk": {"netcat-openbsd", "bind-tools", "iproute2"},
		"dnf": {"nmap-ncat", "bind-utils", "iproute"}, "yum": {"nmap-ncat", "bind-utils", "iproute"}, "zypper": {"netcat-openbsd", "bind-utils", "iproute2"},
	} {
		plan, err := ipQualityPackagePlan(manager, []string{"bash", "curl", "nc", "dig", "ip", "grep"})
		if err != nil {
			t.Fatal(err)
		}
		command := strings.Join(plan[len(plan)-1], " ")
		for _, pkg := range append(want, "bash", "curl", "grep", "ca-certificates") {
			if !strings.Contains(command, pkg) {
				t.Fatalf("%s missing %s: %v", manager, pkg, plan)
			}
		}
	}
}

func TestIPQualityTransientInstallIsolation(t *testing.T) {
	args := strings.Join(ipQualityDependencyHelperArguments("/usr/local/lib/qagent/qagent", "qagent-ip-quality-dependencies-fixture.service"), "\n")
	for _, want := range []string{"--property=User=root", "--property=ProtectSystem=strict", "--property=ReadWritePaths=/usr /etc /var /run /bin /sbin /lib -/lib64 -/lib32 -/libx32", "--property=RuntimeMaxSec=245s", "--property=KillMode=control-group", "prepare-ip-quality"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %s: %s", want, args)
		}
	}
	if strings.Contains(args, "-c\n") || strings.Contains(args, "bash") {
		t.Fatal("privileged helper allows arbitrary shell commands")
	}
}

func TestIPQualityFailureDiagnosticsKeepTailAndRedact(t *testing.T) {
	w := &ipQualityDiagnosticTail{}
	fmt.Fprint(w, strings.Repeat("progress\n", 10000))
	fmt.Fprint(w, "\x1b[31mFailed to fetch https://user:password@repo.test/index?token=secret\x1b[0m\nAuthorization: Bearer abc123\nPermission denied")
	value := sanitizeIPQualityDiagnostic(w.String())
	if !strings.Contains(value, "Permission denied") || !strings.Contains(value, "repo.test") {
		t.Fatal(value)
	}
	for _, secret := range []string{"user:password", "token=secret", "abc123", "\x1b"} {
		if strings.Contains(value, secret) {
			t.Fatalf("leaked %q: %s", secret, value)
		}
	}
	_, err := executeIPQualityProgram(context.Background(), "/bin/bash", `printf 'dig: command not found\n' >&2; exit 127`)
	if err == nil || !strings.Contains(err.Error(), "dig: command not found") || !strings.Contains(err.Error(), "exit status 127") {
		t.Fatal(err)
	}
}
