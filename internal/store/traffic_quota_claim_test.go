package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestQuotaClaimsAreAtomicAcrossMonitors(t *testing.T) {
	s := openPerformanceStore(t)
	_, usages := seedPerformanceAgent(t, s, 3)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `UPDATE port_traffic_policies SET blocked=true`); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	var count atomic.Int64
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			got, err := s.ClaimTrafficQuotaTransitions(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			for _, item := range got {
				if !item.Policy.Blocked || item.Policy.ResetGeneration != 1 {
					t.Errorf("invalid quota claim: %+v", item)
				}
			}
			count.Add(int64(len(got)))
		}()
	}
	group.Wait()
	if count.Load() != 3 {
		t.Fatalf("quota claims=%d, want 3", count.Load())
	}
	if _, err := s.pool.Exec(ctx, `UPDATE port_traffic_policies SET reset_generation=reset_generation+1 WHERE id=$1`, usages[0].PolicyID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ClaimTrafficQuotaTransitions(ctx)
	if err != nil || len(got) != 1 || got[0].Policy.ResetGeneration != 2 {
		t.Fatalf("next generation: %+v, %v", got, err)
	}
}
