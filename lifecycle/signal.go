package lifecycle

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/pkg/errors"
)

var DefaultSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}

var _ Service = (*SignalService)(nil)

// SignalService is a specialized service for handling OS signals.
// This provides a distinct type for dependency injection while reusing FuncService functionality.
type SignalService struct {
	*FuncService
}

// NewSignalService creates a new SignalService that listens for SIGINT and SIGTERM.
func NewSignalService(signals ...os.Signal) *SignalService {
	if len(signals) == 0 {
		signals = DefaultSignals
	}

	funcSvc := NewFuncService(
		func(ctx context.Context) error {
			// Wait for signals using signal.NotifyContext
			signalCtx, cancel := signal.NotifyContext(ctx, signals...)
			defer cancel()

			// Block until signal is received or context is cancelled
			<-signalCtx.Done()

			// Return nil for graceful shutdown (signal received)
			// Return context error for cancellation
			return errors.WithStack(signalCtx.Err())
		},
	).WithName("signal")

	// Wrap FuncService in SignalService
	return &SignalService{FuncService: funcSvc}
}

// WithStop is not supported for SignalService.
func (s *SignalService) WithStop(stop func(ctx context.Context) error) *SignalService {
	panic("this method is not supported for SignalService")
}

// WithName sets the name for the signal service.
func (s *SignalService) WithName(name string) *SignalService {
	s.FuncService.WithName(name)
	return s
}

// SetupSignal creates and registers a signal handling service that listens for SIGINT and SIGTERM.
// Returns a SignalService that will complete when a signal is received.
func SetupSignal(lc *Lifecycle) *SignalService {
	svc := NewSignalService()
	lc.Add(svc)
	return svc
}

// SetupSignalWith returns a setup function that creates and registers a signal handling service
// that listens for the specified signals. If no signals are provided, it defaults to SIGINT and SIGTERM.
// The returned function can be used with dependency injection or called directly with a Lifecycle instance.
func SetupSignalWith(signals ...os.Signal) func(lc *Lifecycle) *SignalService {
	return func(lc *Lifecycle) *SignalService {
		svc := NewSignalService(signals...)
		lc.Add(svc)
		return svc
	}
}
