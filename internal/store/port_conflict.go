package store

// PortConflictError identifies a correctable port/allocation constraint. It
// must not be presented as a stale configuration revision requiring reload.
type PortConflictError struct {
	Port  int
	Field string
	Cause error
}

func (e *PortConflictError) Error() string { return e.Cause.Error() }
func (e *PortConflictError) Unwrap() error { return e.Cause }

func portConflict(port int, cause error) error {
	return &PortConflictError{Port: port, Field: "port", Cause: cause}
}
