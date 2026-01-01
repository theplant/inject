package lifecycle

import (
	"sync"
	"sync/atomic"
)

// RequiresReadinessProbe is an interface for types that provide a readiness probe.
// Actors implementing this interface will have their probes collected and
// waited on during lifecycle startup.
type RequiresReadinessProbe interface {
	RequiresReadinessProbe() *ReadinessProbe
}

// ReadinessProbe is a simple readiness probe that can be used to signal when the application is ready to serve traffic.
type ReadinessProbe struct {
	once  sync.Once
	doneC chan struct{}       // Unified completion signal
	err   atomic.Pointer[error] // Error if failed, nil if successful
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
		if err != nil {
			rp.err.Store(&err)
		}
		close(rp.doneC)
	})
}

// Error returns the error that caused the readiness probe to fail, or nil if successful.
func (rp *ReadinessProbe) Error() error {
	if errPtr := rp.err.Load(); errPtr != nil {
		return *errPtr
	}
	return nil
}
