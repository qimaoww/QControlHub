package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const bbrConfigPath = "/etc/sysctl.d/90-qcontrolhub-bbr.conf"
const bbrConfigMarker = "# Managed by QControlHub system BBR; do not edit.\n"
const bbrAlgorithmKey = "net.ipv4.tcp_congestion_control"
const bbrQdiscKey = "net.core.default_qdisc"

func bbrParameterKeys() []string {
	keys := []string{"net.ipv4.tcp_available_congestion_control", "net.ipv4.tcp_allowed_congestion_control"}
	for _, rule := range core.TCPParameterRules() {
		keys = append(keys, rule.Key)
	}
	return keys
}

type SystemBBRManager struct {
	mu       sync.Mutex
	status   *core.SystemBBRStatus
	services *ServiceManager
	backend  *systemBBRBackend
}

type systemBBRBackend struct {
	configPath string
	read       func(string) (string, error)
	write      func(string, string) error
	persist    func(string) error
}

func newSystemBBRBackend() *systemBBRBackend {
	b := &systemBBRBackend{configPath: bbrConfigPath}
	b.read = func(key string) (string, error) {
		data, err := os.ReadFile("/proc/sys/" + strings.ReplaceAll(key, ".", "/"))
		return strings.TrimSpace(string(data)), err
	}
	b.write = func(key, value string) error {
		// Only allowlisted keys and validated values reach this method. Never
		// execute a shell, sysctl -p/-system, or a panel-supplied command.
		return os.WriteFile("/proc/sys/"+strings.ReplaceAll(key, ".", "/"), []byte(value+"\n"), 0o644)
	}
	b.persist = func(content string) error {
		_, err := atomicDeployWithDefaultMetadata(b.configPath, content, fileMetadata{mode: 0o644})
		return err
	}
	return b
}

func NewSystemBBRManager(services *ServiceManager) *SystemBBRManager {
	return &SystemBBRManager{services: selectedServiceManager(services), backend: newSystemBBRBackend()}
}

func (m *SystemBBRManager) Cached() *core.SystemBBRStatus {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Published snapshots are immutable, including their slices and maps.
	return m.status
}

func (m *SystemBBRManager) Collect(ctx context.Context) *core.SystemBBRStatus {
	if m == nil {
		return nil
	}
	status := m.backend.snapshot()
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	status.Qdiscs, status.QdiscError = collectSystemQdiscs(probeCtx)
	m.mu.Lock()
	m.status = &status
	m.mu.Unlock()
	return &status
}

func (b *systemBBRBackend) snapshot() core.SystemBBRStatus {
	status := core.SystemBBRStatus{CollectedAt: time.Now().UTC(), Parameters: map[string]string{}, Qdiscs: []core.SystemQdisc{}, Persistence: "unmanaged"}
	status.KernelRelease, _ = b.read("kernel.osrelease")
	for _, key := range bbrParameterKeys() {
		value, err := b.read(key)
		if err == nil {
			status.Parameters[key] = value
		}
		if err != nil && (key == bbrAlgorithmKey || key == bbrQdiscKey) {
			status.Error = "无法读取部分系统 TCP 参数：" + key + ": " + err.Error()
		}
	}
	status.CongestionControl = status.Parameters[bbrAlgorithmKey]
	status.DefaultQdisc = status.Parameters[bbrQdiscKey]
	// Containers may expose TCP congestion control without default_qdisc.
	// Still report the real BBR state and allow editing available TCP keys.
	status.Available = status.CongestionControl != ""
	status.AvailableAlgorithms = strings.Fields(status.Parameters["net.ipv4.tcp_available_congestion_control"])
	contents, exists, err := b.config()
	if err != nil {
		status.Persistence = "error"
		status.Error = strings.TrimSpace(status.Error + " " + err.Error())
	} else if exists {
		status.Persistence = "managed"
		status.ConfiguredParameters, _ = parseBBRConfig(contents)
		status.ConfiguredAlgorithm = status.ConfiguredParameters[bbrAlgorithmKey]
		status.ConfiguredQdisc = status.ConfiguredParameters[bbrQdiscKey]
	}
	return status
}

func (b *systemBBRBackend) config() (string, bool, error) {
	info, err := os.Lstat(b.configPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() > 4096 {
		return "", true, errors.New("BBR 托管配置不是受保护的普通文件")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(b.configPath)); err != nil {
		return "", true, err
	}
	if err := validateOwner(info, "BBR configuration"); err != nil {
		return "", true, err
	}
	contents, err := os.ReadFile(b.configPath)
	if err != nil {
		return "", true, err
	}
	if _, err := parseBBRConfig(string(contents)); err != nil {
		return "", true, errors.New("BBR 托管配置已被外部修改；请先人工核对，Agent 不会覆盖")
	}
	return string(contents), true, nil
}

func bbrProfile(settings core.TCPSettings) string {
	keys := sortedTCPKeys(settings)
	var result strings.Builder
	result.WriteString(bbrConfigMarker)
	for _, key := range keys {
		fmt.Fprintf(&result, "%s = %s\n", key, settings[key])
	}
	return result.String()
}

func parseBBRConfig(content string) (core.TCPSettings, error) {
	if !strings.HasPrefix(content, bbrConfigMarker) {
		return nil, errors.New("missing managed TCP configuration marker")
	}
	settings := core.TCPSettings{}
	for _, line := range strings.Split(strings.TrimPrefix(content, bbrConfigMarker), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, " = ")
		if !ok || settings[key] != "" {
			return nil, errors.New("invalid or duplicate TCP configuration key")
		}
		settings[key] = value
	}
	normalized, err := core.NormalizeTCPSettings(settings)
	if err != nil {
		return nil, err
	}
	if bbrProfile(normalized) != content {
		return nil, errors.New("TCP configuration was modified outside QControlHub")
	}
	return normalized, nil
}

func sortedTCPKeys(settings core.TCPSettings) []string {
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func settingsForTCPAction(action core.Action, settings core.TCPSettings) (core.TCPSettings, error) {
	if !action.SystemBBR() {
		return nil, errors.New("unsupported TCP action")
	}
	if action != core.ActionConfigureTCP {
		if len(settings) > 0 {
			return nil, errors.New("only configure-tcp accepts custom parameters")
		}
		algorithm := "bbr"
		if action == core.ActionDisableBBR {
			algorithm = "cubic"
		}
		settings = core.TCPSettings{bbrAlgorithmKey: algorithm, bbrQdiscKey: "fq"}
	}
	return core.NormalizeTCPSettings(settings)
}

// apply is transactional on ordinary failures. A process/host crash can leave
// runtime defaults different from the persisted profile; telemetry exposes that
// drift. It never rewrites existing interface queues or restarts networking.
func (b *systemBBRBackend) apply(ctx context.Context, action core.Action, input core.TCPSettings) (string, error) {
	settings, err := settingsForTCPAction(action, input)
	if err != nil {
		return "", err
	}
	previousConfig, existed, err := b.config()
	if err != nil {
		return "", err
	}
	persisted := core.TCPSettings{}
	if existed {
		persisted, _ = parseBBRConfig(previousConfig)
	}
	previous := core.TCPSettings{}
	keys := sortedTCPKeys(settings)
	for _, key := range keys {
		value, err := b.read(key)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", key, err)
		}
		previous[key] = strings.Join(strings.Fields(value), " ")
		persisted[key] = settings[key]
	}
	var attempted []string
	rollback := func(cause error, restoreFile bool) (string, error) {
		var failures []error
		if restoreFile {
			if existed {
				failures = append(failures, b.persist(previousConfig))
			} else {
				removeErr := os.Remove(b.configPath)
				if !errors.Is(removeErr, os.ErrNotExist) {
					failures = append(failures, removeErr)
				}
			}
		}
		for i := len(attempted) - 1; i >= 0; i-- {
			key := attempted[i]
			failures = append(failures, b.write(key, previous[key]))
		}
		if rollbackErr := errors.Join(failures...); rollbackErr != nil {
			return "", fmt.Errorf("%w; rollback failed: %v", cause, rollbackErr)
		}
		return "", fmt.Errorf("%w; previous TCP defaults restored", cause)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Selecting an algorithm asks the kernel to load its own tcp_<name>
	// module if needed. No downloaded modules or arbitrary modprobe options.
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return rollback(err, false)
		}
		attempted = append(attempted, key)
		if err := b.write(key, settings[key]); err != nil {
			return rollback(fmt.Errorf("apply %s (kernel support / CAP_NET_ADMIN required): %w", key, err), false)
		}
	}
	for _, key := range keys {
		value, err := b.read(key)
		if err != nil || strings.Join(strings.Fields(value), " ") != settings[key] {
			return rollback(fmt.Errorf("kernel did not apply %s", key), false)
		}
	}
	if err := ctx.Err(); err != nil {
		return rollback(err, false)
	}
	if err := b.persist(bbrProfile(persisted)); err != nil {
		return rollback(fmt.Errorf("persist TCP profile: %w", err), true)
	}
	return fmt.Sprintf("系统 TCP 参数已逐项核验并保存到 %s：\n%s现有连接和网卡队列未重置，其他 sysctl 配置可能在重启时覆盖此文件。", b.configPath, strings.TrimPrefix(bbrProfile(settings), bbrConfigMarker)), nil
}

// RunSystemBBR is the fixed local utility used by the systemd transient helper
// and directly by OpenRC. Lock across processes, including a disconnected
// systemd-run client, so two transactions cannot interleave.
func RunSystemBBR(ctx context.Context, action core.Action, settings core.TCPSettings) (string, error) {
	if !action.SystemBBR() || os.Geteuid() != 0 {
		return "", errors.New("system BBR requires root and a supported action")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(bbrConfigPath)); err != nil {
		return "", err
	}
	lock, err := os.OpenFile(filepath.Dir(bbrConfigPath)+"/.qcontrolhub-bbr.lock", os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return "", errors.New("another BBR operation is in progress; retry after it finishes")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return newSystemBBRBackend().apply(ctx, action, settings)
}

func (m *SystemBBRManager) Execute(parent context.Context, action core.Action, settings core.TCPSettings) (string, error) {
	if m == nil || !action.SystemBBR() {
		return "", errors.New("system BBR manager unavailable")
	}
	if _, err := settingsForTCPAction(action, settings); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	if m.services.Kind() == ServiceManagerOpenRC {
		return RunSystemBBR(ctx, action, settings)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	for _, path := range []string{executable, "/usr/bin/systemd-run"} {
		if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
			return "", err
		}
		if err := validatePrivilegedExecutable(path); err != nil {
			return "", err
		}
	}
	return run(ctx, "/usr/bin/systemd-run", bbrHelperArguments(executable, action, settings)...)
}

func bbrHelperArguments(executable string, action core.Action, settings core.TCPSettings) []string {
	normalized, _ := settingsForTCPAction(action, settings)
	writable := []string{"/etc/sysctl.d"}
	for _, key := range sortedTCPKeys(normalized) {
		writable = append(writable, "/proc/sys/"+strings.ReplaceAll(key, ".", "/"))
	}
	encoded, _ := json.Marshal(settings)
	return []string{"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec",
		"--property=User=root", "--property=UMask=0022", "--property=RuntimeMaxSec=30s",
		"--property=NoNewPrivileges=yes", "--property=CapabilityBoundingSet=CAP_NET_ADMIN",
		"--property=AmbientCapabilities=CAP_NET_ADMIN", "--property=ProtectSystem=strict",
		"--property=ProtectHome=yes", "--property=PrivateTmp=yes", "--property=PrivateDevices=yes",
		"--property=ReadOnlyPaths=/proc/sys", "--property=ReadWritePaths=" + strings.Join(writable, " "),
		"--property=RestrictAddressFamilies=AF_UNIX", "--", executable, "system-bbr", string(action), string(encoded)}
}

func collectSystemQdiscs(ctx context.Context) ([]core.SystemQdisc, string) {
	for _, candidate := range []string{"/usr/sbin/tc", "/sbin/tc", "/usr/bin/tc"} {
		path, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		if validateProtectedDirectoryChain(filepath.Dir(path)) != nil || validatePrivilegedExecutable(path) != nil {
			continue
		}
		output, err := run(ctx, path, "-j", "qdisc", "show")
		if err != nil {
			return []core.SystemQdisc{}, "无法读取网卡队列：" + err.Error()
		}
		var entries []struct {
			core.SystemQdisc
			Dev string `json:"dev"`
		}
		if err := json.Unmarshal([]byte(output), &entries); err != nil || len(entries) > 256 {
			return []core.SystemQdisc{}, "无法解析 tc 队列状态"
		}
		result := make([]core.SystemQdisc, 0, len(entries))
		for _, entry := range entries {
			entry.Device = entry.Dev
			result = append(result, entry.SystemQdisc)
		}
		return result, ""
	}
	return []core.SystemQdisc{}, "未安装可用的 iproute2 tc；默认参数仍可查看，实际网卡队列未知"
}
