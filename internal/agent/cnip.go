package agent

import (
	"context"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func prepareCNIPTask(ctx context.Context, task core.Task, fetch func(context.Context, core.CNIPSource) ([]string, error)) (core.Task, error) {
	if task.Action != core.ActionValidate && task.Action != core.ActionDeploy {
		return task, nil
	}
	if task.CNIPSource == nil || !task.CNIPSource.Custom() {
		if !strings.Contains(task.ConfigContent, "qch-mainland-") && !strings.Contains(task.ConfigContent, "qch-chnroutes2-cn") {
			return task, nil
		}
		content, err := serverconfig.ApplyCNIPPrefixes(task.Engine, task.ConfigContent, nil)
		task.ConfigContent = content
		return task, err
	}
	needed := false
	for _, p := range serverconfig.DiscoverMainlandAccessPolicies(task.Engine, task.ConfigContent) {
		needed = needed || p.BlockMainlandSource || p.BlockMainlandDestination
	}
	for _, p := range task.MainlandAccessPolicies {
		needed = needed || p.BlockMainlandSource || p.BlockMainlandDestination
	}
	if !needed {
		return task, nil
	}
	prefixes, err := fetch(ctx, *task.CNIPSource)
	if err != nil {
		return task, err
	}
	content, err := serverconfig.ApplyCNIPPrefixes(task.Engine, task.ConfigContent, prefixes)
	if err != nil {
		return task, err
	}
	task.ConfigContent = content
	// Copy before changing the snapshot so rollback still sees its old policy.
	task.MainlandAccessPolicies = append([]core.MainlandAccessPolicy(nil), task.MainlandAccessPolicies...)
	for i := range task.MainlandAccessPolicies {
		task.MainlandAccessPolicies[i].CNIPPrefixes = prefixes
	}
	return task, nil
}
