package agent

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Trust only the official nginx.org Debian repository inside the temporary
// APT configuration. This public keyring is from nginx.org/keys/nginx_signing.key.
//
//go:embed nginx-signing.asc
var nginxSigningKeys []byte

//go:embed fetch-nginx-deb.sh
var fetchNginxDeb string

//go:embed nginx_signing.rsa.pub
var nginxAPKKey []byte

//go:embed fetch-nginx-apk.sh
var fetchNginxAPK string

//go:embed fetch-nginx-rpm.sh
var fetchNginxRPM string

const privateNginxDir = "/usr/local/lib/qagent/managed-nginx"

const officialRevisionPattern = `[0-9]+\.[0-9]+\.[0-9]+-[a-zA-Z0-9][a-zA-Z0-9.~_+]*`

var nginxPrivateName = regexp.MustCompile(`^nginx-(` + officialRevisionPattern + `)-[a-f0-9]{64}$`)
var nginxOfficialRevision = regexp.MustCompile(`^` + officialRevisionPattern + `$`)

type nginxPlatform struct{ family, suite string }

func nginxHostPlatform() (nginxPlatform, error) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return nginxPlatform{}, err
	}
	values := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		k, v, _ := strings.Cut(line, "=")
		values[k] = decodeOSReleaseValue(v)
	}
	return nginxPackagePlatform(values, runtime.GOARCH)
}

func nginxPackagePlatform(osRelease map[string]string, arch string) (nginxPlatform, error) {
	id, version := osRelease["ID"], osRelease["VERSION_ID"]
	suite := osRelease["VERSION_CODENAME"]
	major, _, _ := strings.Cut(version, ".")
	p := nginxPlatform{}
	switch id {
	case "debian", "ubuntu":
		if regexp.MustCompile(`^[a-z]+$`).MatchString(suite) {
			p = nginxPlatform{id, suite}
		}
	case "alpine":
		m := regexp.MustCompile(`^([0-9]+\.[0-9]+)(?:\.[0-9]+)?$`).FindStringSubmatch(version)
		if m != nil {
			p = nginxPlatform{id, m[1]}
		}
	case "rhel", "centos", "rocky", "almalinux", "ol":
		p = nginxPlatform{"centos", major}
	case "amzn":
		if major == "2023" {
			p = nginxPlatform{id, major}
		}
	case "sles":
		p = nginxPlatform{id, major}
	}
	if (arch != "amd64" && arch != "arm64") || p.family == "" || !regexp.MustCompile(`^[a-z0-9.]+$`).MatchString(p.suite) {
		return nginxPlatform{}, fmt.Errorf("无适用于 %s %s/%s 的 Nginx 官方仓库", id, version, arch)
	}
	return p, nil
}

func (m *tlsFrontendManager) nginxServiceArguments(binary string) string {
	args := "-p " + m.state + "/ -c " + filepath.Join(m.root, "nginx.conf")
	if privateNginxRevision(binary) != "" {
		args += " -e stderr"
	}
	return args
}

func privateNginxRevision(path string) string {
	if filepath.Dir(path) != privateNginxDir || filepath.Clean(path) != path {
		return ""
	}
	fields := nginxPrivateName.FindStringSubmatch(filepath.Base(path))
	if fields == nil {
		return ""
	}
	return fields[1]
}

// The installed unit selects the binary for reloads, certificate renewal and
// future upgrades. Accept only a complete project-generated unit.
func (m *tlsFrontendManager) nginxBinary() string {
	return m.proxyBinary("nginx")
}

func (m *tlsFrontendManager) proxyBinary(mode string) string {
	system := managedCaddyBinary
	if mode == "nginx" {
		system = systemNginxBinary()
	}
	_, unit, _ := m.serviceDefinitionForBinary(mode, false, system)
	data, err := os.ReadFile(unit)
	if err != nil {
		return system
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		var path string
		if value, ok := strings.CutPrefix(line, "ExecStart="); ok {
			path, _, _ = strings.Cut(value, " ")
		} else if value, ok := strings.CutPrefix(line, `command="`); ok {
			path = strings.TrimSuffix(value, `"`)
		}
		if (mode == "nginx" && privateNginxRevision(path) != "") || (mode == "caddy" && caddyPrivateBinary(path)) {
			_, _, expected := m.serviceDefinitionForBinary(mode, false, path)
			if string(data) == expected {
				return path
			}
		}
	}
	return system
}

func fetchOfficialNginx(ctx context.Context, platform nginxPlatform, currentRevision, operation string) (string, string, error) {
	dir, err := os.MkdirTemp("", "qagent-nginx-deb-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(dir)
	// _apt must be able to open the source-scoped keyring.
	if err = os.Chmod(dir, 0711); err != nil {
		return "", "", err
	}
	key := filepath.Join(dir, "signing.asc")
	if err = os.WriteFile(key, nginxSigningKeys, 0644); err != nil {
		return "", "", err
	}
	if err = os.Chmod(key, 0644); err != nil {
		return "", "", err
	}
	script := fetchNginxRPM
	switch platform.family {
	case "debian", "ubuntu":
		script = fetchNginxDeb
	case "alpine":
		script = fetchNginxAPK
		if err = os.WriteFile(filepath.Join(dir, "nginx_signing.rsa.pub"), nginxAPKKey, 0644); err != nil {
			return "", "", err
		}
	}
	output, err := runInDirectory(ctx, dir, "/bin/sh", "-ec", script, "fetch-nginx", platform.family, platform.suite, operation, currentRevision)
	if err != nil {
		return "", "", fmt.Errorf("获取 Nginx 官方签名包失败：%w：%s", err, output)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "revision"))
	revision := string(raw)
	if err != nil || !nginxOfficialRevision.MatchString(revision) {
		return "", "", errors.New("Nginx 官方软件包修订号无效")
	}
	if operation == "check" {
		return "", revision, nil
	}
	source := filepath.Join(dir, "extracted/usr/sbin/nginx")
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 64<<20 {
		return "", "", errors.New("官方软件包中的 Nginx 程序无效")
	}
	value, err := os.ReadFile(source)
	if err != nil {
		return "", "", err
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return "", "", err
	}
	for _, directory := range []string{filepath.Dir(privateNginxDir), privateNginxDir} {
		if err = os.MkdirAll(directory, 0750); err != nil {
			return "", "", err
		}
		if err = validateProtectedDirectoryChain(directory); err != nil {
			return "", "", err
		}
		if err = applyManagedConfigurationMetadata(directory, true, int(identity.gid)); err != nil {
			return "", "", err
		}
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(value))
	path := filepath.Join(privateNginxDir, "nginx-"+revision+"-"+digest)
	if err = validatePrivilegedExecutable(path); errors.Is(err, os.ErrNotExist) {
		_, err = atomicDeployWithDefaultMetadata(path, string(value), fileMetadata{mode: 0755})
	} else if err == nil {
		cached, _, readErr := protectedCoreMigrationFileDigest(path, 64<<20)
		if readErr != nil || cached != digest {
			return "", "", errors.New("托管 Nginx 文件与已验证的软件包不一致")
		}
	}
	if err != nil {
		return "", "", err
	}
	version, err := runInDirectoryWithEnvironmentAndIdentity(ctx, "", "", path, &identity, "-v")
	upstream, _, _ := strings.Cut(revision, "-")
	if err != nil || version != "nginx version: nginx/"+upstream {
		return "", "", fmt.Errorf("Nginx %s 运行依赖或版本校验失败：%s", revision, version)
	}
	return path, revision, nil
}

// Keep administrator files, including modified copies, when retiring old
// versions. Only an artifact whose contents still match its name is ours.
func cleanupPrivateNginx(keep string) error {
	if err := validateProtectedDirectoryChain(privateNginxDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	entries, err := os.ReadDir(privateNginxDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(privateNginxDir, entry.Name())
		if path == keep || privateNginxRevision(path) == "" || !entry.Type().IsRegular() {
			continue
		}
		digest, exists, err := protectedCoreMigrationFileDigest(path, 64<<20)
		if err != nil {
			return err
		}
		if exists && strings.HasSuffix(path, "-"+digest) {
			if err = os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}
