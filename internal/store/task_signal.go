package store

// TaskReady returns a coalescing signal for newly created tasks assigned to an agent.
func (s *Store) TaskReady(agentID string) <-chan struct{} {
	return s.taskReadyChannel(agentID)
}

func (s *Store) taskReadyChannel(agentID string) chan struct{} {
	s.taskWakeMu.Lock()
	defer s.taskWakeMu.Unlock()
	if s.taskWakes == nil {
		s.taskWakes = make(map[string]chan struct{})
	}
	wake := s.taskWakes[agentID]
	if wake == nil {
		wake = make(chan struct{}, 1)
		s.taskWakes[agentID] = wake
	}
	return wake
}

func (s *Store) signalTaskReady(agentID string) {
	wake := s.taskReadyChannel(agentID)
	select {
	case wake <- struct{}{}:
	default:
	}
}
