package api

import (
	"context"
	"sync/atomic"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) registerConnection(agentID, connectionID string, cancel context.CancelFunc) chan struct{} {
	trafficRefresh := make(chan struct{}, 1)
	timeZoneRefresh := make(chan struct{}, 1)
	policyRefresh := make(chan struct{}, 1)
	s.connectionsMu.Lock()
	previous, exists := s.connections[agentID]
	s.connections[agentID] = liveConnection{
		id:              connectionID,
		cancel:          cancel,
		trafficRefresh:  trafficRefresh,
		timeZoneRefresh: timeZoneRefresh,
		policyRefresh:   policyRefresh,
		managedPolicy:   &atomic.Bool{},
	}
	s.connectionsMu.Unlock()
	if exists {
		previous.cancel()
	}
	return trafficRefresh
}

func (s *Server) connectionChannel(agentID, connectionID string, pick func(liveConnection) chan struct{}) chan struct{} {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if connection, ok := s.connections[agentID]; ok && connection.id == connectionID {
		return pick(connection)
	}
	return nil
}

func (s *Server) agentTimeZoneRefreshChannel(agentID, connectionID string) chan struct{} {
	return s.connectionChannel(agentID, connectionID, func(c liveConnection) chan struct{} { return c.timeZoneRefresh })
}

func (s *Server) agentPolicyRefreshChannel(agentID, connectionID string) chan struct{} {
	return s.connectionChannel(agentID, connectionID, func(c liveConnection) chan struct{} { return c.policyRefresh })
}

func (s *Server) setConnectionManagedPolicy(agentID, connectionID string, managed bool) {
	s.connectionsMu.Lock()
	connection, ok := s.connections[agentID]
	s.connectionsMu.Unlock()
	if ok && connection.id == connectionID && connection.managedPolicy != nil {
		connection.managedPolicy.Store(managed)
	}
}

func signalConnectionChannel(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *Server) signalLiveConnection(agentID string, pick func(liveConnection) chan struct{}) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if connection, ok := s.connections[agentID]; ok {
		signalConnectionChannel(pick(connection))
	}
}

func (s *Server) refreshAgentTimeZone(agentID string) {
	s.signalLiveConnection(agentID, func(c liveConnection) chan struct{} { return c.timeZoneRefresh })
}

// Refresh policies in-place: reconnect discovery would also add unselected ports.
func (s *Server) refreshAgentTrafficPolicies(agentID string) {
	s.signalLiveConnection(agentID, func(c liveConnection) chan struct{} { return c.trafficRefresh })
}

// RefreshAgentPolicy pushes updated Agent policy in-place for managed-policy
// Agents, falling back to disconnect/reconnect only for legacy Agents without
// dynamic policy hot-reload support.
func (s *Server) RefreshAgentPolicy(agentID string) {
	s.connectionsMu.Lock()
	connection, exists := s.connections[agentID]
	if exists && connection.managedPolicy != nil && connection.managedPolicy.Load() {
		signalConnectionChannel(connection.policyRefresh)
		s.connectionsMu.Unlock()
		return
	}
	if exists {
		delete(s.connections, agentID)
	}
	s.connectionsMu.Unlock()
	if exists {
		connection.cancel()
	}
}

func (s *Server) unregisterConnection(agentID, connectionID string) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	current, exists := s.connections[agentID]
	if exists && current.id == connectionID {
		delete(s.connections, agentID)
	}
}

// DisconnectAgent terminates the currently authenticated WSS session after
// the corresponding identity has been revoked. New handshakes are rejected by
// the store-backed authentication middleware.
func (s *Server) DisconnectAgent(agentID string) {
	s.connectionsMu.Lock()
	connection, exists := s.connections[agentID]
	if exists {
		delete(s.connections, agentID)
	}
	s.connectionsMu.Unlock()
	if exists {
		connection.cancel()
	}
}

// DisconnectAllAgents makes active sessions reconnect and receive a freshly
// saved managed policy. It does not restart Agent processes or managed cores.
func (s *Server) DisconnectAllAgents() {
	s.connectionsMu.Lock()
	connections := make([]liveConnection, 0, len(s.connections))
	for id, connection := range s.connections {
		connections = append(connections, connection)
		delete(s.connections, id)
	}
	s.connectionsMu.Unlock()
	for _, connection := range connections {
		connection.cancel()
	}
}

// Notify Agents only after a Key mutation commits its authorization and data.
func afterRequestCommit(ctx context.Context, callback func()) {
	if !store.AfterRequestCommit(ctx, callback) {
		callback()
	}
}
