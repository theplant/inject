package lifecycle

import "sync"

// ReadinessProbe is a simple readiness probe that can be used to signal when the application is ready to serve traffic.
type ReadinessProbe struct {
	once  sync.Once
	doneC chan struct{} // Unified completion signal
	err   error         // Error if failed, nil if successful
}

// NewReadinessProbe creates a new readiness probe.
func NewReadinessProbe() *ReadinessProbe {
	return &ReadinessProbe{
		doneC: make(chan struct{}),
	}
}

// Signal signals the completion of readiness check.
// Pass nil error to indicate success, or non-nil error to indicate failure.
func (rp *ReadinessProbe) Signal(err error) {
	rp.once.Do(func() {
		rp.err = err
		close(rp.doneC)
	})
}

// Error returns the error that caused the readiness probe to fail, or nil if successful.
func (rp *ReadinessProbe) Error() error {
	return rp.err
}
