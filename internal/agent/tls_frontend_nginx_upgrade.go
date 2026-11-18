package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (e *Executor) upgradeNginxForAgentBuild(ctx context.Context, build string, action core.Action) (string, error) {
	if build == "" || !managedTLSStateExists() {
		return "", nil
	}
	manager := e.serviceManager().Kind()
	spec := DefaultSpecsForServiceManager(manager)[core.EngineSingBox]
	var report strings.Builder
	err := e.runTLSFrontendRequest(ctx, tlsFrontendRequest{
		Service: spec.Service, Kind: manager, Action: action, Deadline: tlsOperationDeadline(ctx), NginxUpgrade: build,
	}, &report)
	return strings.TrimSpace(report.String()), err
}

// The TLS helper holds the same lock as deployments and certificate reloads.
// A build marker avoids contacting package repositories on ordinary restarts.
func (m *tlsFrontendManager) upgradeManagedNginx(ctx context.Context, build string, explicit bool, upgradePackage func(context.Context, string) (string, error)) (string, error) {
	deployments, err := m.load()
	if err != nil {
		return "", err
	}
	frontends, err := activeTLSFrontends(deployments)
	if err != nil {
		return "", err
	}
	active := false
	for _, f := range frontends {
		active = active || f.Mode == "nginx"
	}
	if !active {
		return "", nil
	}
	if len(build) > 256 || strings.ContainsAny(build, "\r\n\x00") {
		return "", errors.New("无效的 Agent 构建版本")
	}
	marker := filepath.Join(m.root, "nginx-package-build")
	if err := validateProtectedDirectoryChain(m.root); err != nil {
		return "", err
	}
	if !explicit {
		checked, err := readConfigurationFile(marker)
		if err == nil && checked == build {
			return "", nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}

	service, unit, definition := m.serviceDefinition("nginx", false)
	status, err := m.services.status(ctx, service)
	if err != nil {
		return "", err
	}
	if status != "active" {
		return "", fmt.Errorf("托管 Nginx 服务状态为 %s，请在维护窗口核对并更新", status)
	}
	if err = validateManagedUnitFile(unit); err != nil {
		return "", err
	}
	if config, err := os.ReadFile(unit); err != nil || string(config) != definition {
		return "", errors.New("托管 Nginx 服务定义已变更，请在维护窗口更新")
	}
	if m.services.Kind() == ServiceManagerSystemd {
		if path, err := systemdUnitProperty(ctx, service, "FragmentPath"); err != nil || path != unit {
			return "", errors.New("无法核对正在使用的托管 Nginx 单元")
		}
		err = validateManagedUnitDropIns(ctx, service)
	} else {
		err = validateOpenRCCoreOverrides(service)
	}
	if err != nil {
		return "", err
	}
	binary := managedNginxBinary()
	if err := validatePrivilegedExecutable(binary); err != nil {
		return "", err
	}
	if _, err := m.nginxMasterExecutable(ctx, service, binary); err != nil {
		return "", err
	}
	revision, err := upgradePackage(ctx, binary)
	if err != nil {
		return "", err
	}
	if err := validatePrivilegedExecutable(binary); err != nil {
		return "", err
	}
	installed, err := os.Stat(binary)
	if err != nil {
		return "", err
	}
	running, err := m.nginxMasterExecutable(ctx, service, binary)
	if err != nil {
		return "", err
	}
	if !os.SameFile(running, installed) {
		if _, err := m.validateNginx(ctx, filepath.Join(m.root, "nginx.conf")); err != nil {
			return "", fmt.Errorf("候选 Nginx 配置校验失败：%w", err)
		}
		// Only restart an active instance. Respect an administrator stopping
		// the service while the package manager or config check was running.
		restart := []string{"try-restart", service}
		if m.services.Kind() == ServiceManagerOpenRC {
			restart = []string{"--ifstarted", service, "restart"}
		}
		if _, err := run(ctx, m.services.executable, restart...); err != nil {
			return "", fmt.Errorf("托管 Nginx 重启失败：%w", err)
		}
		check, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := waitForServiceState(check, "active", time.Second, 100*time.Millisecond, func(ctx context.Context) (string, error) {
			if running, err := m.nginxMasterExecutable(ctx, service, binary); err == nil && os.SameFile(running, installed) {
				return m.services.status(ctx, service)
			}
			return "", nil
		}); err != nil {
			return "", fmt.Errorf("托管 Nginx 运行二进制核对失败：%w", err)
		}
	}
	if err := m.writeConfig(marker, build, true); err != nil {
		return "", err
	}
	return fmt.Sprintf("托管 Nginx 正在运行 %s。安全补丁以软件包来源公告为准。", revision), nil
}

func (m *tlsFrontendManager) nginxMasterExecutable(ctx context.Context, service, binary string) (os.FileInfo, error) {
	pid, err := managedServiceProcessPID(ctx, m.services, service)
	if err != nil {
		return nil, err
	}
	identity, err := readOpenRCProcessIdentity(pid)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return nil, err
	}
	expected := "nginx: master process " + binary + " -p " + m.state + "/ -c " + filepath.Join(m.root, "nginx.conf")
	if strings.TrimSuffix(identity.Executable, " (deleted)") != resolved || strings.TrimSpace(strings.Join(identity.Argv, " ")) != expected {
		return nil, errors.New("运行中的 Nginx 与 QAgent 托管服务配置不一致")
	}
	return os.Stat(filepath.Join(openRCProcRoot, strconv.Itoa(pid), "exe"))
}

var nginxDebPackage = regexp.MustCompile(`^nginx(-(core|full|light|extras))?(:[a-z0-9-]+)?$`)
var nginxAPKRevision = regexp.MustCompile(`^nginx-[0-9][a-zA-Z0-9._+~-]*-r[0-9]+$`)

func (m *tlsFrontendManager) upgradeSystemNginxPackage(ctx context.Context, binary string) (string, error) {
	path, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", err
	}
	apt, err := findIPQualityExecutable("apt-get")
	if err != nil {
		return "", err
	}
	if apt != "" {
		return m.upgradeAptNginx(ctx, apt, path)
	}
	apk, err := findIPQualityExecutable("apk")
	if err != nil {
		return "", err
	}
	if apk != "" {
		return upgradeAPKNginx(ctx, apk, path)
	}
	return "", errors.New("此软件源需由管理员更新 Nginx 软件包并重启托管服务")
}

func (m *tlsFrontendManager) upgradeAptNginx(ctx context.Context, apt, binary string) (string, error) {
	query, err := findIPQualityExecutable("dpkg-query")
	if err != nil || query == "" {
		return "", errors.New("无法核对 Nginx 的 Debian 软件包归属")
	}
	owner, err := run(ctx, query, "--search", binary)
	name, path, ok := strings.Cut(strings.TrimSpace(owner), ": ")
	if err != nil || !ok || path != binary || !nginxDebPackage.MatchString(name) {
		return "", errors.New("运行的 Nginx 可执行文件由管理员负责更新")
	}
	if _, err := runIPQualityCommandWithRestartMode(ctx, apt, []string{"-o", "Acquire::Retries=2", "--error-on=any", "update"}, "l"); err != nil {
		return "", fmt.Errorf("更新 APT 软件包索引失败：%w", err)
	}
	// The upstream package can call the SysV binary upgrade directly when
	// /run/nginx.pid exists, bypassing policy-rc.d.
	if err := m.checkIndependentNginx(ctx); err != nil {
		return "", err
	}
	cleanup, err := protectAptServices(ctx, "start", "stop", "restart", "try-restart", "reload", "force-reload", "upgrade")
	if err != nil {
		return "", err
	}
	defer cleanup()
	args := []string{"-o", "DPkg::Lock::Timeout=30", "-o", "Acquire::Retries=2", "-o", "Dpkg::Options::=--force-confold",
		"install", "-y", "--only-upgrade", "--no-remove", "--no-install-recommends", name}
	if _, err := runIPQualityCommandWithRestartMode(ctx, apt, args, "l"); err != nil {
		return "", fmt.Errorf("安装 APT 的 Nginx 更新失败：%w", err)
	}
	revision, err := run(ctx, query, "--show", "--showformat=${Version}", name)
	if fields := strings.Fields(revision); err != nil || len(fields) != 1 {
		return "", errors.New("无法核对已安装的 Nginx 软件包修订号")
	}
	return name + " " + strings.TrimSpace(revision), nil
}

func (m *tlsFrontendManager) checkIndependentNginx(ctx context.Context) error {
	if _, err := os.Lstat("/run/nginx.pid"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("检测到独立 Nginx 进程文件或无法核对，请在维护窗口手动更新系统包")
	}
	service := "nginx"
	if m.services.Kind() == ServiceManagerSystemd {
		service += ".service"
	}
	status, err := m.services.status(ctx, service)
	if err != nil {
		return fmt.Errorf("无法核对独立 Nginx 服务状态：%w", err)
	}
	switch status {
	case "inactive", "failed":
		return nil
	default:
		return errors.New("独立 Nginx 服务正在使用系统包，请在维护窗口手动更新")
	}
}

func upgradeAPKNginx(ctx context.Context, apk, binary string) (string, error) {
	revision := func() (string, error) {
		owner, err := run(ctx, apk, "--no-network", "info", "--who-owns", binary)
		_, version, ok := strings.Cut(strings.TrimSpace(owner), " is owned by ")
		if err != nil || !ok || !nginxAPKRevision.MatchString(version) {
			return "", errors.New("无法核对 Nginx 的 Alpine 软件包归属和修订号")
		}
		return version, nil
	}
	if _, err := revision(); err != nil {
		return "", err
	}
	if _, err := runIPQualityCommandWithRestartMode(ctx, apk, []string{"--wait", "30", "--no-cache", "update"}, "l"); err != nil {
		return "", fmt.Errorf("更新 APK 软件包索引失败：%w", err)
	}
	args := []string{"--wait", "30", "--no-cache", "--no-scripts", "--no-commit-hooks", "upgrade", "--latest", "nginx"}
	output, err := runIPQualityCommandWithRestartMode(ctx, apk, args, "l")
	if err != nil {
		return "", fmt.Errorf("安装 APK 的 Nginx 更新失败：%w", err)
	}
	if strings.Contains(output, "WARNING: fetching ") {
		return "", errors.New("APK 软件源未完整刷新，请核对 Nginx 软件包状态")
	}
	return revision()
}
