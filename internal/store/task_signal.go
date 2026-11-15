package store

import (
	"context"
	"sync"
)

type agentSignalHub struct {
	mu    sync.Mutex
	wakes map[string]chan struct{}
}

func (h *agentSignalHub) channel(agentID string) chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wakes == nil {
		h.wakes = make(map[string]chan struct{})
	}
	wake := h.wakes[agentID]
	if wake == nil {
		wake = make(chan struct{}, 1)
		h.wakes[agentID] = wake
	}
	return wake
}

func (h *agentSignalHub) signal(agentID string) {
	if agentID == "" {
		return
	}
	select {
	case h.channel(agentID) <- struct{}{}:
	default:
	}
}

func signalAgentAfterCommit(ctx context.Context, agentID string, hub *agentSignalHub) {
	if agentID == "" {
		return
	}
	runAfterCommit(ctx, func() { hub.signal(agentID) })
}

// TaskReady returns a coalescing signal for newly created tasks assigned to an agent.
func (s *Store) TaskReady(agentID string) <-chan struct{} {
	return s.taskSignals.channel(agentID)
}

func (s *Store) signalTaskReady(agentID string) {
	s.taskSignals.signal(agentID)
}

func (s *Store) signalTaskReadyAfterCommit(ctx context.Context, agentID string) {
	signalAgentAfterCommit(ctx, agentID, &s.taskSignals)
}

// DiagnosticReady returns a coalescing signal for diagnostic session transitions involving an agent.
func (s *Store) DiagnosticReady(agentID string) <-chan struct{} {
	return s.diagSignals.channel(agentID)
}

func (s *Store) signalDiagnosticReadyAfterCommit(ctx context.Context, agentID string) {
	signalAgentAfterCommit(ctx, agentID, &s.diagSignals)
}

// CertificateChallengeReady returns a coalescing signal for HTTP-01 challenge changes on an agent.
func (s *Store) CertificateChallengeReady(agentID string) <-chan struct{} {
	return s.certSignals.channel(agentID)
}

func (s *Store) signalChallengeReady(agentID string) {
	s.certSignals.signal(agentID)
}

func (s *Store) signalChallengeReadyAfterCommit(ctx context.Context, agentID string) {
	signalAgentAfterCommit(ctx, agentID, &s.certSignals)
}
