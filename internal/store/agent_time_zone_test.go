package store

import (
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAgentTimeZoneIsAgentScopedVersionedAndOwnerManaged(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	admin := WithConfigScope(ctx, "", true)
	first := sharedTestAgent(t, db, ctx)
	second, _ := enrollTaskTestAgent(t, ctx, db)
	firstPending, changed, err := db.SetAgentTimeZone(admin, first.ID, "custom", "UTC", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("initial time zone change was not recorded")
	}
	secondPending, changed, err := db.SetAgentTimeZone(admin, first.ID, "custom", "Asia/Hong_Kong", firstPending.Revision, false)
	if err != nil || secondPending.Revision <= firstPending.Revision {
		t.Fatalf("second revision = %+v, %v", secondPending, err)
	}
	if !changed {
		t.Fatal("second time zone change was not recorded")
	}
	if accepted, err := db.AcknowledgeAgentTimeZone(ctx, first.ID, core.AgentTimeZoneResult{Revision: firstPending.Revision, TimeZone: "UTC"}); err != nil || accepted {
		t.Fatalf("stale acknowledgement accepted=%v error=%v", accepted, err)
	}
	if accepted, err := db.AcknowledgeAgentTimeZone(ctx, first.ID, core.AgentTimeZoneResult{Revision: secondPending.Revision, TimeZone: "Asia/Hong_Kong"}); err != nil || !accepted {
		t.Fatalf("current acknowledgement accepted=%v error=%v", accepted, err)
	}
	if accepted, err := db.AcknowledgeAgentTimeZone(ctx, first.ID, core.AgentTimeZoneResult{Revision: secondPending.Revision, TimeZone: "Asia/Hong_Kong", Error: "late transport error"}); err != nil || accepted {
		t.Fatalf("late failure changed an already applied revision: accepted=%v error=%v", accepted, err)
	}
	firstState, err := db.GetAgentTimeZone(ctx, first.ID)
	if err != nil || firstState.Mode != "custom" || firstState.TimeZone != "Asia/Hong_Kong" || firstState.AppliedTimeZone != "Asia/Hong_Kong" || firstState.Status != "applied" {
		t.Fatalf("first Agent state = %+v, %v", firstState, err)
	}
	secondState, err := db.GetAgentTimeZone(ctx, second.ID)
	if err != nil || secondState.Mode != "auto" || secondState.TimeZone != "" || secondState.Status != "pending" {
		t.Fatalf("other Agent state changed = %+v, %v", secondState, err)
	}
	if _, _, err := db.SetAgentTimeZone(admin, first.ID, "custom", "Invalid/Zone", secondPending.Revision, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid stored time zone error = %v", err)
	}
	recipient, recipientContext := sharedTestUser(t, db, ctx, "timezone-recipient")
	sharedTestAllocation(t, db, ctx, recipient.ID, first.ID, 1024, 21001)
	clockSync := &core.ClockSyncStatus{Status: "synchronized"}
	if err := db.UpdateAgentMetrics(admin, first.ID, core.HostMetrics{CPUAvailable: true, ClockSync: clockSync}); err != nil {
		t.Fatal(err)
	}
	sharedView, err := db.GetAgent(recipientContext, first.ID)
	if err != nil || sharedView.CanManage || sharedView.TimeZone.TimeZone != "" || sharedView.Metrics.ClockSync != nil {
		t.Fatalf("shared node exposed host time-zone or clock-sync details: agent=%+v error=%v", sharedView, err)
	}
	if _, _, err := db.SetAgentTimeZone(recipientContext, first.ID, "custom", "Asia/Tokyo", secondPending.Revision, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("shared recipient changed host time zone: %v", err)
	}
}

func TestAgentTimeZoneLookupFailureRetainsAppliedSetting(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	admin := WithConfigScope(ctx, "", true)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	state, _, err := db.SetAgentTimeZone(admin, agent.ID, "custom", "Asia/Hong_Kong", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if accepted, err := db.AcknowledgeAgentTimeZone(ctx, agent.ID, core.AgentTimeZoneResult{Revision: state.Revision, TimeZone: "Asia/Hong_Kong"}); err != nil || !accepted {
		t.Fatalf("initial acknowledgement accepted=%v error=%v", accepted, err)
	}
	state, _, err = db.SetAgentTimeZone(admin, agent.ID, "auto", "", state.Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	state, err = db.UpdateAgentTimeZoneResolution(ctx, agent.ID, state.Revision, state.AutoAddressHash, "", "new-address-hash", errors.New("provider unavailable"))
	if err != nil || state.Status != "failed" || state.TimeZone != "Asia/Hong_Kong" || state.AppliedTimeZone != "Asia/Hong_Kong" || state.Error != core.AgentTimeZoneLookupFailure {
		t.Fatalf("lookup failure did not retain the effective setting: state=%+v error=%v", state, err)
	}
}
