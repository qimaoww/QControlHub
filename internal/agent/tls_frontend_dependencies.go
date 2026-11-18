package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const managedCaddyBinary = "/usr/local/lib/qagent/cores/caddy"

func systemNginxBinary() string {
	for _, path := range []string{"/usr/sbin/nginx", "/usr/bin/nginx", "/sbin/nginx"} {
		if _, err := os.Lstat(path); err == nil {
			return path
		}
	}
	return "/usr/sbin/nginx"
}

func (m *tlsFrontendManager) ensureDependency(ctx context.Context, mode string) error {
	binary := m.proxyBinary(mode)
	if mode == "nginx" {
		binary = m.nginxBinary()
		_, unit, _ := m.serviceDefinition("nginx", false)
		if _, err := os.Lstat(unit); errors.Is(err, os.ErrNotExist) {
			platform, err := nginxHostPlatform()
			if err != nil {
				return err
			}
			candidate, _, err := fetchOfficialNginx(ctx, platform, "", "install")
			if err != nil {
				return err
			}
			return m.ensureServiceForBinary(ctx, mode, candidate)
		}
	} else {
		identity, err := managedCoreServiceIdentity()
		if err != nil {
			return err
		}
		for _, directory := range []string{filepath.Dir(filepath.Dir(binary)), filepath.Dir(binary)} {
			if err = os.MkdirAll(directory, 0750); err != nil {
				return err
			}
			if err = validateProtectedDirectoryChain(directory); err != nil {
				return err
			}
			if err = applyManagedConfigurationMetadata(directory, true, int(identity.gid)); err != nil {
				return fmt.Errorf("准备 Caddy 非 root 服务的程序目录：%w", err)
			}
		}
	}
	if mode == "caddy" && ipQualityRootCAs() == nil {
		if err := installManagedPackages(ctx, false); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(binary); err == nil {
		if err := validatePrivilegedExecutable(binary); err != nil {
			return err
		}
		if mode == "caddy" {
			identity, err := managedCoreServiceIdentity()
			if err != nil {
				return err
			}
			output, err := m.runCaddy(ctx, &identity, "list-modules")
			if err != nil {
				return err
			}
			for _, module := range []string{"tls.issuance.acme", "caddy.storage.file_system", "http.handlers.reverse_proxy", "http.reverse_proxy.transport.http"} {
				if !strings.Contains(output, module) {
					return fmt.Errorf("Caddy 缺少必要能力 %s；重新安装官方稳定版", module)
				}
			}
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if mode == "nginx" {
		if privateNginxRevision(binary) != "" {
			platform, err := nginxHostPlatform()
			if err != nil {
				return err
			}
			candidate, _, err := fetchOfficialNginx(ctx, platform, privateNginxRevision(binary), "install")
			if err != nil {
				return err
			}
			if candidate != binary {
				return errors.New("已验证的软件包与服务记录不同，请在维护窗口核对托管 Nginx")
			}
			return nil
		}
		return errors.New("历史托管 Nginx 程序缺失，请核对服务定义")
	}
	updater, tag, asset, err := m.caddyCandidate(ctx)
	if err != nil {
		return err
	}
	data, err := caddyAssetBinary(ctx, updater, asset)
	if err != nil {
		return err
	}
	if _, err = atomicDeployWithDefaultMetadata(binary, string(data), fileMetadata{mode: 0755}); err != nil {
		return err
	}
	version, err := m.caddyCommand(ctx, binary, "version")
	if err != nil || firstWord(version) != tag {
		_ = os.Remove(binary)
		return fmt.Errorf("Caddy 版本校验失败：%s", version)
	}
	_, err = atomicDeploy(binary+".qch-owned", fmt.Sprintf("%x", sha256.Sum256(data)))
	return err
}

func (m *tlsFrontendManager) caddyCandidate(ctx context.Context) (*CoreUpdater, string, githubReleaseAsset, error) {
	updater := m.updater
	if updater == nil {
		updater = NewCoreUpdater()
	}
	release, err := updater.caddyRelease(ctx)
	if err != nil {
		return nil, "", githubReleaseAsset{}, fmt.Errorf("下载 Caddy 官方发布信息：%w", err)
	}
	if release.Draft || release.Prerelease || !caddyStableTag.MatchString(release.TagName) {
		return nil, "", githubReleaseAsset{}, errors.New("Caddy 最新发布不是支持的 v2 稳定版")
	}
	name := "caddy_" + strings.TrimPrefix(release.TagName, "v") + "_linux_" + updater.goarch + ".tar.gz"
	var asset githubReleaseAsset
	for _, candidate := range release.Assets {
		if candidate.Name == name {
			asset = candidate
			break
		}
	}
	if asset.Name == "" || !releaseDigest.MatchString(asset.Digest) {
		return nil, "", githubReleaseAsset{}, fmt.Errorf("Caddy 官方发布缺少 %s 或 SHA256 摘要", name)
	}
	return updater, release.TagName, asset, nil
}

func caddyAssetBinary(ctx context.Context, updater *CoreUpdater, asset githubReleaseAsset) ([]byte, error) {
	archive, err := updater.downloadAsset(ctx, asset)
	if err != nil {
		return nil, fmt.Errorf("下载并校验 Caddy：%w", err)
	}
	defer os.Remove(archive)
	file, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var value []byte
	for {
		header, readErr := reader.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		if header.Name != "caddy" {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > 100<<20 {
			return nil, errors.New("Caddy 压缩包中的程序无效")
		}
		value, err = io.ReadAll(io.LimitReader(reader, 100<<20+1))
		if err != nil {
			return nil, err
		}
		break
	}
	if len(value) == 0 {
		return nil, errors.New("Caddy 压缩包缺少程序")
	}
	return value, nil
}

func installManagedPackages(ctx context.Context, nginx bool) error {
	packages := []string{"ca-certificates"}
	if nginx {
		packages = append(packages, "nginx")
	}
	var binary string
	var args []string
	for _, name := range []string{"apt-get", "apk", "dnf", "yum", "zypper"} {
		path, err := findIPQualityExecutable(name)
		if err != nil {
			return err
		}
		if path == "" {
			continue
		}
		binary = path
		switch name {
		case "apt-get":
			args = []string{"-o", "DPkg::Lock::Timeout=30", "-o", "Acquire::Retries=2", "install", "-y", "--no-install-recommends"}
		case "apk":
			args = []string{"--wait", "30", "add", "--no-cache"}
		case "dnf", "yum":
			args = []string{"install", "-y"}
		case "zypper":
			args = []string{"--non-interactive", "install", "--no-recommends"}
		}
		if name == "apt-get" {
			if output, err := runIPQualityCommand(ctx, binary, []string{"-o", "Acquire::Retries=2", "update"}); err != nil {
				return fmt.Errorf("更新软件包索引失败：%w：%s", err, output)
			}
			var actions []string
			if nginx {
				actions = []string{"start"}
			}
			cleanup, err := protectAptServices(ctx, actions...)
			if err != nil {
				return err
			}
			defer cleanup()
		}
		break
	}
	if binary == "" {
		return errors.New("缺少 Nginx，且未找到支持的包管理器（apt-get/apk/dnf/yum/zypper）")
	}
	args = append(args, packages...)
	output, err := runIPQualityCommand(ctx, binary, args)
	if err != nil {
		return fmt.Errorf("自动安装 Nginx 失败：%w：%s；检查软件源及包管理器锁后重试", err, output)
	}
	return nil
}

// Debian maintainer scripts must leave the independent system service alone.
// Preserve administrator policy and remove only the temporary file we own.
func protectAptServices(ctx context.Context, actions ...string) (func(), error) {
	const policy = "/usr/sbin/policy-rc.d"
	file, err := os.OpenFile(policy, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if errors.Is(err, os.ErrExist) {
		if len(actions) > 0 {
			if err := validatePrivilegedExecutable(policy); err != nil {
				return nil, err
			}
		}
		for _, action := range actions {
			for _, service := range []string{"nginx", "nginx.service"} {
				_, policyErr := run(ctx, policy, service, action)
				var exit *exec.ExitError
				if !errors.As(policyErr, &exit) || exit.ExitCode() != 101 {
					return nil, errors.New("现有 policy-rc.d 允许 Nginx 服务操作，请在维护窗口手动更新")
				}
			}
		}
		return func() {}, nil
	}
	if err != nil {
		return nil, err
	}
	created, statErr := file.Stat()
	cleanup := func() {
		if current, err := os.Lstat(policy); err == nil && statErr == nil && os.SameFile(created, current) {
			_ = os.Remove(policy)
		}
	}
	_, writeErr := file.WriteString("#!/bin/sh\nexit 101\n")
	if err := errors.Join(statErr, writeErr, file.Close()); err != nil {
		cleanup()
		return nil, err
	}
	return cleanup, nil
}

func (m *tlsFrontendManager) serviceDefinition(mode string, legacy bool) (string, string, string) {
	return m.serviceDefinitionForBinary(mode, legacy, m.proxyBinary(mode))
}

func (m *tlsFrontendManager) serviceDefinitionForBinary(mode string, legacy bool, binary string) (string, string, string) {
	name := "qagent-tls-" + mode
	arguments := "run --config " + filepath.Join(m.root, "caddy.conf")
	if mode == "nginx" {
		arguments = m.nginxServiceArguments(binary)
	}
	var path, content string
	if m.services.Kind() == ServiceManagerSystemd {
		name += ".service"
		path = "/etc/systemd/system/" + name
		content = "[Unit]\nDescription=QControlHub managed TLS frontend (" + mode + ")\nAfter=network-online.target\nWants=network-online.target\n[Service]\nType=simple\nUser=qcontrolhub-core\nGroup=qcontrolhub-core\nUMask=0027\nExecStart=" + binary + " " + arguments + "\nRestart=on-failure\nRestartSec=3\nNoNewPrivileges=yes\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE\nAmbientCapabilities=CAP_NET_BIND_SERVICE\nProtectSystem=strict\nProtectHome=yes\nPrivateTmp=yes\nPrivateDevices=yes\nReadWritePaths=" + m.state + "\n[Install]\nWantedBy=multi-user.target\n"
	} else {
		path = "/etc/init.d/" + name
		content = "#!/sbin/openrc-run\n# QControlHub managed TLS frontend\ncommand=\"" + binary + "\"\ncommand_args=\"" + arguments + "\"\ncommand_user=\"qcontrolhub-core:qcontrolhub-core\"\nsupervisor=supervise-daemon\nrespawn_delay=3\ncapabilities=\"^cap_net_bind_service\"\nno_new_privs=true\numask=0027\ndepend() { need net; }\n"
	}
	if m.services.Kind() == ServiceManagerOpenRC {
		content = strings.Replace(content, "no_new_privs=true", `qch_last_cap=$(cat /proc/sys/kernel/cap_last_cap) || exit 1
case "$qch_last_cap" in ''|*[!0-9]*) exit 1 ;; esac
qch_cap=0
while [ "$qch_cap" -le "$qch_last_cap" ]; do
 case "$qch_cap" in 10) ;; *) capabilities="${capabilities},!${qch_cap}" ;; esac
 qch_cap=$((qch_cap + 1))
done
no_new_privs=true`, 1)
	}

	if mode == "caddy" && !legacy {
		if m.services.Kind() == ServiceManagerSystemd {
			content = strings.Replace(content, "UMask=0027\n", "UMask=0027\nWorkingDirectory="+m.state+"\nEnvironment=XDG_DATA_HOME="+m.state+"/data XDG_CONFIG_HOME="+m.state+"/config HOME="+m.state+"\n", 1)
		} else {
			content = strings.Replace(content, "command_user=", "directory=\""+m.state+"\"\nexport XDG_DATA_HOME=\""+m.state+"/data\"\nexport XDG_CONFIG_HOME=\""+m.state+"/config\"\nexport HOME=\""+m.state+"\"\ncommand_user=", 1)
		}
	}
	return name, path, content
}

func (m *tlsFrontendManager) ensureService(ctx context.Context, mode string) error {
	return m.ensureServiceForBinary(ctx, mode, m.proxyBinary(mode))
}

func (m *tlsFrontendManager) ensureServiceForBinary(ctx context.Context, mode, binary string) error {
	name, path, content := m.serviceDefinitionForBinary(mode, false, binary)
	if m.services.Kind() == ServiceManagerSystemd {
		if err := validateManagedUnitDropIns(ctx, name); err != nil {
			return err
		}
	}
	pending := filepath.Join(m.root, mode+"-runtime.pending")
	_, pendingErr := os.Lstat(pending)
	changed := pendingErr == nil
	if stored, err := os.ReadFile(path); err == nil {
		if err = validateManagedUnitFile(path); err != nil {
			return err
		}
		if string(stored) != content {
			_, _, legacy := m.serviceDefinition(mode, true)
			if string(stored) != legacy {
				return fmt.Errorf("%s 已被用户修改，未覆盖；备份并与项目生成版本核对，将自定义设置移至其他服务后重试", path)
			}
			if err := m.writeConfig(pending, "runtime-v2", true); err != nil {
				return err
			}
			// Only the exact historical project template is eligible. The atomic
			// deploy keeps a root-owned backup and never touches certificate storage.
			if _, err = atomicDeployWithDefaultMetadata(path, content, fileMetadata{mode: 0644}); err != nil {
				return err
			}
			changed = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		if err = validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteString(content)
		if err = errors.Join(writeErr, file.Close()); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		if m.services.Kind() == ServiceManagerOpenRC {
			if err := os.Chmod(path, 0755); err != nil {
				return err
			}
		} else if _, err := run(ctx, m.services.executable, "daemon-reload"); err != nil {
			return err
		}
		// Reloading JSON does not update a process environment. A migrated
		// running service needs one restart, including when routes are unchanged.
		if status, err := m.services.status(ctx, name); err != nil {
			return err
		} else if status == "active" {
			if output, err := m.services.command(ctx, name, core.ActionRestart); err != nil {
				return fmt.Errorf("应用 TLS 服务环境失败：%w；%s", err, output)
			}
		}
		if err := os.Remove(pending); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
