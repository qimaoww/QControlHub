package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/cnip"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func (c *Client) executeTask(ctx context.Context, task core.Task, outgoing chan<- core.WireMessage) {
	c.executeTaskForSession(ctx, ctx, task, outgoing)
}

func (c *Client) executeTaskForSession(executionContext, deliveryContext context.Context, task core.Task, outgoing chan<- core.WireMessage) {
	result := c.resultForTask(executionContext, task)
	message := core.WireMessage{Type: core.WireResult, Result: &core.TaskResultEnvelope{TaskID: task.ID, Result: result}}
	if c.traffic != nil {
		message.TrafficUsage, message.Result.Result.TrafficSettled = c.traffic.settledSnapshot(executionContext)
	}
	select {
	case outgoing <- message:
	case <-deliveryContext.Done():
		return
	}
	// Probe after real lifecycle results (including partial failures), rather
	// than optimistically declaring the service installed/running in the UI.
	// The session loop owns heartbeat collection; a single buffered request
	// coalesces completions without racing its periodic metrics collection.
	switch task.Action {
	case core.ActionInstall, core.ActionDeploy, core.ActionImportExisting,
		core.ActionStart, core.ActionStop, core.ActionRestart, core.ActionEnableBBR, core.ActionDisableBBR, core.ActionConfigureTCP:
		select {
		case c.runtimeRefresh <- struct{}{}:
		default:
		}
	}
	if task.InstallIfMissing && task.Action == core.ActionValidate {
		select {
		case c.runtimeRefresh <- struct{}{}:
		default:
		}
	}
}

func (c *Client) resultForTask(ctx context.Context, task core.Task) core.TaskResultRequest {
	if cached, ok := c.cachedTaskResult(task); ok {
		if c.logs != nil && cached.Success && task.Action == core.ActionImportExisting && task.Engine == core.EngineSingBox {
			if err := c.logs.RefreshImportedSingBoxSource(c.executor); err != nil {
				slog.Warn("refresh cached imported sing-box log source", "error", err)
			}
		}
		slog.Info("returning cached task result", "task_id", task.ID)
		return cached
	}
	c.executionsMu.Lock()
	if c.executions == nil {
		c.executions = make(map[string]*taskExecution)
	}
	c.pruneExecutionsLocked(time.Now())
	if running, ok := c.executions[task.ID]; ok {
		done := running.done
		c.executionsMu.Unlock()
		slog.Info("joining in-flight task after reconnect", "task_id", task.ID)
		select {
		case <-done:
			c.executionsMu.Lock()
			result := running.result
			c.executionsMu.Unlock()
			result.LeaseID = task.LeaseID
			return result
		case <-ctx.Done():
			return core.TaskResultRequest{LeaseID: task.LeaseID, Error: "agent stopped while waiting for the in-flight task"}
		}
	}
	execution := &taskExecution{done: make(chan struct{})}
	c.executions[task.ID] = execution
	c.executionsMu.Unlock()
	// Serialize mutations across reconnects as well as within one WSS session.
	// An upgrade must not replace/re-exec the process during a core migration.
	c.taskLifecycleMu.Lock()
	defer c.taskLifecycleMu.Unlock()

	slog.Info("executing task", "task_id", task.ID, "action", task.Action, "engine", task.Engine)
	execute := c.executeFunc
	var output string
	var executionErr error
	var ipQuality *core.IPQualityResult
	preparedLogTransition := false
	if c.upgradePending {
		executionErr = errors.New("Agent upgrade is waiting for restart; retry after the new Agent reconnects")
	} else if task.Action == core.ActionIPQuality {
		check := c.ipQualityFunc
		if check == nil {
			check = runIPQuality
		}
		report, err := check(ctx)
		if err == nil {
			report, err = core.NormalizeIPQualityResult(&report)
		}
		executionErr = err
		if err == nil {
			ipQuality = &report
			output = "IPQuality check completed"
		}
	} else if task.Action.SystemBBR() {
		output, executionErr = c.bbr.Execute(ctx, task.Action, task.TCPSettings)
	} else if task.Action == core.ActionUpgradeAgent {
		output, executionErr = c.upgradeAgent(ctx)
		if executionErr == nil {
			c.upgradePending = true
			c.executionsMu.Lock()
			c.restartAfterTask = task.ID
			c.executionsMu.Unlock()
		}
	} else {
		if execute == nil {
			execute = c.executor.Execute
		}
		if task.SharedTrafficID != "" {
			endpoints, err := serverconfig.SharedTrafficEndpoints(task.Engine, task.ConfigContent)
			if err != nil {
				executionErr = fmt.Errorf("unsafe shared configuration: %w", err)
			} else if task.Action == core.ActionDeploy {
				executionErr = c.traffic.authorizeSharedDeployment(ctx, task.SharedTrafficID, endpoints)
			} else if task.Action != core.ActionValidate {
				executionErr = errors.New("unsupported shared task action")
			}
		}
		if executionErr == nil {
			task, executionErr = prepareCNIPTask(ctx, task, cnip.FetchRoutes)
		}
		var previousMainlandPolicies []core.MainlandAccessPolicy
		mainlandChanged := executionErr == nil && task.Action == core.ActionDeploy && task.Engine == core.EngineShadowsocksRust && !task.SharedInstance && c.mainland != nil
		if mainlandChanged {
			previousMainlandPolicies = c.mainland.Snapshot()
			if err := c.mainland.Deploy(ctx, task.MainlandAccessPolicies, c.creds.AgentID); err != nil {
				executionErr = fmt.Errorf("apply Shadowsocks Rust mainland access policy: %w", err)
			}
		}
		if executionErr == nil && c.logs != nil && task.Engine == core.EngineSingBox &&
			(task.Action == core.ActionImportExisting || task.Action == core.ActionDeploy) {
			if err := c.logs.PrepareImportedSingBoxSource(ctx, c.executor, task.ConfigContent); err != nil {
				slog.Warn("prepare managed sing-box log capture window", "error", err)
			} else {
				preparedLogTransition = true
			}
		} else if executionErr == nil && c.logs != nil && task.Engine == core.EngineSingBox &&
			(task.Action == core.ActionInstall || task.Action == core.ActionStart || task.Action == core.ActionRestart) {
			if err := c.logs.waitForConsoleSource(ctx, core.EngineSingBox); err != nil {
				slog.Warn("wait for managed sing-box console log source", "error", err)
			}
		}
		if executionErr == nil {
			output, executionErr = execute(ctx, task)
		}
		if executionErr != nil && mainlandChanged {
			rollbackContext, rollbackCancel := context.WithTimeout(context.Background(), 20*time.Second)
			rollbackErr := c.mainland.Deploy(rollbackContext, previousMainlandPolicies, c.creds.AgentID)
			rollbackCancel()
			if rollbackErr != nil {
				executionErr = fmt.Errorf("%v; mainland access rollback failed: %w", executionErr, rollbackErr)
			}
		}
		if preparedLogTransition {
			if err := c.logs.CompleteImportedSingBoxSource(c.executor, executionErr == nil); err != nil {
				slog.Warn("complete managed sing-box log source transition", "error", err)
			}
		}
	}
	if c.logs != nil && task.Engine == core.EngineSingBox && (coreLogSourceMayChange(task.Action) || task.InstallIfMissing) {
		collectorTransitionComplete := preparedLogTransition &&
			(task.Action == core.ActionImportExisting || task.Action == core.ActionDeploy)
		if !collectorTransitionComplete {
			if err := c.logs.RefreshImportedSingBoxSource(c.executor); err != nil {
				slog.Warn("refresh managed sing-box log source", "error", err)
			}
		}
	}
	result := core.TaskResultRequest{
		LeaseID: task.LeaseID, Success: executionErr == nil, Output: output,
		IPQuality: ipQuality,
	}
	if executionErr != nil {
		result.Error = executionErr.Error()
		slog.Warn("task failed", "task_id", task.ID, "error", executionErr)
	} else {
		slog.Info("task completed", "task_id", task.ID)
	}
	if task.Action != core.ActionReadConfig && task.Action != core.ActionReadManagedConfig {
		if err := c.rememberTaskResult(task.ID, result); err != nil {
			slog.Warn("persist completed task result", "task_id", task.ID, "error", err)
			if task.Action == core.ActionUpgradeAgent && result.Success && c.upgradeCommitted != nil {
				rollbackErr := c.upgradeCommitted.rollback()
				result.Success = false
				result.Error = fmt.Sprintf("persist Agent upgrade result: %v; rollback: %v", err, rollbackErr)
				c.credentialsMu.Lock()
				delete(c.creds.CompletedTasks, task.ID)
				c.credentialsMu.Unlock()
				c.executionsMu.Lock()
				c.restartAfterTask = ""
				c.executionsMu.Unlock()
				c.upgradePending = rollbackErr != nil
			}
		}
	}
	c.executionsMu.Lock()
	execution.result = result
	execution.completedAt = time.Now()
	close(execution.done)
	c.executionsMu.Unlock()
	return result
}

func coreLogSourceMayChange(action core.Action) bool {
	switch action {
	case core.ActionImportExisting, core.ActionDeploy, core.ActionInstall, core.ActionStart, core.ActionRestart:
		return true
	default:
		return false
	}
}

func (c *Client) pruneExecutionsLocked(now time.Time) {
	const retention = 10 * time.Minute
	for taskID, execution := range c.executions {
		if !execution.completedAt.IsZero() && now.Sub(execution.completedAt) >= retention {
			delete(c.executions, taskID)
		}
	}
	for len(c.executions) >= 64 {
		oldestID := ""
		var oldest time.Time
		for taskID, execution := range c.executions {
			if execution.completedAt.IsZero() {
				continue
			}
			if oldestID == "" || execution.completedAt.Before(oldest) {
				oldestID, oldest = taskID, execution.completedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(c.executions, oldestID)
	}
}

func (c *Client) acknowledgeTaskResult(taskID string) {
	restart := false
	c.executionsMu.Lock()
	delete(c.executions, taskID)
	if c.restartAfterTask == taskID {
		c.restartAfterTask = ""
		restart = true
	}
	c.executionsMu.Unlock()
	if restart {
		go c.reexecAfterUpgrade()
	}
}

func (c *Client) cachedTaskResult(task core.Task) (core.TaskResultRequest, bool) {
	c.credentialsMu.Lock()
	defer c.credentialsMu.Unlock()
	cached, ok := c.creds.CompletedTasks[task.ID]
	if !ok {
		return core.TaskResultRequest{}, false
	}
	return core.TaskResultRequest{
		LeaseID: task.LeaseID, Success: cached.Success, Output: cached.Output, Error: cached.Error,
		IPQuality: cached.IPQuality,
	}, true
}

func (c *Client) rememberTaskResult(taskID string, result core.TaskResultRequest) error {
	c.credentialsMu.Lock()
	defer c.credentialsMu.Unlock()
	if c.creds.CompletedTasks == nil {
		c.creds.CompletedTasks = make(map[string]completedTask)
	}
	for len(c.creds.CompletedTasks) >= 64 {
		oldestID := ""
		var oldest time.Time
		for id, item := range c.creds.CompletedTasks {
			if oldestID == "" || item.CompletedAt.Before(oldest) {
				oldestID, oldest = id, item.CompletedAt
			}
		}
		delete(c.creds.CompletedTasks, oldestID)
	}
	c.creds.CompletedTasks[taskID] = completedTask{
		Success: result.Success, Output: limitStateValue(result.Output, 4<<10),
		Error: limitStateValue(result.Error, 2<<10), CompletedAt: time.Now().UTC(),
		IPQuality: result.IPQuality,
	}
	if err := c.boundCompletedTaskCache(taskID); err != nil {
		return err
	}
	return saveCredentials(c.config.StatePath, c.creds)
}

func limitStateValue(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "�") + "\n… cached output truncated"
}

func (c *Client) validTask(task core.Task) bool {
	if task.SharedInstance && (task.Action != core.ActionDeploy || !core.ValidAgentShareID(task.SharedTrafficID) ||
		task.InstallIfMissing) {
		return false
	}
	if task.InstallIfMissing && ((task.Action != core.ActionValidate && task.Action != core.ActionDeploy) ||
		task.ConfigVersion < 1 || task.ConfigID == "" || task.ConfigContent == "" || task.SharedTrafficID != "" ||
		task.CoreVersion != "" || task.CoreSource != "") {
		return false
	}
	if task.Action.SystemBBR() {
		if _, err := settingsForTCPAction(task.Action, task.TCPSettings); err != nil {
			return false
		}
	} else if len(task.TCPSettings) > 0 {
		return false
	}
	engineValid := task.Engine.Valid()
	if task.Action.AgentLevel() {
		engineValid = task.Engine == ""
	}
	if task.Action.RequiresAgentManagement() && (task.ConfigID != "" || task.ConfigContent != "" || task.CoreVersion != "" || task.CoreSource != "" || len(task.MainlandAccessPolicies) != 0 || task.SharedTrafficID != "" || task.CNIPSource != nil) {
		return false
	}
	return task.AgentID == c.creds.AgentID && validTaskID(task.ID) && len(task.LeaseID) >= 32 &&
		task.Status == core.TaskRunning && task.Action.Valid() && engineValid
}
