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

func TestDiagnosticAndCertificateSignalsAreAgentScopedAndCoalescing(t *testing.T) {
	t.Parallel()
	dataStore := &Store{}

	diagFirst := dataStore.DiagnosticReady("agt_first")
	diagSecond := dataStore.DiagnosticReady("agt_second")
	dataStore.signalDiagnosticReadyAfterCommit(t.Context(), "agt_first")
	dataStore.signalDiagnosticReadyAfterCommit(t.Context(), "agt_first")
	select {
	case <-diagFirst:
	default:
		t.Fatal("first agent did not receive diagnostic-ready signal")
	}
	select {
	case <-diagFirst:
		t.Fatal("duplicate diagnostic-ready signals were not coalesced")
	default:
	}
	select {
	case <-diagSecond:
		t.Fatal("diagnostic-ready signal leaked to another agent")
	default:
	}

	certFirst := dataStore.CertificateChallengeReady("agt_first")
	certSecond := dataStore.CertificateChallengeReady("agt_second")
	dataStore.signalChallengeReadyAfterCommit(t.Context(), "agt_first")
	dataStore.signalChallengeReadyAfterCommit(t.Context(), "agt_first")
	select {
	case <-certFirst:
	default:
		t.Fatal("first agent did not receive challenge-ready signal")
	}
	select {
	case <-certFirst:
		t.Fatal("duplicate challenge-ready signals were not coalesced")
	default:
	}
	select {
	case <-certSecond:
		t.Fatal("challenge-ready signal leaked to another agent")
	default:
	}
}
