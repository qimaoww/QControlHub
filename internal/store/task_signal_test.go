package store

import "testing"

func TestTaskReadySignalIsAgentScopedAndCoalescing(t *testing.T) {
	t.Parallel()
	dataStore := &Store{}
	first := dataStore.TaskReady("agt_first")
	second := dataStore.TaskReady("agt_second")
	dataStore.signalTaskReady("agt_first")
	dataStore.signalTaskReady("agt_first")
	select {
	case <-first:
	default:
		t.Fatal("first agent did not receive task-ready signal")
	}
	select {
	case <-first:
		t.Fatal("duplicate task-ready signals were not coalesced")
	default:
	}
	select {
	case <-second:
		t.Fatal("task-ready signal leaked to another agent")
	default:
	}
}
