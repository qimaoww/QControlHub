//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Upstream publishes the detector as a one-liner that pipes its script straight
// into bash. Follow that source instead of pinning a revision and a checksum,
// so the detector keeps working when upstream moves `main`.
const ipQualityScriptURL = "https://IP.Check.Place"

// ipQualityProgram runs the official one-liner with the fixed detection
// arguments: accept upstream's dependency installation, stay in privacy mode so
// nothing is uploaded to the upstream report host, and keep the full IP and the
// machine-readable JSON. The default locale is kept so the JSON carries the same
// localized labels the detector prints, which the panel renders.
const ipQualityProgram = `ulimit -f 256 || exit
exec bash --noprofile --norc <(curl -Ls ` + ipQualityScriptURL + `) -y -p -f -j -o "$1"`

func runIPQuality(ctx context.Context) (core.IPQualityResult, error) {
	ctx, cancel := context.WithTimeout(ctx, core.IPQualityTimeout)
	defer cancel()
	bash, err := ipQualityPrerequisites()
	if err != nil {
		return core.IPQualityResult{}, err
	}
	return executeIPQualityProgram(ctx, bash, ipQualityProgram)
}

// ipQualityPrerequisites resolves the shell that runs the one-liner. Only bash
// and curl are required up front, because curl fetches the script itself. Every
// other probe dependency is installed by upstream when it is missing; the Agent
// passes -y so that installation stays non-interactive.
func ipQualityPrerequisites() (string, error) {
	bash := ""
	for _, name := range []string{"bash", "curl"} {
		binary := ""
		// Match the child's fixed PATH, including the first executable it
		// would find. Do not skip an unsafe earlier executable for a safe one.
		for _, directory := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
			path := filepath.Join(directory, name)
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
				binary, err = ipQualityExecutable(path)
				if err != nil {
					return "", fmt.Errorf("IPQuality 依赖 %s 不安全：%w", name, err)
				}
				break
			}
		}
		if binary == "" {
			return "", fmt.Errorf("IPQuality 缺少依赖：%s；bash 与 curl 是获取上游脚本的前提，请先在节点安装", name)
		}
		if name == "bash" {
			bash = binary
		}
	}
	return bash, nil
}

func ipQualityExecutable(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(resolved)); err != nil {
		return "", err
	}
	if err := validatePrivilegedExecutable(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// executeIPQualityProgram runs a bash program inside a private working
// directory and accepts only the bounded JSON report. Cancellation kills the
// whole process group (curl, DNSBL workers and progress-bar subprocesses).
func executeIPQualityProgram(ctx context.Context, bash, program string) (core.IPQualityResult, error) {
	directory, err := os.MkdirTemp("", "qagent-ip-quality-")
	if err != nil {
		return core.IPQualityResult{}, err
	}
	defer os.RemoveAll(directory)
	command := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", program,
		"ip-quality", filepath.Join(directory, "report.json"))
	command.Dir = directory
	command.Env = append(commandEnvironment(""), "TERM=dumb", "CURL_HOME="+directory,
		"XDG_CONFIG_HOME="+directory, "TMPDIR="+directory)
	configureCommand(command)
	// Progress output can be verbose. Truncate diagnostics without interrupting
	// a healthy run; only the bounded JSON report is accepted as result data.
	log := &boundedOutput{limit: 16 << 10}
	command.Stdout, command.Stderr = log, log
	runErr := command.Run()
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	if ctx.Err() != nil {
		return core.IPQualityResult{}, fmt.Errorf("IPQuality 检测超时或已停止：%w", ctx.Err())
	}
	result, err := readIPQualityReport(directory)
	if err != nil {
		// Do not publish ANSI progress, ads, external HTML or partial reports as
		// a successful result. The exit status still helps diagnose missing tools.
		return core.IPQualityResult{}, fmt.Errorf("IPQuality 未生成有效报告（执行状态：%v）：%w", runErr, err)
	}
	// Upstream's final [[ IPv6 available ]] returns 1 on IPv4-only hosts even
	// after a valid IPv4 report. A validated report, not that status, is decisive.
	return result, nil
}

func readIPQualityReport(directory string) (core.IPQualityResult, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return core.IPQualityResult{}, err
	}
	defer root.Close()
	file, err := root.OpenFile("report.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return core.IPQualityResult{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > core.MaxIPQualityResultBytes {
		return core.IPQualityResult{}, errors.New("IPQuality 报告不是大小受限的普通文件")
	}
	content, err := io.ReadAll(io.LimitReader(file, core.MaxIPQualityResultBytes+1))
	if err != nil {
		return core.IPQualityResult{}, err
	}
	return core.ParseIPQualityReports(content)
}
