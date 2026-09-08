package api

import (
	"context"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type blockingClientAccessSource struct {
	started chan string
	release chan struct{}
}

func (source *blockingClientAccessSource) wait(ctx context.Context, name string) error {
	select {
	case source.started <- name:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-source.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (source *blockingClientAccessSource) ListAgents(ctx context.Context) ([]core.Agent, error) {
	return nil, source.wait(ctx, "agents")
}

func (source *blockingClientAccessSource) DeployedConfigs(ctx context.Context) ([]store.DeployedConfig, error) {
	return nil, source.wait(ctx, "deployed-configs")
}

func TestLoadClientAccessSnapshotStartsIndependentReadsTogether(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	source := &blockingClientAccessSource{
		started: make(chan string, 2),
		release: make(chan struct{}),
	}
	done := make(chan error, 1)
	go func() {
		_, err := loadClientAccessSnapshot(ctx, source)
		done <- err
	}()

	started := make(map[string]bool, 2)
	for len(started) < 2 {
		select {
		case name := <-source.started:
			started[name] = true
		case <-ctx.Done():
			t.Fatalf("client access reads started serially: %v", started)
		}
	}
	close(source.release)
	if err := <-done; err != nil {
		t.Fatalf("load client access snapshot: %v", err)
	}
}
