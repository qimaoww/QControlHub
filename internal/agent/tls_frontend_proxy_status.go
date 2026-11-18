package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func readProxyReports() map[string]core.ProxyStatus {
	result := map[string]core.ProxyStatus{}
	for _, mode := range []string{"nginx", "caddy"} {
		raw, err := readConfigurationFile(filepath.Join(tlsFrontendRequests, mode+"-status.json"))
		var row core.ProxyStatus
		if err == nil && json.Unmarshal([]byte(raw), &row) == nil {
			result[mode] = row
		}
	}
	return result
}

// The durable report contains only component versions and aggregate counts.
// Task diagnostics stay on the authorized task, separate from the node view.
func (m *tlsFrontendManager) saveProxyObservation(ctx context.Context, mode string, change func(*core.ProxyStatus)) error {
	previous := readProxyReports()[mode]
	row := core.ProxyStatus{Latest: previous.Latest, CheckedAt: previous.CheckedAt, Outcome: previous.Outcome, ObservedAt: previous.ObservedAt, State: "unknown"}
	deployments, err := m.load()
	if err == nil {
		ports := map[int]bool{}
		for service, d := range deployments {
			shared := false
			for _, f := range d.Frontends {
				if f.Mode == mode {
					row.Managed = true
					if !d.Stopped {
						ports[f.PublicPort] = true
						shared = strings.Contains(service, "@") || strings.Contains(service, "-shr_")
					}
				}
			}
			if shared {
				row.SharedInstances++
			}
		}
		row.Listeners = len(ports)
		m.observeProxyProcess(ctx, mode, &row)
	}
	if change != nil {
		change(&row)
	}
	if row.Installed != "" && row.Latest != "" && row.Installed != row.Latest {
		row.NewerInstalled = m.proxyNewer(ctx, mode, row.Installed, row.Latest)
	}
	if previous == row && time.Since(row.ObservedAt) < 5*time.Minute {
		return nil
	}
	path := filepath.Join(tlsFrontendRequests, mode+"-status.json")
	row.ObservedAt = time.Now().UTC()
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	backup, err := atomicDeploy(path, string(data))
	if backup != "" {
		_ = os.Remove(backup)
	}
	return err
}

func (m *tlsFrontendManager) observeProxyProcess(ctx context.Context, mode string, row *core.ProxyStatus) {
	binary := m.proxyBinary(mode)
	installed, err := os.Stat(binary)
	if errors.Is(err, os.ErrNotExist) {
		row.State = "missing"
		return
	}
	if err != nil || validatePrivilegedExecutable(binary) != nil {
		return
	}
	row.Installed = m.proxyVersion(ctx, mode, binary)
	if !row.Managed {
		row.State = "unmanaged"
		return
	}
	service, _, _ := m.serviceDefinition(mode, false)
	state, err := m.services.status(ctx, service)
	if err != nil {
		return
	}
	if state != "active" {
		row.State = state
		return
	}
	if m.checkProxyUnit(ctx, mode) != nil {
		return
	}
	pid, err := managedServiceProcessPID(ctx, m.services, service)
	if err != nil {
		return
	}
	proc := filepath.Join(openRCProcRoot, strconv.Itoa(pid), "exe")
	path, err := os.Readlink(proc)
	if err != nil {
		return
	}
	path = strings.TrimSuffix(path, " (deleted)")
	if mode == "nginx" && path != systemNginxBinary() && privateNginxRevision(path) == "" || mode == "caddy" && path != managedCaddyBinary && !caddyPrivateBinary(path) {
		return
	}
	running, err := m.proxyMasterExecutable(ctx, mode, service, path)
	if err != nil {
		return
	}
	if os.SameFile(running, installed) {
		row.State = "active"
		row.Running = row.Installed
		return
	}
	row.State = "pending"
	if disk, e := os.Stat(path); e == nil && os.SameFile(disk, running) {
		row.Running = m.proxyVersion(ctx, mode, path)
		return
	}
	if privateNginxRevision(path) != "" {
		row.Running = privateNginxRevision(path)
	} else if caddyPrivateBinary(path) {
		row.Running = caddyPrivateName.FindStringSubmatch(filepath.Base(path))[1]
	}
}

func (m *tlsFrontendManager) proxyVersion(ctx context.Context, mode, path string) string {
	if mode == "nginx" {
		if v := privateNginxRevision(path); v != "" {
			return v
		}
		p, err := nginxHostPlatform()
		if err == nil {
			v, _ := systemNginxRevision(ctx, p, path)
			return v
		}
		return ""
	}
	output, err := m.caddyCommand(ctx, path, "version")
	v := firstWord(output)
	if err != nil || !caddyStableTag.MatchString(v) {
		return ""
	}
	return v
}

func (m *tlsFrontendManager) proxyOperation(ctx context.Context, mode string, action core.Action, build string, explicit bool) error {
	var latest, output string
	var err error
	if action.ChecksProxy() {
		if mode == "nginx" {
			var p nginxPlatform
			p, err = nginxHostPlatform()
			if err == nil {
				_, latest, err = fetchOfficialNginx(ctx, p, "", "check")
			}
		} else {
			_, latest, _, err = m.caddyCandidate(ctx)
		}
	} else if err = m.recoverProxySwitch(ctx, mode); err != nil {
		err = fmt.Errorf("中断的托管切换恢复失败：%w", err)
	} else if mode == "nginx" {
		output, err = m.upgradeManagedNginx(ctx, build, explicit, m.upgradeNginxBinary)
	} else {
		output, err = m.upgradeManagedCaddy(ctx, build, explicit)
	}
	if action.ProxyComponent() != "" && !action.ChecksProxy() && output == "" && err == nil {
		err = errors.New("此组件没有运行中的托管监听")
	}
	saveErr := m.saveProxyObservation(ctx, mode, func(row *core.ProxyStatus) {
		if !action.ChecksProxy() && err == nil && output == "" {
			return
		}
		if err != nil {
			if action.ChecksProxy() {
				row.Outcome = "检查失败"
			} else if strings.Contains(err.Error(), "恢复失败") {
				row.Outcome = "恢复失败"
			} else {
				row.Outcome = "更新失败"
			}
			return
		}
		if latest == "" {
			latest = row.Running
		}
		row.Latest = latest
		row.CheckedAt = time.Now().UTC()
		row.Outcome = ""
	})
	if output != "" {
		fmt.Println(output)
	}
	if action.ChecksProxy() && latest != "" {
		fmt.Printf("%s 官方最新版 %s\n", mode, latest)
	}
	if err != nil {
		return errors.Join(fmt.Errorf("%s：%w", mode, err), saveErr)
	}
	return saveErr
}

func (e *Executor) runProxyTask(ctx context.Context, action core.Action, build string) (string, error) {
	services := e.serviceManager().Kind()
	req := tlsFrontendRequest{Action: action, ProxyBuild: build, Service: DefaultSpecsForServiceManager(services)[core.EngineSingBox].Service, Kind: services, Deadline: tlsOperationDeadline(ctx)}
	var log strings.Builder
	err := e.runTLSFrontendRequest(ctx, req, &log)
	return log.String(), err
}

func (c *Client) observeProxies(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		services := c.executor.serviceManager().Kind()
		req := tlsFrontendRequest{ObserveProxies: true, Action: core.ActionStatus, Service: DefaultSpecsForServiceManager(services)[core.EngineSingBox].Service, Kind: services, Deadline: tlsOperationDeadline(ctx)}
		if err := c.executor.runTLSFrontendRequest(ctx, req); err != nil && ctx.Err() == nil {
			slog.Warn("读取托管代理状态失败", "error", core.DiagnosticText(err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *tlsFrontendManager) proxyNewer(ctx context.Context, mode, installed, latest string) bool {
	if mode == "caddy" {
		return caddyVersionNewer(installed, latest)
	}
	p, err := nginxHostPlatform()
	if err != nil {
		return false
	}
	switch p.family {
	case "debian", "ubuntu":
		_, err = run(ctx, "/usr/bin/dpkg", "--compare-versions", installed, "gt", latest)
		return err == nil
	case "alpine":
		apk, _ := findIPQualityExecutable("apk")
		v, err := run(ctx, apk, "version", "-t", installed, latest)
		return err == nil && v == ">"
	default:
		if !nginxOfficialRevision.MatchString(installed) || !nginxOfficialRevision.MatchString(latest) {
			return false
		}
		rpm, _ := findIPQualityExecutable("rpm")
		v, err := run(ctx, rpm, "--eval", "%{lua:print(rpm.vercmp('"+installed+"','"+latest+"'))}")
		return err == nil && v == "1"
	}
}
