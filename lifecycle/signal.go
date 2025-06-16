package lifecycle

import (
	"context"
	"os/signal"
	"syscall"
)

// SignalService implements Service interface to handle OS signals for graceful shutdown.
// It listens for SIGINT and SIGTERM signals and signals completion when received.
type SignalService struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// NewSignalService creates a new SignalService that listens for SIGINT and SIGTERM.
func NewSignalService() *SignalService {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	return &SignalService{
		ctx:    ctx,
		cancel: cancel,
	}
}

func SetupSignalService(lc *Lifecycle) *SignalService {
	return Add(lc, NewSignalService())
}

// Start is a no-op since signal listening begins at construction.
func (s *SignalService) Start(_ context.Context) error {
	return nil
}

// Stop stops the signal listener.
func (s *SignalService) Stop(_ context.Context) error {
	s.cancel()
	return nil
}

// Done returns a channel that is closed when a signal is received.
func (s *SignalService) Done() <-chan struct{} {
	return s.ctx.Done()
}

// Err returns any error that occurred. For signal-based shutdown, this is typically nil.
func (s *SignalService) Err() error {
	return s.ctx.Err()
}
