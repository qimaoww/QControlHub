package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

var caddyStableTag = regexp.MustCompile(`^v2\.[0-9]+\.[0-9]+$`)
var releaseDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var caddyPrivateName = regexp.MustCompile(`^caddy-(v2\.[0-9]+\.[0-9]+)-[a-f0-9]{64}$`)

func firstWord(value string) string {
	fields := strings.Fields(value)
	if len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func caddyPrivateBinary(path string) bool {
	return filepath.Clean(path) == path && filepath.Dir(path) == filepath.Dir(managedCaddyBinary) && caddyPrivateName.MatchString(filepath.Base(path))
}

func (m *tlsFrontendManager) caddyCommand(ctx context.Context, binary string, args ...string) (string, error) {
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return "", err
	}
	identity.ambientCaps = []uintptr{unix.CAP_NET_BIND_SERVICE}
	return runTLSCommand(ctx, m.state, m.caddyEnvironment(), binary, &identity, args...)
}

func (m *tlsFrontendManager) proxyMasterExecutable(ctx context.Context, mode, service, binary string) (os.FileInfo, error) {
	if mode == "nginx" {
		return m.nginxMasterExecutable(ctx, service, binary)
	}
	pid, err := managedServiceProcessPID(ctx, m.services, service)
	if err != nil {
		return nil, err
	}
	id, err := readOpenRCProcessIdentity(pid)
	if err != nil {
		return nil, err
	}
	if strings.TrimSuffix(id.Executable, " (deleted)") != binary || !slices.Equal(id.Argv, []string{binary, "run", "--config", filepath.Join(m.root, "caddy.conf")}) {
		return nil, errors.New("Caddy 主进程与托管单元不一致")
	}
	return os.Stat(filepath.Join(openRCProcRoot, strconv.Itoa(pid), "exe"))
}

func (m *tlsFrontendManager) checkProxyUnit(ctx context.Context, mode string) error {
	service, unit, definition := m.serviceDefinition(mode, false)
	if err := validateManagedUnitFile(unit); err != nil {
		return err
	}
	data, err := os.ReadFile(unit)
	if err != nil || string(data) != definition {
		return fmt.Errorf("托管 %s 单元已变更", mode)
	}
	if m.services.Kind() == ServiceManagerSystemd {
		if path, err := systemdUnitProperty(ctx, service, "FragmentPath"); err != nil || path != unit {
			return errors.New("托管服务单元路径不一致")
		}
		return validateManagedUnitDropIns(ctx, service)
	}
	return validateOpenRCCoreOverrides(service)
}

func (m *tlsFrontendManager) upgradeManagedCaddy(ctx context.Context, build string, explicit bool, latest *string) (string, error) {
	deployments, err := m.load()
	if err != nil {
		return "", err
	}
	routes, err := activeTLSFrontends(deployments)
	if err != nil {
		return "", err
	}
	used := false
	for _, route := range routes {
		used = used || route.Mode == "caddy"
	}
	if !used {
		return "", nil
	}
	if err = m.checkProxyUnit(ctx, "caddy"); err != nil {
		return "", err
	}
	service, unit, _ := m.serviceDefinition("caddy", false)
	if state, err := m.services.status(ctx, service); err != nil {
		return "", err
	} else if state != "active" {
		return "", fmt.Errorf("%w（%s）", issueStopped, state)
	}
	target := m.proxyBinary("caddy")
	pid, err := managedServiceProcessPID(ctx, m.services, service)
	if err != nil {
		return "", err
	}
	id, err := readOpenRCProcessIdentity(pid)
	if err != nil {
		return "", err
	}
	old := strings.TrimSuffix(id.Executable, " (deleted)")
	if old != managedCaddyBinary && !caddyPrivateBinary(old) {
		return "", errors.New("无法识别正在运行的 Caddy")
	}
	running, err := m.proxyMasterExecutable(ctx, "caddy", service, old)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(old)
	if err != nil || !os.SameFile(info, running) {
		return "", errors.New("Caddy 磁盘文件与主进程不一致，需核对来源")
	}
	digest, exists, err := protectedCoreMigrationFileDigest(old, 100<<20)
	if err != nil || !exists {
		return "", errors.New("无法校验 Caddy 程序")
	}
	if old != managedCaddyBinary && !strings.HasSuffix(old, "-"+digest) {
		return "", issueChangedBinary
	}
	marker := filepath.Join(m.root, "caddy-package-build")
	if !explicit && target == old {
		value, err := readConfigurationFile(marker)
		if err == nil && value == build+" "+digest {
			return "", nil
		}
	}
	current, err := m.caddyCommand(ctx, old, "version")
	version := firstWord(current)
	if err != nil || !caddyStableTag.MatchString(version) {
		return "", issueCaddyVersion
	}
	modules, err := m.caddyCommand(ctx, old, "list-modules", "--packages", "--skip-standard")
	if err := validateCaddyModuleList(modules, err); err != nil {
		return "", err
	}
	updater, tag, asset, err := m.caddyCandidate(ctx)
	if err != nil {
		return "", fmt.Errorf("%w：%w", issueCandidate, err)
	}
	*latest = tag
	if old == managedCaddyBinary {
		owned, err := readConfigurationFile(old + ".qch-owned")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", issueOwnershipFile
		}
		if owned != digest {
			if err := verifyHistoricalCaddy(ctx, updater, version, digest); err != nil {
				return "", err
			}
		}
	}
	if caddyVersionNewer(version, tag) {
		if target != old {
			return "", issueChangedBinary
		}
		tag = version
	}
	if version != tag || target != old {
		data, err := caddyAssetBinary(ctx, updater, asset)
		if err != nil {
			return "", fmt.Errorf("%w：%w", issueCandidate, err)
		}
		digest = fmt.Sprintf("%x", sha256.Sum256(data))
		candidate := filepath.Join(filepath.Dir(old), "caddy-"+tag+"-"+digest)
		if cached, exists, err := protectedCoreMigrationFileDigest(candidate, 100<<20); err != nil {
			return "", err
		} else if !exists {
			if _, err = atomicDeployWithDefaultMetadata(candidate, string(data), fileMetadata{mode: 0755}); err != nil {
				return "", err
			}
		} else if cached != digest {
			return "", errors.New("Caddy 候选程序摘要不一致")
		}
		out, err := m.caddyCommand(ctx, candidate, "version")
		if err != nil || firstWord(out) != tag {
			return "", errors.New("Caddy 候选版本不符")
		}
		if out, err = m.caddyCommand(ctx, candidate, "validate", "--config", filepath.Join(m.root, "caddy.conf")); err != nil {
			return "", issueProxyConfig
		}
		info, err = os.Stat(candidate)
		if err != nil {
			return "", err
		}
		if err = m.switchProxy(ctx, "caddy", service, unit, old, candidate, info); err != nil {
			return "", err
		}
	} else if err = m.verifyRunningProxy(ctx, "caddy", service, old, info); err != nil {
		return "", err
	}
	if err = m.writeConfig(marker, build+" "+digest, true); err != nil {
		return "", err
	}
	return "托管 Caddy 正在运行 " + tag, nil
}

func validateCaddyModuleList(output string, err error) error {
	if err != nil {
		return issueModuleInspection
	}
	if strings.TrimSpace(output) != "" {
		return issueCaddyModules
	}
	return nil
}

// A legacy private path may predate the ownership marker. Prove the identity
// of the running bytes without claiming ownership of an administrator's file.
func verifyHistoricalCaddy(ctx context.Context, updater *CoreUpdater, version, digest string) error {
	if !caddyStableTag.MatchString(version) {
		return issueCaddyVersion
	}
	var release githubRelease
	endpoint := strings.TrimSuffix(updater.apiBase, "/") + "/repos/caddyserver/caddy/releases/tags/" + version
	if err := updater.getJSON(ctx, endpoint, &release); err != nil {
		return fmt.Errorf("%w：%w", issueHistoricalRelease, err)
	}
	if release.TagName != version {
		return issueHistoricalRelease
	}
	asset, err := caddyLinuxAsset(release, updater.goarch)
	if err != nil {
		return fmt.Errorf("%w：%w", issueHistoricalRelease, err)
	}
	data, err := caddyAssetBinary(ctx, updater, asset)
	if err != nil {
		return fmt.Errorf("%w：%w", issueHistoricalRelease, err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
		return issueHistoricalMismatch
	}
	return nil
}

func caddyVersionNewer(version, tag string) bool {
	if !caddyStableTag.MatchString(version) || !caddyStableTag.MatchString(tag) {
		return false
	}
	parts := strings.Split(tag[1:], ".")
	for i, v := range strings.Split(version[1:], ".") {
		a, _ := strconv.Atoi(v)
		b, _ := strconv.Atoi(parts[i])
		if a != b {
			return a > b
		}
	}
	return false
}
