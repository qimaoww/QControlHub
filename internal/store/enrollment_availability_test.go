package store

import (
	"context"
	"testing"
)

func TestBatchedEnrollmentAvailabilityVerifiesSecrets(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agentID, _ := seedPerformanceAgent(t, s, 0)
	if _, err := s.CreateAgentEnrollmentToken(ctx, agentID); err != nil {
		t.Fatal(err)
	}
	assertAvailable := func(reader *Store, want bool) {
		t.Helper()
		agents, err := reader.ListAgentsWithEnrollmentCommands(ctx)
		if err != nil || len(agents) != 1 || agents[0].EnrollmentCommandAvailable != want {
			t.Fatalf("availability=%+v, %v; want %v", agents, err, want)
		}
	}
	assertAvailable(s, true)
	assertAvailable(&Store{pool: s.pool}, false)
	if _, err := s.pool.Exec(ctx, `UPDATE enrollment_tokens SET token_ciphertext='rf2:corrupt' WHERE agent_id=$1`, agentID); err != nil {
		t.Fatal(err)
	}
	assertAvailable(s, false)
}
