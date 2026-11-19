package agent

import (
	"context"
	"errors"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Persist only reviewed messages in node-wide observations. A task may include
// command output, endpoints or paths which do not belong in a node report.
type proxyIssue string

func (issue proxyIssue) Error() string { return string(issue) }

const (
	issueTimeout            proxyIssue = "节点操作超时，请查看实际运行版本并在连接恢复后重试"
	issuePlatform           proxyIssue = "此系统或架构没有受支持的官方软件包"
	issueAsset              proxyIssue = "官方稳定发布缺少适配当前架构且可校验的 Linux 产物"
	issueCandidate          proxyIssue = "无法获取或校验官方候选版本，请核对发布源可达性、平台和校验结果后重试"
	issueHistoricalRelease  proxyIssue = "无法取得或校验当前版本的官方历史发布包，请核对节点到 GitHub 的连接和发布资产后重试"
	issueHistoricalMismatch proxyIssue = "当前程序与同版本官方 Linux 发布包的摘要不同，请核对原安装渠道并通过该渠道维护"
	issueCaddyModules       proxyIssue = "当前 Caddy 含非标准或未知模块，请使用保留这些模块的构建更新"
	issueModuleInspection   proxyIssue = "无法读取当前 Caddy 模块清单，请核对服务账号的执行权限后重试"
	issueCaddyVersion       proxyIssue = "无法识别当前 Caddy 稳定版本，请核对原构建来源"
	issueOwnershipFile      proxyIssue = "安装记录无法安全读取，请核对记录的所有者和权限后重试"
	issueChangedBinary      proxyIssue = "托管程序或运行身份已变更，请核对服务单元和主进程"
	issueProxyConfig        proxyIssue = "候选配置校验失败，请核对托管配置、所需模块和服务账号权限后重试"
	issueRolledBack         proxyIssue = "切换失败，已恢复并验证原运行版本"
	issueRecovery           proxyIssue = "原托管服务尚未完成恢复，请核对服务状态和运行版本后重试"
	issueUpdate             proxyIssue = "请核对节点服务状态和任务详情后重试"
	issueStopped            proxyIssue = "服务已停止，请先确认托管监听应当运行"
	issueNoListeners        proxyIssue = "此组件没有运行中的托管监听"
	issueNewerInstalled     proxyIssue = "当前版本较新，已阻止降级"
)

// Observation and mutations take the same inter-process lock. When another
// helper obtains it, a persisted start without a finish is an interrupted run.
func reconcileProxyAttempt(row *core.ProxyStatus, at time.Time) {
	if row.UpdateStartedAt.IsZero() {
		return
	}
	row.UpdateStartedAt = time.Time{}
	row.UpdateAt, row.UpdateState = at, "interrupted"
	row.UpdateError, row.Outcome = "更新中断，请核对运行版本并重试以恢复托管切换", "更新失败"
}

func proxyResultIssue(err error, fallback proxyIssue) proxyIssue {
	var issue proxyIssue
	if errors.As(err, &issue) {
		return issue
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return issueTimeout
	}
	return fallback
}

func recordProxyResult(row *core.ProxyStatus, check bool, latest string, err error, at time.Time) {
	// Migrate a v1 cached failure before a different operation overwrites it.
	if row.UpdateState == "" {
		switch row.Outcome {
		case "恢复失败":
			row.UpdateState, row.UpdateError = "recovery_failed", issueRecovery.Error()
		case "更新失败":
			row.UpdateState, row.UpdateError = "failed", issueUpdate.Error()
		case "检查失败":
			row.CheckError = issueCandidate.Error()
		}
	}
	if latest != "" && (!check || err == nil) {
		row.Latest, row.CheckedAt, row.CheckAttemptAt, row.CheckError = latest, at, at, ""
	}
	if check {
		row.CheckAttemptAt = at
		if err != nil {
			row.CheckError = proxyResultIssue(err, issueCandidate).Error()
		}
	} else {
		row.UpdateAt, row.UpdateState, row.UpdateError = at, "succeeded", ""
		if err != nil {
			issue := proxyResultIssue(err, issueUpdate)
			row.UpdateState, row.UpdateError = "failed", issue.Error()
			switch issue {
			case issueRecovery:
				row.UpdateState = "recovery_failed"
			case issueRolledBack:
				row.UpdateState = "rolled_back"
			case issueCaddyModules, issueHistoricalMismatch, issueOwnershipFile, issueChangedBinary, issueCaddyVersion, issueStopped, issueNoListeners:
				row.UpdateState = "blocked"
			}
		}
	}
	switch {
	case row.UpdateState == "recovery_failed":
		row.Outcome = "恢复失败"
	case row.UpdateError != "":
		row.Outcome = "更新失败"
	case row.CheckError != "":
		row.Outcome = "检查失败"
	default:
		row.Outcome = ""
	}
}
