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
	"regexp"
	"strings"
	"syscall"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Upstream publishes the detector as a one-liner that pipes its script straight
// into bash. Follow that source instead of pinning a revision and a checksum,
// so the detector keeps working when upstream moves `main`.
const ipQualityScriptURL = "https://IP.Check.Place"

// ipQualityProgram runs the official one-liner with the fixed detection
// arguments: accept upstream's dependency installation, stay in privacy mode so
// nothing is uploaded to the upstream report host, and write the machine-readable
// JSON to the private report path. The default locale is kept so the JSON carries
// the same localized labels the detector prints, and the printed report is left
// enabled on stdout so the Agent can read the vendor risk wording from it.
const ipQualityProgram = `ulimit -f 256 || exit
exec bash --noprofile --norc <(curl -Ls ` + ipQualityScriptURL + `) -y -p -f -o "$1"`

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
	// Progress output is verbose and goes to stderr. Truncate diagnostics without
	// interrupting a healthy run; only the bounded JSON report is accepted as
	// result data, and the bounded stdout only supplies the printed risk labels.
	output := &boundedOutput{limit: 64 << 10}
	log := &boundedOutput{limit: 16 << 10}
	command.Stdout, command.Stderr = output, log
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
	if levels := parseIPQualityLevels(output.String()); len(levels) == len(result.Reports) {
		result.Levels = levels
	}
	// Upstream's final [[ IPv6 available ]] returns 1 on IPv4-only hosts even
	// after a valid IPv4 report. A validated report, not that status, is decisive.
	return result, nil
}

// ipQualityLevelLabels maps the detector's printed provider names to the keys
// the panel renders.
var ipQualityLevelLabels = map[string]string{
	"IP2Location": "IP2LOCATION",
	"Scamalytics": "SCAMALYTICS",
	"ipapi":       "ipapi",
	"AbuseIPDB":   "AbuseIPDB",
	"IPQS":        "IPQS",
	"DB-IP":       "DBIP",
}

var (
	ipQualityANSIPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	ipQualityLevelLine   = regexp.MustCompile(`^([^：:]+)[：:][^|]*[|](\S+)$`)
)

// parseIPQualityLevels reads the risk wording upstream prints, one map per
// address family. ipapi and DB-IP return it from their own APIs, so it is not
// recoverable from the score in the JSON.
func parseIPQualityLevels(output string) []map[string]string {
	text := ipQualityANSIPattern.ReplaceAllString(output, "")
	sections := strings.Split(text, "IP质量体检报告")
	levels := make([]map[string]string, 0, len(sections)-1)
	for _, section := range sections[1:] {
		found := map[string]string{}
		for _, line := range strings.Split(section, "\n") {
			match := ipQualityLevelLine.FindStringSubmatch(strings.TrimSpace(line))
			if match == nil {
				continue
			}
			key, ok := ipQualityLevelLabels[strings.TrimSpace(match[1])]
			if !ok {
				continue
			}
			if label := strings.TrimSpace(match[2]); label != "" {
				found[key] = label
			}
		}
		levels = append(levels, found)
	}
	return levels
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
