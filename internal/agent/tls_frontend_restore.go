package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type tlsRuntimeSnapshot struct {
	mode, service, content, status, enabled string
	exists                                  bool
}

func (m *tlsFrontendManager) snapshotRuntime(ctx context.Context) ([]tlsRuntimeSnapshot, error) {
	var result []tlsRuntimeSnapshot
	for _, mode := range []string{"caddy", "nginx"} {
		service, unit, _ := m.serviceDefinition(mode, false)
		value, err := os.ReadFile(filepath.Join(m.root, mode+".conf"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		snapshot := tlsRuntimeSnapshot{mode: mode, service: service, content: string(value), exists: err == nil, status: "inactive", enabled: "disabled"}
		if _, err := os.Stat(unit); err == nil {
			if snapshot.status, err = m.services.status(ctx, service); err != nil {
				return nil, err
			}
			if snapshot.enabled, err = serviceEnableState(ctx, service, m.services); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, nil
}

func (m *tlsFrontendManager) restoreRuntime(ctx context.Context, previous tlsDeployments, snapshots []tlsRuntimeSnapshot) error {
	if err := m.sync(ctx, previous); err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		path := filepath.Join(m.root, snapshot.mode+".conf")
		current, _ := os.ReadFile(path)
		changed := string(current) != snapshot.content
		if snapshot.exists {
			if err := m.writeConfig(path, snapshot.content, false); err != nil {
				return err
			}
		} else if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_, unit, _ := m.serviceDefinition(snapshot.mode, false)
		if _, err := os.Stat(unit); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		status, err := m.services.status(ctx, snapshot.service)
		if err != nil {
			return err
		}
		active := snapshot.status == "active" || snapshot.status == "activating" || snapshot.status == "reloading"
		if active && status == "active" && changed {
			var output string
			if snapshot.mode == "caddy" {
				output, err = m.runCaddy(ctx, nil, "reload", "--config", path, "--address", "unix/"+filepath.Join(m.state, "caddy-admin.sock"))
			} else {
				output, err = run(ctx, managedNginxBinary(), "-p", m.state+"/", "-c", path, "-s", "reload")
			}
			if err != nil {
				return fmt.Errorf("恢复 %s 配置：%w；%s", snapshot.service, err, output)
			}
		} else if active != (status == "active") {
			action := core.ActionStop
			if active {
				action = core.ActionStart
			}
			if output, err := serviceCommandAndVerifyWithManager(ctx, m.services, snapshot.service, action); err != nil {
				return fmt.Errorf("恢复 %s 到 %s：%w；%s", snapshot.service, snapshot.status, err, output)
			}
		}
		if err := restoreServiceEnableState(ctx, snapshot.service, snapshot.enabled, m.services); err != nil {
			return err
		}
	}
	return nil
}
