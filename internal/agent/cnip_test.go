package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestCNIPTaskPreparationFailsClosed(t *testing.T) {
	source := &core.CNIPSource{URL: "https://example.com/cn.txt", Format: "txt"}
	content, err := serverconfig.ApplyMainlandAccessPolicyWithPrefixes(core.EngineXray, `{"inbounds":[{"tag":"a","port":1080}]}`, core.MainlandAccessPolicy{Tag: "a", Port: 1080, BlockMainlandSource: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	task := core.Task{Action: core.ActionDeploy, Engine: core.EngineXray, ConfigContent: content, CNIPSource: source}
	offline := errors.New("download failed")
	got, err := prepareCNIPTask(context.Background(), task, func(context.Context, core.CNIPSource) ([]string, error) { return nil, offline })
	if !errors.Is(err, offline) || got.ConfigContent != content {
		t.Fatal("failure changed source or silently used default")
	}
	got, err = prepareCNIPTask(context.Background(), task, func(_ context.Context, s core.CNIPSource) ([]string, error) {
		if s != *source {
			t.Fatal("wrong task snapshot")
		}
		return []string{"1.0.1.0/24"}, nil
	})
	if err != nil || !strings.Contains(got.ConfigContent, "1.0.1.0/24") {
		t.Fatalf("%v %s", err, got.ConfigContent)
	}
	task.Action = core.ActionRestart
	if _, err := prepareCNIPTask(context.Background(), task, func(context.Context, core.CNIPSource) ([]string, error) {
		t.Fatal("restart downloaded source")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}
