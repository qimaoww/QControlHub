package agent

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (e *Executor) migrateNativeAccounting(ctx context.Context) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		e.specsMu.RLock()
		spec, ok := e.Specs[engine]
		issue := e.ExistingDiscoveryIssues[engine]
		_, pendingImport := e.ExistingSpecs[engine]
		e.specsMu.RUnlock()
		if !ok || pendingImport || issue != "" || spec != DefaultSpecsForServiceManager(e.serviceManager().Kind())[engine] {
			continue
		}
		migration, cancel := context.WithTimeout(ctx, 45*time.Second)
		func() {
			defer cancel()
			if e.serviceManager().Kind() == ServiceManagerOpenRC {
				current, err := os.ReadFile(filepath.Join(openRCInitRoot, spec.Service))
				if err != nil {
					return
				}
				wanted, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/openrc/"+spec.Service)
				if err != nil {
					return
				}
				legacy := bytes.ReplaceAll(wanted, []byte("^cap_net_bind_service,^cap_net_admin"), []byte("^cap_net_bind_service"))
				if !bytes.Equal(current, wanted) && !bytes.Equal(current, legacy) {
					slog.Warn("managed traffic migration refused customized OpenRC script", "engine", engine)
					return
				}
			}
			if _, err := validateManagedServiceForExistingDiscovery(migration, engine, spec, e.serviceManager()); err != nil {
				slog.Warn("managed traffic migration refused unrecognized service", "engine", engine, "error", err)
				return
			}
			status, err := serviceStatusWithManager(migration, e.serviceManager(), spec.Service)
			if err != nil || strings.TrimSpace(status) != "active" {
				return
			}
			content, err := readConfigurationFile(spec.ConfigPath)
			if err != nil {
				return
			}
			prepared, warning := e.prepareNativeAccountingContent(migration, engine, spec, content)
			if warning != "" {
				slog.Warn("managed traffic migration deferred", "engine", engine, "reason", warning)
				return
			}
			if prepared == content {
				return
			}
			// Execute validates before activation, backs up the fixed config and
			// restores/restarts the previous revision if the service fails.
			if _, err := e.Execute(migration, core.Task{Engine: engine, Action: core.ActionDeploy, ConfigContent: prepared}); err != nil {
				slog.Warn("managed traffic migration failed", "engine", engine, "error", err)
				return
			}
			slog.Info("managed traffic migration completed", "engine", engine)
		}()
	}
}
