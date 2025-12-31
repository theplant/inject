package lifecycle

import "sync"

// ReadinessProbe is a simple readiness probe that can be used to signal when the application is ready to serve traffic.
type ReadinessProbe struct {
	once   sync.Once
	readyC chan struct{}
}

// NewReadinessProbe creates a new readiness probe.
func NewReadinessProbe() *ReadinessProbe {
	return &ReadinessProbe{readyC: make(chan struct{})}
}

// SignalReady signals that the application is ready to serve traffic.
func (rp *ReadinessProbe) SignalReady() {
	rp.once.Do(func() {
		close(rp.readyC)
	})
}
