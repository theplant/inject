package lifecycle

import (
	"context"
	"os/signal"
	"syscall"
)

// SignalService is a specialized service for handling OS signals.
// This provides a distinct type for dependency injection while reusing FuncService functionality.
type SignalService struct {
	*FuncService
}

// NewSignalService creates a new SignalService that listens for SIGINT and SIGTERM.
func NewSignalService() *SignalService {
	funcSvc := NewFuncService(
		func(ctx context.Context) error {
			// Wait for signals using signal.NotifyContext
			signalCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			// Block until signal is received or context is cancelled
			<-signalCtx.Done()

			// Return nil for graceful shutdown (signal received)
			// Return context error for cancellation
			return ctx.Err()
		},
	).WithName("signal")

	// Wrap FuncService in SignalService
	return &SignalService{FuncService: funcSvc}
}

// SetupSignal creates and registers a signal handling service that listens for SIGINT and SIGTERM.
// Returns a SignalService that will complete when a signal is received.
func SetupSignal(lc *Lifecycle) *SignalService {
	return Add(lc, NewSignalService())
}
