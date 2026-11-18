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
func (m *tlsFrontendManager) upgradeManagedNginx(ctx context.Context, build string, explicit bool, upgradePackage func(context.Context, string) (string, string, error)) (string, error) {
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
	// A previous Agent can write the target build before the new Agent starts.
	markerValue := "runtime-v2 " + build
	if err := validateProtectedDirectoryChain(m.root); err != nil {
		return "", err
	}

	service, unit, _ := m.serviceDefinition("nginx", false)
	status, err := m.services.status(ctx, service)
	if err != nil {
		return "", err
	}
	if status != "active" {
		return "", fmt.Errorf("托管 Nginx 服务状态为 %s，请在维护窗口核对并更新", status)
	}
	if err = m.checkProxyUnit(ctx, "nginx"); err != nil {
		return "", err
	}
	binary := m.nginxBinary()
	if err := validatePrivilegedExecutable(binary); err != nil {
		return "", err
	}
	installed, err := os.Stat(binary)
	if err != nil {
		return "", err
	}
	runningBinary := binary
	running, err := m.nginxMasterExecutable(ctx, service, binary)
	if err != nil && privateNginxRevision(binary) != "" {
		// A previous update may have persisted the unit before restarting.
		pid, pidErr := managedServiceProcessPID(ctx, m.services, service)
		if pidErr != nil {
			return "", pidErr
		}
		id, idErr := readOpenRCProcessIdentity(pid)
		if idErr != nil {
			return "", idErr
		}
		runningBinary = strings.TrimSuffix(id.Executable, " (deleted)")
		if runningBinary != systemNginxBinary() && privateNginxRevision(runningBinary) == "" {
			return "", err
		}
		running, err = m.nginxMasterExecutable(ctx, service, runningBinary)
	}
	if err != nil {
		return "", err
	}
	if !explicit {
		checked, err := readConfigurationFile(marker)
		if err == nil && checked == markerValue && os.SameFile(running, installed) && runningBinary == binary {
			return "", nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}

	candidate, revision, err := upgradePackage(ctx, binary)
	if err != nil {
		return "", err
	}
	if err := validatePrivilegedExecutable(candidate); err != nil {
		return "", err
	}
	installed, err = os.Stat(candidate)
	if err != nil {
		return "", err
	}
	if !os.SameFile(running, installed) || candidate != binary {
		if output, err := m.validateNginxBinary(ctx, filepath.Join(m.root, "nginx.conf"), candidate); err != nil {
			return "", fmt.Errorf("候选 Nginx 配置校验失败：%w：%s", err, output)
		}
		if err := m.switchNginx(ctx, service, unit, runningBinary, candidate, installed); err != nil {
			return "", err
		}
	}
	if os.SameFile(running, installed) {
		if err = m.verifyRunningProxy(ctx, "nginx", service, candidate, installed); err != nil {
			return "", err
		}
	}
	if privateNginxRevision(candidate) != "" {
		if err = cleanupPrivateNginx(candidate); err != nil {
			return "", err
		}
	}
	if err := m.writeConfig(marker, markerValue, true); err != nil {
		return "", err
	}
	if privateNginxRevision(candidate) != "" {
		return fmt.Sprintf("托管 Nginx 正在运行 nginx.org 主线签名包 %s（已核对运行进程）。", revision), nil
	}
	return fmt.Sprintf("托管 Nginx 正在运行 %s。安全补丁以软件包来源公告为准。", revision), nil
}

func (m *tlsFrontendManager) switchNginx(ctx context.Context, service, unit, old, candidate string, installed os.FileInfo) error {
	return m.switchProxy(ctx, "nginx", service, unit, old, candidate, installed)
}

func (m *tlsFrontendManager) switchProxy(ctx context.Context, mode, service, unit, old, candidate string, installed os.FileInfo) error {
	if status, err := m.services.status(ctx, service); err != nil || status != "active" {
		return fmt.Errorf("托管 Nginx 已停止切换，服务状态 %s：%v", status, err)
	}
	if err := m.checkProxyUnit(ctx, mode); err != nil {
		return err
	}
	if _, err := m.proxyMasterExecutable(ctx, mode, service, old); err != nil {
		return err
	}
	reload := func(ctx context.Context) error {
		if m.services.Kind() == ServiceManagerSystemd {
			_, err := run(ctx, m.services.executable, "daemon-reload")
			return err
		}
		return nil
	}
	writeUnit := func(binary string) error {
		_, _, content := m.serviceDefinitionForBinary(mode, false, binary)
		backup, err := atomicDeployWithDefaultMetadata(unit, content, fileMetadata{mode: 0644})
		if backup != "" {
			_ = os.Remove(backup)
		}
		return err
	}
	// Use try-restart so an independently stopped service stays stopped.
	restart := []string{"try-restart", service}
	if m.services.Kind() == ServiceManagerOpenRC {
		restart = []string{"--ifstarted", service, "restart"}
	}
	activate := func() error {
		if old != candidate {
			if err := writeUnit(candidate); err != nil {
				return err
			}
			if err := reload(ctx); err != nil {
				return err
			}
		}
		if _, err := run(ctx, m.services.executable, restart...); err != nil {
			return err
		}
		return m.verifyRunningProxy(ctx, mode, service, candidate, installed)
	}
	err := activate()
	if err == nil || old == candidate {
		return err
	}
	recoverCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	restore := func() error {
		if err := writeUnit(old); err != nil {
			return err
		}
		if err := reload(recoverCtx); err != nil {
			return err
		}
		status, err := m.services.status(recoverCtx, service)
		if status == "inactive" {
			return errors.New("服务已停止，已恢复原单元")
		}
		if err != nil {
			return err
		}
		original, err := os.Stat(old)
		if err != nil {
			return err
		}
		if running, err := m.proxyMasterExecutable(recoverCtx, mode, service, old); err == nil && os.SameFile(running, original) {
			return m.verifyRunningProxy(recoverCtx, mode, service, old, original)
		}
		if _, err := m.services.command(recoverCtx, service, core.ActionRestart); err != nil {
			return err
		}
		return m.verifyRunningProxy(recoverCtx, mode, service, old, original)
	}
	if restoreErr := restore(); restoreErr != nil {
		return fmt.Errorf("%s 切换失败（%v），恢复失败：%w", mode, err, restoreErr)
	}
	return fmt.Errorf("%s 切换失败，已恢复 %s：%w", mode, m.proxyVersion(recoverCtx, mode, old), err)
}

func (m *tlsFrontendManager) verifyRunningProxy(ctx context.Context, mode, service, binary string, installed os.FileInfo) error {
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := verifyNginxActivation(check, func(ctx context.Context) (string, error) {
		status, err := m.services.status(ctx, service)
		if err != nil || status != "active" {
			return status, err
		}
		if running, err := m.proxyMasterExecutable(ctx, mode, service, binary); err == nil && os.SameFile(running, installed) {
			return status, nil
		}
		return "activating", nil
	})
	if err != nil {
		return err
	}
	deployments, err := m.load()
	if err != nil {
		return err
	}
	routes, err := activeTLSFrontends(deployments)
	if err != nil {
		return err
	}
	for _, f := range routes {
		if f.Mode == mode {
			if err := probeFrontendTLS(check, f, true, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func verifyNginxActivation(ctx context.Context, probe serviceStatusProbe) error {
	status, err := waitForServiceState(ctx, "active", time.Second, 100*time.Millisecond, probe)
	if err != nil {
		return err
	}
	if status != "active" {
		return fmt.Errorf("托管 Nginx 服务状态为 %s", status)
	}
	return nil
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
	expected := "nginx: master process " + binary + " " + m.nginxServiceArguments(binary)
	if strings.TrimSuffix(identity.Executable, " (deleted)") != resolved || strings.TrimSpace(strings.Join(identity.Argv, " ")) != expected {
		return nil, errors.New("运行中的 Nginx 与 QAgent 托管服务配置不一致")
	}
	return os.Stat(filepath.Join(openRCProcRoot, strconv.Itoa(pid), "exe"))
}

var nginxDebPackage = regexp.MustCompile(`^nginx(-(core|full|light|extras))?(:[a-z0-9-]+)?$`)

func (m *tlsFrontendManager) upgradeNginxBinary(ctx context.Context, binary string) (string, string, error) {
	p, err := nginxHostPlatform()
	if err != nil {
		return "", "", err
	}
	revision := privateNginxRevision(binary)
	if revision == "" {
		revision, err = systemNginxRevision(ctx, p, binary)
		if err != nil {
			return "", "", err
		}
	}
	return fetchOfficialNginx(ctx, p, revision, "install")
}

func systemNginxRevision(ctx context.Context, p nginxPlatform, binary string) (string, error) {
	if p.family == "debian" || p.family == "ubuntu" {
		_, v, err := nginxDebianOwner(ctx, binary)
		return v, err
	}
	if p.family == "alpine" {
		apk, err := findIPQualityExecutable("apk")
		if err != nil {
			return "", err
		}
		out, err := run(ctx, apk, "--no-network", "info", "--who-owns", binary)
		_, v, ok := strings.Cut(out, " is owned by nginx-")
		if err != nil || !ok || !nginxOfficialRevision.MatchString(v) {
			return "", errors.New("无法核对 Nginx APK 文件归属")
		}
		return v, nil
	}
	rpm, err := findIPQualityExecutable("rpm")
	if err != nil {
		return "", err
	}
	out, err := run(ctx, rpm, "-qf", "--qf", "%{NAME} %{VERSION}-%{RELEASE}", binary)
	v, ok := strings.CutPrefix(out, "nginx ")
	if err != nil || !ok || !nginxOfficialRevision.MatchString(v) {
		return "", errors.New("无法核对 Nginx RPM 文件归属")
	}
	return v, nil
}

func nginxDebianOwner(ctx context.Context, binary string) (string, string, error) {
	query, err := findIPQualityExecutable("dpkg-query")
	if err != nil || query == "" {
		return "", "", errors.New("无法核对 Nginx 的 Debian 软件包归属")
	}
	owner, err := run(ctx, query, "--search", binary)
	name, path, ok := strings.Cut(strings.TrimSpace(owner), ": ")
	if err != nil || !ok || path != binary || !nginxDebPackage.MatchString(name) {
		return "", "", errors.New("运行的 Nginx 可执行文件由管理员负责更新")
	}
	revision, err := run(ctx, query, "--show", "--showformat=${Version}", name)
	if fields := strings.Fields(revision); err != nil || len(fields) != 1 {
		return "", "", errors.New("无法核对已安装的 Nginx 软件包修订号")
	}
	return name, strings.TrimSpace(revision), nil
}
