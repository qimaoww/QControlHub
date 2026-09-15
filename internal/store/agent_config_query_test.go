package store

import (
	"context"
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAgentConfigsDoNotReadOtherNodes(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	first, _ := seedPerformanceAgent(t, s, 0)
	second, _ := seedPerformanceAgent(t, s, 0)
	input := core.Config{AgentID: second, Name: "other node", Engine: core.EngineMihomo, Content: "mixed-port: 7890\n"}
	other, err := s.SaveAgentConfig(ctx, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	// An unrelated, undecryptable body must not break this node's workspace.
	if _, err := s.pool.Exec(ctx, `UPDATE configs SET content='rf2:corrupt' WHERE id=$1`, other.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AgentConfigs(ctx, first); err != nil || len(got) != 0 {
		t.Fatalf("empty node = %+v, %v", got, err)
	}
	input.AgentID = first
	want, err := s.SaveAgentConfig(ctx, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.AgentConfigs(ctx, first); err != nil || len(got) != 1 || got[0].ID != want.ID {
		t.Fatalf("filtered node = %+v, %v", got, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET revoked_at=now() WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AgentConfigs(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked node = %v", err)
	}
	if _, err := s.AgentConfigs(ctx, "agt_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing node = %v", err)
	}
}
