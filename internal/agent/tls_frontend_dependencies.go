package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const managedCaddyBinary = "/usr/local/lib/qagent/cores/caddy"

func managedNginxBinary() string {
	for _, path := range []string{"/usr/sbin/nginx", "/usr/bin/nginx", "/sbin/nginx"} {
		if _, err := os.Lstat(path); err == nil {
			return path
		}
	}
	return "/usr/sbin/nginx"
}

func (m *tlsFrontendManager) ensureDependency(ctx context.Context, mode string) error {
	binary := managedCaddyBinary
	if mode == "nginx" {
		binary = managedNginxBinary()
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
	if _, err := os.Lstat(binary); err == nil {
		return validatePrivilegedExecutable(binary)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if mode == "nginx" {
		if err := installManagedPackages(ctx, true); err != nil {
			return err
		}
		return validatePrivilegedExecutable(managedNginxBinary())
	}
	if ipQualityRootCAs() == nil {
		if err := installManagedPackages(ctx, false); err != nil {
			return err
		}
	}
	updater := m.updater
	if updater == nil {
		updater = NewCoreUpdater()
	}
	var release githubRelease
	if err := updater.getJSON(ctx, updater.apiBase+"/repos/caddyserver/caddy/releases/latest", &release); err != nil {
		return fmt.Errorf("下载 Caddy 官方发布信息：%w", err)
	}
	if release.Draft || release.Prerelease || !strings.HasPrefix(release.TagName, "v2.") {
		return errors.New("Caddy 最新发布不是支持的 v2 稳定版")
	}
	name := "caddy_" + strings.TrimPrefix(release.TagName, "v") + "_linux_" + updater.goarch + ".tar.gz"
	var asset githubReleaseAsset
	for _, candidate := range release.Assets {
		if candidate.Name == name {
			asset = candidate
			break
		}
	}
	if asset.Name == "" || !strings.HasPrefix(asset.Digest, "sha256:") {
		return fmt.Errorf("Caddy 官方发布缺少 %s 或 SHA256 摘要", name)
	}
	archive, err := updater.downloadAsset(ctx, asset)
	if err != nil {
		return fmt.Errorf("下载并校验 Caddy：%w", err)
	}
	defer os.Remove(archive)
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
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
			return readErr
		}
		if header.Name != "caddy" {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > 100<<20 {
			return errors.New("Caddy 压缩包中的程序无效")
		}
		value, err = io.ReadAll(io.LimitReader(reader, 100<<20+1))
		if err != nil {
			return err
		}
		break
	}
	if len(value) == 0 {
		return errors.New("Caddy 压缩包缺少程序")
	}
	if err = os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
		return err
	}
	if err = validateProtectedDirectoryChain(filepath.Dir(binary)); err != nil {
		return err
	}
	if _, err = atomicDeployWithDefaultMetadata(binary, string(value), fileMetadata{mode: 0755}); err != nil {
		return err
	}
	output, err := runInDirectoryWithEnvironment(ctx, "", "XDG_CONFIG_HOME="+m.state, binary, "version")
	version := strings.Fields(output)
	if err != nil || len(version) == 0 || version[0] != release.TagName {
		return fmt.Errorf("Caddy 安装版本检查失败：%v：%s", err, output)
	}
	return nil
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
		// Debian maintainer scripts must not launch an unmanaged default server.
		// A pre-existing policy is respected; otherwise install a temporary deny
		// policy and remove only the exact file created by this helper.
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
			policy := "/usr/sbin/policy-rc.d"
			file, createErr := os.OpenFile(policy, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
			if createErr == nil {
				if _, err = file.WriteString("#!/bin/sh\nexit 101\n"); err != nil {
					file.Close()
					os.Remove(policy)
					return err
				}
				file.Close()
				defer os.Remove(policy)
			} else if !errors.Is(createErr, os.ErrExist) {
				return createErr
			} else if nginx {
				if err := validatePrivilegedExecutable(policy); err != nil {
					return err
				}
				_, policyErr := run(ctx, policy, "nginx", "start")
				var exit *exec.ExitError
				if !errors.As(policyErr, &exit) || exit.ExitCode() != 101 {
					return errors.New("现有 policy-rc.d 未禁止默认服务启动；请先安装 Nginx，面板将创建独立服务")
				}
			}
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

func (m *tlsFrontendManager) ensureService(ctx context.Context, mode string) error {
	name := "qagent-tls-" + mode
	binary := managedCaddyBinary
	arguments := "run --config " + filepath.Join(m.root, "caddy.conf")
	if mode == "nginx" {
		binary = managedNginxBinary()
		arguments = "-p " + m.state + "/ -c " + filepath.Join(m.root, "nginx.conf")
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

	if stored, err := os.ReadFile(path); err == nil {
		if err = validateManagedUnitFile(path); err != nil {
			return err
		}
		if string(stored) != content {
			return fmt.Errorf("%s 已被修改，请检查服务配置后重试", path)
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
		closeErr := file.Close()
		if err = errors.Join(writeErr, closeErr); err != nil {
			return err
		}
		if m.services.Kind() == ServiceManagerOpenRC {
			if err = os.Chmod(path, 0755); err != nil {
				return err
			}
		} else {
			if _, err = run(ctx, m.services.executable, "daemon-reload"); err != nil {
				return err
			}
		}
	}
	if m.services.Kind() == ServiceManagerSystemd {
		if err := validateManagedUnitDropIns(ctx, name); err != nil {
			return err
		}
		_, err := run(ctx, m.services.enableHelper(), "enable", name)
		return err
	}
	_, err := run(ctx, m.services.enableHelper(), "add", name, "default")
	return err
}
