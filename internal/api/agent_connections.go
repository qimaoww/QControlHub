package api

import (
	"context"
)

func (s *Server) registerConnection(agentID, connectionID string, cancel context.CancelFunc) chan struct{} {
	trafficRefresh := make(chan struct{}, 1)
	s.connectionsMu.Lock()
	previous, exists := s.connections[agentID]
	s.connections[agentID] = liveConnection{id: connectionID, cancel: cancel, trafficRefresh: trafficRefresh}
	s.connectionsMu.Unlock()
	if exists {
		previous.cancel()
	}
	return trafficRefresh
}

// Refresh policies in-place: reconnect discovery would also add unselected ports.
func (s *Server) refreshAgentTrafficPolicies(agentID string) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if connection, ok := s.connections[agentID]; ok {
		select {
		case connection.trafficRefresh <- struct{}{}:
		default:
		}
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
