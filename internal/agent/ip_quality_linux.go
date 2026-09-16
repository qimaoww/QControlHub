//go:build linux

package agent

import (
	"bytes"
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
	"strings"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// The third-party program is downloaded only for an explicitly queued task.
// Keep the immutable revision and checksum together when reviewing upgrades.
const ipQualityRevision = "2384a67c756eb35231f5982b34731e522be3653e"
const ipQualityScriptURL = "https://raw.githubusercontent.com/xykt/IPQuality/" + ipQualityRevision + "/ip.sh"
const ipQualityScriptSHA256 = "b30df5a3c2204276c54e99dcc5080b46f8a627667730aee7de63b109b8ecaecf"
const maxIPQualityScriptBytes = 512 << 10

func runIPQuality(ctx context.Context) (core.IPQualityResult, error) {
	ctx, cancel := context.WithTimeout(ctx, core.IPQualityTimeout)
	defer cancel()
	bash, err := ipQualityPrerequisites()
	if err != nil {
		return core.IPQualityResult{}, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("IPQuality script redirects are disabled")
		},
	}
	defer transport.CloseIdleConnections()
	script, err := downloadIPQualityScript(ctx, client, ipQualityScriptURL, ipQualityScriptSHA256)
	if err != nil {
		return core.IPQualityResult{}, err
	}
	// ip.sh normally loads ref/* from main, including the DNSBL list passed to
	// its shell workers. Pin these data files to the reviewed revision as well.
	script = bytes.ReplaceAll(script, []byte("${rawgithub}main/"),
		[]byte("https://raw.githubusercontent.com/xykt/IPQuality/"+ipQualityRevision+"/"))
	return executeIPQualityScript(ctx, bash, script)
}

func ipQualityPrerequisites() (string, error) {
	var missing []string
	bash := ""
	for _, name := range []string{"bash", "curl", "jq", "bc", "nc", "dig", "ip", "timeout", "grep"} {
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
			missing = append(missing, name)
		}
		if name == "bash" {
			bash = binary
		}
	}
	if len(missing) != 0 {
		return "", fmt.Errorf("IPQuality 缺少依赖：%s；请按 docs/ip-quality.md 安装，检测任务不会自动安装软件", strings.Join(missing, ", "))
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

func downloadIPQualityScript(ctx context.Context, client *http.Client, url, checksum string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("下载 IPQuality 脚本失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 IPQuality 脚本失败：HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxIPQualityScriptBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxIPQualityScriptBytes {
		return nil, errors.New("IPQuality 脚本超过大小限制")
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != checksum {
		return nil, errors.New("IPQuality 脚本 SHA-256 校验失败，未执行")
	}
	return content, nil
}

func executeIPQualityScript(ctx context.Context, bash string, script []byte) (core.IPQualityResult, error) {
	directory, err := os.MkdirTemp("", "qagent-ip-quality-")
	if err != nil {
		return core.IPQualityResult{}, err
	}
	defer os.RemoveAll(directory)
	scriptPath := filepath.Join(directory, "ip.sh")
	if err := os.WriteFile(scriptPath, script, 0o600); err != nil {
		return core.IPQualityResult{}, err
	}
	// The shell fragment is fixed; all arguments are separate argv entries.
	// A file-size limit bounds report writes, and cancellation kills the entire
	// process group (curl, DNSBL workers and progress-bar subprocesses).
	command := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c",
		`ulimit -f 256 || exit; exec "$@"`, "ip-quality",
		bash, "--noprofile", "--norc", scriptPath, "-n", "-p", "-f", "-E", "-j",
		"-o", filepath.Join(directory, "report.json"))
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
