package lifecycle

import "sync"

// RequiresReadinessProbe is an interface for types that provide a readiness probe.
// Actors implementing this interface will have their probes collected and
// waited on during lifecycle startup.
type RequiresReadinessProbe interface {
	RequiresReadinessProbe() *ReadinessProbe
}

// ReadinessProbe is a simple readiness probe that can be used to signal when the application is ready to serve traffic.
type ReadinessProbe struct {
	once  sync.Once
	doneC chan struct{} // Completion signal channel
	mu    sync.RWMutex  // Protects err field
	err   error         // Error if failed, nil if successful
	name  string        // Name for logging and debugging
}

// NewReadinessProbe creates a new readiness probe.
func NewReadinessProbe() *ReadinessProbe {
	return &ReadinessProbe{
		doneC: make(chan struct{}),
	}
}

// Done returns a channel that is closed when the readiness probe is signaled.
func (rp *ReadinessProbe) Done() <-chan struct{} {
	return rp.doneC
}

// Signal signals the completion of readiness check.
// Pass nil error to indicate success, or non-nil error to indicate failure.
func (rp *ReadinessProbe) Signal(err error) {
	rp.once.Do(func() {
		rp.mu.Lock()
		rp.err = err
		rp.mu.Unlock()
		close(rp.doneC)
	})
}

// Error returns the error that caused the readiness probe to fail, or nil if successful.
func (rp *ReadinessProbe) Error() error {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	return rp.err
}

// WithName sets the name of the readiness probe for logging and debugging.
func (rp *ReadinessProbe) WithName(name string) *ReadinessProbe {
	rp.name = name
	return rp
}

// GetName returns the name of the readiness probe.
func (rp *ReadinessProbe) GetName() string {
	return rp.name
}
