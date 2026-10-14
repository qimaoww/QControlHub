//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Keep the official moving source, but download and validate it before running.
const ipQualityScriptURL = "https://IP.Check.Place"

// ipQualityPrintedReportLimit bounds each address family's printed report.
// The capture also allows line separators and a small amount of non-report
// output; the encoded result is checked separately against core's limit.
const ipQualityPrintedReportLimit = 32 << 10
const ipQualityPrintedOutputLimit = 2*ipQualityPrintedReportLimit + 4<<10

// ipQualityProgram runs the verified script with the fixed detection
// arguments: skip upstream's unchecked installation after Agent preparation, stay in privacy mode so
// nothing is uploaded to the upstream report host, and write the machine-readable
// JSON to the private report path. The default locale is kept so the JSON carries
// the same localized labels the detector prints, and the printed report is left
// enabled on stdout so the Agent can read the vendor risk wording from it.
// Do not impose RLIMIT_FSIZE on this process tree: dependency installers write
// package indexes and binaries larger than the report budget. The report reader
// enforces the JSON file's type and size before accepting it.
const ipQualityProgram = `exec bash --noprofile --norc "$2" -n -y -p -f -o "$1"`

func runIPQuality(ctx context.Context) (core.IPQualityResult, error) {
	result, _, err := runIPQualityDetailed(ctx)
	return result, err
}

func runIPQualityDetailed(ctx context.Context) (core.IPQualityResult, string, error) {
	ctx, cancel := context.WithTimeout(ctx, core.IPQualityTimeout)
	defer cancel()
	var stages []string
	progress := func(message string) {
		message = sanitizeIPQualityDiagnostic(message)
		stages = append(stages, message)
		slog.Info("IPQuality", "stage", message)
	}
	result, err := runPreparedIPQuality(ctx, progress)
	if err != nil {
		progress("失败：" + err.Error())
	} else {
		progress("完成：有效报告已通过校验")
	}
	return result, strings.Join(stages, "\n"), err
}

func runPreparedIPQuality(ctx context.Context, progress func(string)) (core.IPQualityResult, error) {
	progress("检查依赖")
	preparation, cancel := context.WithTimeout(ctx, ipQualityPreparationTimeout+10*time.Second)
	err := prepareIPQualityDependencies(preparation, progress)
	cancel()
	if err != nil {
		return core.IPQualityResult{}, fmt.Errorf("IPQuality 依赖准备失败：%w", err)
	}
	bash, err := findIPQualityExecutable("bash")
	if err != nil {
		return core.IPQualityResult{}, err
	}
	directory, err := os.MkdirTemp("", "qagent-ip-quality-")
	if err != nil {
		return core.IPQualityResult{}, err
	}
	defer os.RemoveAll(directory)
	progress("下载脚本")
	if err := downloadIPQualityScript(ctx, directory, progress); err != nil {
		return core.IPQualityResult{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return core.IPQualityResult{}, err
	}
	executable, err = ipQualityExecutable(executable)
	if err != nil {
		return core.IPQualityResult{}, err
	}
	progress("执行检测：检测服务连通性由上游按 IPv4/IPv6 分别检查")
	return executeIPQualityInDirectory(ctx, bash, ipQualityResourceWrapper(executable)+ipQualityProgram, directory, progress)
}

// RunIPQualityCheck runs the same complete pipeline for local diagnostics.
func RunIPQualityCheck(parent context.Context, progress func(string)) (core.IPQualityResult, error) {
	ctx, cancel := context.WithTimeout(parent, core.IPQualityTimeout)
	defer cancel()
	return runPreparedIPQuality(ctx, progress)
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
	return executeIPQualityInDirectory(ctx, bash, program, directory, func(string) {})
}

func executeIPQualityInDirectory(ctx context.Context, bash, program, directory string, progress func(string)) (core.IPQualityResult, error) {
	command := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", program,
		"ip-quality", filepath.Join(directory, "report.json"), filepath.Join(directory, "detector.sh"))
	command.Dir = directory
	command.Env = append(commandEnvironment(""), "TERM=dumb", "CURL_HOME="+directory,
		"XDG_CONFIG_HOME="+directory, "TMPDIR="+directory)
	configureCommand(command)
	// Progress output is verbose and goes to stderr. Truncate diagnostics without
	// interrupting a healthy run; only the bounded JSON report is accepted as
	// result data, and the bounded stdout only supplies the printed report.
	output := &boundedOutput{limit: ipQualityPrintedOutputLimit}
	log := &ipQualityDiagnosticTail{}
	command.Stdout, command.Stderr = output, log
	runErr := command.Run()
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	if ctx.Err() != nil {
		return core.IPQualityResult{}, fmt.Errorf("执行检测：超时或已停止：%w；诊断：%s", ctx.Err(), sanitizeIPQualityDiagnostic(log.String()))
	}
	if output.Truncated() {
		return core.IPQualityResult{}, errors.New("IPQuality 报告输出超过采集大小限制，已拒绝截断的报告")
	}
	progress(fmt.Sprintf("解析报告：执行状态 %v", runErr))
	result, err := readIPQualityReport(directory)
	if err != nil {
		// Do not publish ANSI progress, ads, external HTML or partial reports as
		// a successful result. The exit status still helps diagnose missing tools.
		return core.IPQualityResult{}, fmt.Errorf("执行检测/解析报告：IPQuality 未生成有效报告（执行状态：%v）：%w；检查检测服务的 DNS/HTTP/IPv4/IPv6 连通性；stderr：%s；stdout：%s", runErr, err, sanitizeIPQualityDiagnostic(log.String()), sanitizeIPQualityDiagnostic(output.String()))
	}
	texts := parseIPQualityReportTexts(output.String())
	if len(texts) != len(result.Reports) {
		return core.IPQualityResult{}, errors.New("IPQuality 报告原文缺失或与 JSON 报告份数不匹配")
	}
	for _, text := range texts {
		if len(text) > ipQualityPrintedReportLimit {
			return core.IPQualityResult{}, errors.New("IPQuality 单个地址族的报告原文超过 32 KiB 大小限制")
		}
	}
	result.ReportsText = texts
	// Upstream's final [[ IPv6 available ]] returns 1 on IPv4-only hosts even
	// after a valid IPv4 report. A validated report, not that status, is decisive.
	return core.NormalizeIPQualityResult(&result)
}

// parseIPQualityReportTexts splits the detector's printed output into one entry
// per address family. The panel renders the archived image from this text, so
// the image is exactly what the terminal shows.
func parseIPQualityReportTexts(output string) []string {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	starts := make([]int, 0, 2)
	for index, line := range lines {
		if !strings.Contains(line, "IP质量体检报告") {
			continue
		}
		if start := index - 1; start >= 0 && isIPQualityBanner(lines[start]) {
			index = start
		}
		starts = append(starts, index)
	}
	texts := make([]string, 0, len(starts))
	for index, start := range starts {
		end := len(lines)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		if text := strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n"); text != "" {
			texts = append(texts, text)
		}
	}
	return texts
}

func isIPQualityBanner(line string) bool {
	trimmed := strings.TrimSpace(line)
	return len(trimmed) >= 32 && strings.Trim(trimmed, "#") == ""
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
