package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (m *tlsFrontendManager) selectProxyUnit(ctx context.Context, mode, unit, binary string) error {
	_, _, content := m.serviceDefinitionForBinary(mode, false, binary)
	backup, err := atomicDeployWithDefaultMetadata(unit, content, fileMetadata{mode: 0644})
	if backup != "" {
		_ = os.Remove(backup)
	}
	if err == nil && m.services.Kind() == ServiceManagerSystemd {
		_, err = run(ctx, m.services.executable, "daemon-reload")
	}
	return err
}

// The previous executable is retained until business traffic and MainPID have
// both been verified. Recover under the deployment lock before consulting the
// upstream again, even when the previous helper was killed during restart.
func (m *tlsFrontendManager) recoverProxySwitch(ctx context.Context, mode string) error {
	path := filepath.Join(m.root, mode+"-switch.pending")
	data, err := readConfigurationFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	record := strings.Split(data, "\n")
	if len(record) != 3 {
		return errors.New("托管切换记录无效")
	}
	old, target := record[0], record[1]
	allowed := func(p string) bool {
		if mode == "caddy" {
			return p == managedCaddyBinary || caddyPrivateBinary(p)
		}
		return p == systemNginxBinary() || privateNginxRevision(p) != ""
	}
	if !allowed(old) || !allowed(target) {
		return errors.New("托管程序路径已变更")
	}
	if err = m.checkProxyUnit(ctx, mode); err != nil {
		return err
	}
	selected := m.proxyBinary(mode)
	if selected != old && selected != target {
		return errors.New("托管单元已变更")
	}
	digest, exists, err := protectedCoreMigrationFileDigest(old, 100<<20)
	if err != nil || !exists || digest != record[2] {
		return errors.New("原托管程序已被更改")
	}
	service, unit, _ := m.serviceDefinition(mode, false)
	state, err := m.services.status(ctx, service)
	if err != nil {
		return err
	}
	if state == "active" {
		pid, err := managedServiceProcessPID(ctx, m.services, service)
		if err != nil {
			return err
		}
		id, err := readOpenRCProcessIdentity(pid)
		if err != nil {
			return err
		}
		if id.Executable != old && id.Executable != target {
			return errors.New("当前服务运行其他程序")
		}
		if _, err = m.proxyMasterExecutable(ctx, mode, service, id.Executable); err != nil {
			return err
		}
	}
	if err = m.selectProxyUnit(ctx, mode, unit, old); err != nil {
		return err
	}
	if state == "inactive" {
		return errors.New("服务已停止，已保留原托管单元")
	}
	info, err := os.Stat(old)
	if err != nil {
		return err
	}
	running, err := m.proxyMasterExecutable(ctx, mode, service, old)
	if err != nil || !os.SameFile(info, running) {
		if _, err = m.services.command(ctx, service, core.ActionRestart); err != nil {
			return err
		}
	}
	if err = m.verifyRunningProxy(ctx, mode, service, old, info); err != nil {
		return err
	}
	return os.Remove(path)
}
