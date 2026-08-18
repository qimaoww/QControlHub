package webui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestDeploymentDetailOnlyExposesOperationalMetadata(t *testing.T) {
	t.Parallel()
	const credential = "123e4567-e89b-42d3-a456-426614174000"
	content, err := serverconfig.Generate(core.EngineSingBox, serverconfig.Input{
		Protocol: serverconfig.ProtocolVMess, Tag: "managed", Listen: "::", Port: 24443,
		Username: "operator", Credential: credential, Transport: "websocket", TransportPath: "/relay", TLSEnabled: true,
		CertificatePath: "/etc/qcontrolhub/server.crt", PrivateKeyPath: "/etc/qcontrolhub/server.key",
	})
	if err != nil {
		t.Fatal(err)
	}
	detail, ok := deploymentDetailFor(core.EngineSingBox, content)
	if !ok {
		t.Fatal("deployment detail was not parsed")
	}
	if detail.Protocol != "VMess" || detail.Endpoint != "[::]:24443" || detail.Mode != "WebSocket · TLS" {
		t.Fatalf("deployment detail = %+v", detail)
	}
	if strings.Contains(fmt.Sprintf("%+v", detail), credential) {
		t.Fatal("deployment detail leaked client credentials")
	}
	if _, ok := deploymentDetailFor(core.EngineXray, `{"inbounds":[]}`); ok {
		t.Fatal("arbitrary configuration unexpectedly produced a deployment detail")
	}
}

func TestDeploymentStatusRequiresMatchingConfigurationIdentity(t *testing.T) {
	t.Parallel()
	saved := core.Config{ID: "cfg_node", Version: 5}
	tests := []struct {
		name       string
		deployed   core.Deployment
		wantDrift  bool
		wantLabel  string
		wantDetail string
	}{
		{name: "synchronized", deployed: core.Deployment{ConfigID: saved.ID, ConfigVersion: 5}},
		{name: "older revision", deployed: core.Deployment{ConfigID: saved.ID, ConfigVersion: 3}, wantDrift: true, wantLabel: "已保存版本尚未部署", wantDetail: "待部署 v5"},
		{name: "different configuration", deployed: core.Deployment{ConfigID: "cfg_shared", ConfigVersion: 5}, wantDrift: true, wantLabel: "当前运行其他配置", wantDetail: "待部署已保存的 v5"},
		{name: "never deployed", wantDrift: true, wantLabel: "已保存配置尚未部署", wantDetail: "待部署 v5"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := deploymentStatusFor(saved, test.deployed)
			if got.SavedConfigID != saved.ID || got.SavedVersion != saved.Version || got.Drift != test.wantDrift || got.DriftLabel != test.wantLabel || got.DriftDetail != test.wantDetail {
				t.Fatalf("deploymentStatusFor() = %+v", got)
			}
		})
	}
}

func TestDiagnoseTaskExplainsKnownRecoveryPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		task       core.Task
		wantTitle  string
		wantAdvice string
	}{
		{name: "download", task: core.Task{Status: core.TaskFailed, Action: core.ActionInstall, Error: `Get "https://github.com/release.zip": EOF`}, wantTitle: "官方发行包下载未完成", wantAdvice: "重新从官方 Release"},
		{name: "configuration", task: core.Task{Status: core.TaskFailed, Action: core.ActionValidate, Error: "sing-box rejected the configuration: exit status 1"}, wantTitle: "配置未通过真实内核校验", wantAdvice: "修正并保存配置"},
		{name: "rollback", task: core.Task{Status: core.TaskFailed, Action: core.ActionDeploy, Error: "previous configuration was restored"}, wantTitle: "变更失败，已自动回滚", wantAdvice: "先查询服务状态"},
		{name: "lease", task: core.Task{Status: core.TaskFailed, Action: core.ActionRestart, Error: "agent did not report a result before the execution lease expired"}, wantTitle: "Agent 未在执行租约内回写", wantAdvice: "WSS 稳定"},
		{name: "read current configuration", task: core.Task{Status: core.TaskFailed, Action: core.ActionReadConfig, Error: "configuration file permissions are unsafe"}, wantTitle: "无法读取节点当前配置", wantAdvice: "白名单配置路径"},
		{name: "non failure", task: core.Task{Status: core.TaskSucceeded, Action: core.ActionInstall}, wantTitle: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := diagnoseTask(test.task)
			if got.Title != test.wantTitle || (test.wantAdvice != "" && !strings.Contains(got.Advice, test.wantAdvice)) {
				t.Fatalf("diagnoseTask() = %+v", got)
			}
		})
	}
}

func TestSortAgentsForDisplayPrioritizesOnlineAndRecent(t *testing.T) {
	t.Parallel()
	now := time.Now()
	agents := []core.Agent{
		{ID: "offline-new", Status: "offline", LastSeen: now},
		{ID: "online-old", Status: "online", LastSeen: now.Add(-time.Minute)},
		{ID: "online-new", Status: "online", LastSeen: now},
		{ID: "offline-old", Status: "offline", LastSeen: now.Add(-time.Minute)},
	}

	sortAgentsForDisplay(agents)

	want := []string{"online-new", "online-old", "offline-new", "offline-old"}
	for index, agent := range agents {
		if agent.ID != want[index] {
			t.Fatalf("agents[%d].ID = %q, want %q", index, agent.ID, want[index])
		}
	}
}

func TestSelectedAgentForDisplayUsesRequestedOrFirstAgent(t *testing.T) {
	t.Parallel()
	agents := []core.Agent{{ID: "first"}, {ID: "second"}}

	if got := selectedAgentForDisplay("second", agents); got != "second" {
		t.Fatalf("selectedAgentForDisplay(second) = %q", got)
	}
	if got := selectedAgentForDisplay("missing", agents); got != "first" {
		t.Fatalf("selectedAgentForDisplay(missing) = %q, want first", got)
	}
	if got := selectedAgentForDisplay("", nil); got != "" {
		t.Fatalf("selectedAgentForDisplay(empty) = %q", got)
	}
}

func TestSafeReturnToKeepsSelectedAgentWorkspace(t *testing.T) {
	t.Parallel()
	if got := safeReturnTo("/agents?node=agt_123"); got != "/agents?node=agt_123" {
		t.Fatalf("safeReturnTo(selected agent) = %q", got)
	}
	if got := safeReturnTo("https://example.com"); got != "/tasks" {
		t.Fatalf("safeReturnTo(external) = %q, want /tasks", got)
	}
}

func TestTaskTimingDescribesQueueAndExecution(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 3, 10, 0, 0, 0, time.UTC)
	created := now.Add(-2*time.Minute - 15*time.Second)
	started := created.Add(15 * time.Second)
	finished := started.Add(time.Minute + 5*time.Second)

	tests := []struct {
		name string
		task core.Task
		want string
	}{
		{name: "pending", task: core.Task{Status: core.TaskPending, CreatedAt: created}, want: "准备执行"},
		{name: "running", task: core.Task{Status: core.TaskRunning, CreatedAt: created, StartedAt: &started}, want: "已运行 2 分 0 秒"},
		{name: "finished", task: core.Task{Status: core.TaskSucceeded, CreatedAt: created, StartedAt: &started, FinishedAt: &finished}, want: "执行 1 分 5 秒"},
		{name: "canceled before start", task: core.Task{Status: core.TaskCanceled, CreatedAt: created, FinishedAt: &finished}, want: "未开始执行"},
		{name: "incomplete running", task: core.Task{Status: core.TaskRunning, CreatedAt: created}, want: "正在启动执行"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := taskTimingAt(test.task, now); got != test.want {
				t.Fatalf("taskTimingAt() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFormatTaskDurationKeepsUsefulPrecision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value time.Duration
		want  string
	}{
		{value: 500 * time.Millisecond, want: "不足 1 秒"},
		{value: 42 * time.Second, want: "42 秒"},
		{value: 3*time.Minute + 4*time.Second, want: "3 分 4 秒"},
		{value: 2*time.Hour + 7*time.Minute, want: "2 小时 7 分"},
		{value: 26 * time.Hour, want: "1 天 2 小时"},
	}
	for _, test := range tests {
		if got := formatTaskDuration(test.value); got != test.want {
			t.Errorf("formatTaskDuration(%s) = %q, want %q", test.value, got, test.want)
		}
	}
}
