//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const ipQualityPreparationTimeout = 4 * time.Minute

// Packages come exclusively from the host's configured package repositories.
// Do not source os-release, run sudo, or repair an unsafe existing executable.
type ipQualityDependency struct {
	name string
	args []string
}

var ipQualityDependencies = []ipQualityDependency{
	{"bash", []string{"--noprofile", "--norc", "-c", `(( BASH_VERSINFO[0] >= 4 ))`}},
	{"curl", []string{"--version"}}, {"jq", []string{"--version"}},
	{"bc", []string{"--version"}}, {"nc", []string{"-h"}},
	{"dig", []string{"-v"}}, {"ip", []string{"-Version"}},
	{"grep", []string{"--version"}}, {"gunzip", []string{"--help"}},
	{"awk", []string{"BEGIN { exit 0 }"}}, {"timeout", []string{"--help"}},
	{"xargs", []string{"--help"}}, {"sed", []string{"--help"}},
	{"sort", []string{"--help"}}, {"head", []string{"--help"}},
	{"cut", []string{"--help"}}, {"tr", []string{"--help"}},
	{"date", []string{"--help"}}, {"od", []string{"--help"}},
	{"wc", []string{"--help"}}, {"uname", []string{"--help"}},
	{"stty", []string{"--help"}}, {"cat", []string{"--help"}},
}

func findIPQualityExecutable(name string) (string, error) {
	for _, directory := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		path := filepath.Join(directory, name)
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return "", fmt.Errorf("依赖 %s 不是可执行的普通文件", name)
		}
		return ipQualityExecutable(path)
	}
	return "", nil
}

type ipQualityDependencyPreparer struct {
	lookup      func(string) (string, error)
	run         func(context.Context, string, []string) (string, error)
	euid        func() int
	caAvailable func() bool
}

func newIPQualityDependencyPreparer() ipQualityDependencyPreparer {
	return ipQualityDependencyPreparer{findIPQualityExecutable, runIPQualityCommand, os.Geteuid, func() bool { return ipQualityRootCAs() != nil }}
}

func (p ipQualityDependencyPreparer) missing(ctx context.Context) ([]string, error) {
	var missing []string
	for _, dependency := range ipQualityDependencies {
		path, err := p.lookup(dependency.name)
		if err != nil {
			return nil, fmt.Errorf("检查依赖 %s 不安全：%w；请修复文件所有者及目录权限", dependency.name, err)
		}
		if path == "" {
			missing = append(missing, dependency.name)
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		output, probeErr := p.run(probeCtx, path, dependency.args)
		cancel()
		// OpenBSD nc prints help but exits 1 on some distributions.
		if dependency.name == "nc" && strings.Contains(strings.ToLower(output), "usage") {
			probeErr = nil
		}
		// Alpine's BusyBox grep lacks -P, which the detector uses.
		if dependency.name == "grep" && !strings.Contains(output, "GNU grep") {
			probeErr = errors.New("需要支持 -P 的 GNU grep")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if probeErr != nil {
			missing = append(missing, dependency.name)
		}
	}
	if !p.caAvailable() {
		missing = append(missing, "ca-certificates")
	}
	return missing, nil
}

func ipQualityPackagePlan(manager string, missing []string) ([][]string, error) {
	mapping := map[string]string{"bash": "bash", "curl": "curl", "jq": "jq", "bc": "bc", "grep": "grep", "gunzip": "gzip", "ca-certificates": "ca-certificates"}
	for _, command := range []string{"timeout", "sort", "head", "cut", "tr", "date", "od", "wc", "uname", "stty", "cat"} {
		mapping[command] = "coreutils"
	}
	mapping["awk"], mapping["sed"], mapping["xargs"] = "gawk", "sed", "findutils"
	var refresh, install []string
	switch manager {
	case "apt-get":
		mapping["nc"], mapping["dig"], mapping["ip"] = "netcat-openbsd", "dnsutils", "iproute2"
		refresh = []string{"-o", "APT::Update::Error-Mode=any", "-o", "Acquire::Retries=2", "-o", "Acquire::http::Timeout=20", "-o", "Acquire::https::Timeout=20", "update"}
		install = []string{"-o", "DPkg::Lock::Timeout=30", "-o", "Acquire::Retries=2", "-o", "Acquire::http::Timeout=20", "-o", "Acquire::https::Timeout=20", "install", "-y", "--no-install-recommends"}
	case "apk":
		mapping["nc"], mapping["dig"], mapping["ip"] = "netcat-openbsd", "bind-tools", "iproute2"
		install = []string{"--wait", "30", "add", "--no-cache"}
	case "dnf", "yum":
		mapping["nc"], mapping["dig"], mapping["ip"] = "nmap-ncat", "bind-utils", "iproute"
		install = []string{"install", "-y", "--setopt=timeout=20", "--setopt=retries=2"}
	case "zypper":
		mapping["nc"], mapping["dig"], mapping["ip"] = "netcat-openbsd", "bind-utils", "iproute2"
		install = []string{"--non-interactive", "install", "--no-recommends"}
	default:
		return nil, errors.New("不支持的包管理器；支持 apt-get、apk、dnf、yum、zypper")
	}
	seen := map[string]bool{}
	for _, name := range missing {
		pkg := mapping[name]
		if pkg == "" {
			return nil, fmt.Errorf("未知依赖 %s", name)
		}
		if !seen[pkg] {
			install = append(install, pkg)
			seen[pkg] = true
		}
	}
	// A minimal host may have no CA store yet; install through its own repositories.
	if !seen["ca-certificates"] {
		install = append(install, "ca-certificates")
	}
	if refresh != nil {
		return [][]string{refresh, install}, nil
	}
	return [][]string{install}, nil
}

func (p ipQualityDependencyPreparer) prepare(ctx context.Context, progress func(string)) error {
	missing, err := p.missing(ctx)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		progress("检查依赖：全部可执行，复用已有依赖")
		return nil
	}
	progress("检查依赖：需要补齐 " + strings.Join(missing, ", "))
	if p.euid() != 0 {
		return errors.New("安装依赖：权限不足；请使用 root 部署 Agent 后重试")
	}
	manager, binary := "", ""
	for _, name := range []string{"apt-get", "apk", "dnf", "yum", "zypper"} {
		path, err := p.lookup(name)
		if err != nil {
			return fmt.Errorf("安装依赖：包管理器 %s 不安全：%w", name, err)
		}
		if path != "" {
			manager, binary = name, path
			break
		}
	}
	if binary == "" {
		return fmt.Errorf("安装依赖：缺少 %s，未找到支持的包管理器（apt-get/apk/dnf/yum/zypper）；请配置系统软件源及包管理器", strings.Join(missing, ", "))
	}
	plan, err := ipQualityPackagePlan(manager, missing)
	if err != nil {
		return err
	}
	for _, args := range plan {
		progress("安装依赖：" + manager + " " + strings.Join(args, " "))
		for attempt := 0; ; attempt++ {
			output, err := p.run(ctx, binary, args)
			if err == nil {
				break
			}
			kind, advice, retryable := classifyIPQualityInstallFailure(output)
			if ctx.Err() != nil {
				return fmt.Errorf("安装依赖：超时或已取消；命令 %s；重新检测可补齐未完成安装：%w", manager, ctx.Err())
			}
			if kind == "包管理器锁冲突" && attempt < 2 {
				progress("安装依赖：包管理器锁冲突，等待后重试")
				if err := waitIPQualityRetry(ctx, 2*time.Second); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("安装依赖：%s；命令 %s %s；退出状态 %v；可重试=%t；%s；诊断：%s", kind, manager, strings.Join(args, " "), err, retryable, advice, sanitizeIPQualityDiagnostic(output))
		}
	}
	missing, err = p.missing(ctx)
	if err != nil {
		return err
	}
	if len(missing) != 0 {
		return fmt.Errorf("安装依赖：命令成功但依赖仍不可执行：%s；请检查软件包版本及系统 PATH", strings.Join(missing, ", "))
	}
	progress("安装依赖：完成并重新验证可执行性")
	return nil
}

func classifyIPQualityInstallFailure(output string) (string, string, bool) {
	value := strings.ToLower(output)
	switch {
	case (strings.Contains(value, "seteuid") || strings.Contains(value, "setresuid") || strings.Contains(value, "setegid") || strings.Contains(value, "setresgid")) && (strings.Contains(value, "operation not permitted") || strings.Contains(value, "permission denied")):
		return "安装服务身份切换权限不足", "更新 Agent 并检查安装服务的 CAP_SETUID/CAP_SETGID；若仍失败，检查宿主机或容器的 capability 限制", false
	case strings.Contains(value, "permission denied"), strings.Contains(value, "read-only"), strings.Contains(value, "operation not permitted"), strings.Contains(value, "superuser"):
		return "系统权限或只读文件系统", "检查 root 权限、磁盘挂载和 Agent 安装服务配置", false
	case strings.Contains(value, "lock"), strings.Contains(value, "another app"):
		return "包管理器锁冲突", "等待其他软件包操作完成后重试；不要删除系统锁文件", true
	case strings.Contains(value, "unable to locate"), strings.Contains(value, "no match"), strings.Contains(value, "no such package"), strings.Contains(value, "nothing provides"), strings.Contains(value, "unmet dependencies"), strings.Contains(value, "dpkg was interrupted"):
		return "软件包或系统状态异常", "修复系统软件源或未完成的软件包事务后重试", false
	case strings.Contains(value, "resolve"), strings.Contains(value, "fetch"), strings.Contains(value, "download"), strings.Contains(value, "connection"), strings.Contains(value, "timed out"), strings.Contains(value, "temporary failure"), strings.Contains(value, "network"), strings.Contains(value, "certificate"):
		return "依赖下载/软件源失败", "检查系统软件源的 DNS、HTTP/HTTPS、证书及 IPv4/IPv6 连通性后重试", true
	default:
		return "软件包安装失败", "根据包管理器诊断修复系统后重试", false
	}
}

// RunIPQualityDependencyPreparation is a fixed utility: it accepts no commands,
// packages, paths, credentials or repository overrides from a task or script.
func RunIPQualityDependencyPreparation(parent context.Context, progress func(string)) error {
	ctx, cancel := context.WithTimeout(parent, ipQualityPreparationTimeout)
	defer cancel()
	if os.Geteuid() != 0 {
		return errors.New("安装依赖：需要 root 权限")
	}
	lock, err := acquireIPQualityDependencyLock(ctx, "/run/qagent-ip-quality-dependencies")
	if err != nil {
		return err
	}
	defer lock.Close()
	return newIPQualityDependencyPreparer().prepare(ctx, progress)
}

func acquireIPQualityDependencyLock(ctx context.Context, directory string) (_ *os.File, err error) {
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := validateProtectedDirectoryChain(directory); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(directory+"/install.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			lock.Close()
		}
	}()
	info, err := lock.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("安装依赖：不安全的安装锁文件")
	}
	if err := validateOwner(info, "dependency lock"); err != nil {
		return nil, err
	}
	for {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("安装依赖：等待并发安装超时或取消：%w", ctx.Err())
		}
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, err
		}
		if err := waitIPQualityRetry(ctx, 200*time.Millisecond); err != nil {
			return nil, fmt.Errorf("安装依赖：等待并发安装超时或取消：%w", err)
		}
	}
	return lock, nil
}

func prepareIPQualityDependencies(ctx context.Context, progress func(string)) error {
	p := newIPQualityDependencyPreparer()
	missing, err := p.missing(ctx)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		progress("检查依赖：全部可执行，复用已有依赖")
		return nil
	}
	progress("检查依赖：需要补齐 " + strings.Join(missing, ", "))
	// OpenRC and standalone root installations can use the same fixed utility.
	if _, err := os.Stat("/run/systemd/system"); errors.Is(err, os.ErrNotExist) {
		return RunIPQualityDependencyPreparation(ctx, progress)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = ipQualityExecutable(executable)
	if err != nil {
		return err
	}
	runner, err := ipQualityExecutable("/usr/bin/systemd-run")
	if err != nil {
		return fmt.Errorf("安装依赖：systemd 临时服务不可用；请重新部署 Agent：%w", err)
	}
	suffix, err := randomSuffix(8)
	if err != nil {
		return err
	}
	unit := "qagent-ip-quality-dependencies-" + suffix + ".service"
	ctx, cancel := context.WithTimeout(ctx, ipQualityPreparationTimeout+5*time.Second)
	defer cancel()
	progress("安装依赖：使用固定的 systemd 临时服务（Agent 保持 ProtectSystem=strict）")
	output, runErr := runIPQualityCommand(ctx, runner, ipQualityDependencyHelperArguments(executable, unit))
	// Killing systemd-run alone does not cancel its service. Stop the named unit
	// before returning on every path; RuntimeMaxSec remains a crash backstop.
	cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if systemctl, err := ipQualityExecutable("/usr/bin/systemctl"); err == nil {
		_, _ = runIPQualityCommand(cleanup, systemctl, []string{"stop", unit})
	}
	if output != "" {
		progress(sanitizeIPQualityDiagnostic(output))
	}
	if runErr != nil {
		return fmt.Errorf("安装依赖：临时服务失败；退出状态 %v；诊断：%s；检查软件源、系统日志及 systemd 权限后重试", runErr, sanitizeIPQualityDiagnostic(output))
	}
	missing, err = p.missing(ctx)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("安装依赖：临时服务结束后仍缺少 %s", strings.Join(missing, ", "))
	}
	return nil
}

func ipQualityDependencyHelperArguments(executable, unit string) []string {
	return []string{"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec", "--unit=" + unit,
		"--property=User=root", "--property=UMask=0022", "--property=NoNewPrivileges=yes",
		// systemd 257 drops CAP_SETUID while setting up seccomp unless explicitly
		// requested as ambient. NoNewPrivileges then prevents root exec from
		// restoring it, so APT cannot switch to _apt (including mirror+file).
		// UID/GID changes are needed only by this fixed package helper; APT's
		// own privilege drop clears ambient capabilities in its _apt children.
		"--property=AmbientCapabilities=CAP_SETUID CAP_SETGID",
		// strict protects the root mount and permits explicit package-store
		// exceptions. full creates conflicting read-only /usr and /etc mounts
		// on systemd 252, even when those exact paths are in ReadWritePaths.
		"--property=ProtectSystem=strict", "--property=ReadWritePaths=/usr /etc /var /run /bin /sbin /lib -/lib64 -/lib32 -/libx32",
		"--property=ProtectHome=yes", "--property=PrivateTmp=yes", "--property=PrivateDevices=yes",
		"--property=ProtectKernelTunables=yes", "--property=ProtectKernelModules=yes", "--property=ProtectControlGroups=yes",
		"--property=RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK",
		"--property=RuntimeMaxSec=245s", "--property=TimeoutStopSec=5s", "--property=KillMode=control-group",
		"--", executable, "prepare-ip-quality"}
}

func runIPQualityCommand(ctx context.Context, executable string, args []string) (string, error) {
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = append(commandEnvironment(""), "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a", "TERM=dumb")
	configureCommand(command)
	output := &ipQualityDiagnosticTail{}
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return output.String(), err
}

func waitIPQualityRetry(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
