package lifecycle

import (
	"context"
	"errors"
)

// FuncActor wraps start and stop functions into an Actor implementation.
// This is useful for creating actors from simple functions without defining new types.
type FuncActor struct {
	startFunc func(ctx context.Context) error
	stopFunc  func(ctx context.Context) error
}

// NewFuncActor creates a new FuncActor with the provided start and stop functions.
func NewFuncActor(start, stop func(ctx context.Context) error) *FuncActor {
	return &FuncActor{
		startFunc: start,
		stopFunc:  stop,
	}
}

// Start calls the wrapped start function.
func (f *FuncActor) Start(ctx context.Context) error {
	if f.startFunc != nil {
		return f.startFunc(ctx)
	}
	return nil
}

// Stop calls the wrapped stop function.
func (f *FuncActor) Stop(ctx context.Context) error {
	if f.stopFunc != nil {
		return f.stopFunc(ctx)
	}
	return nil
}

// FuncService runs a function in a background goroutine and implements Service interface.
// Useful for long-running background tasks that need to be monitored.
type FuncService struct {
	taskFunc func(ctx context.Context) error
	ctx      context.Context
	cancel   context.CancelCauseFunc
	doneC    chan struct{}
}

// NewFuncService creates a new FuncService with the provided task function.
func NewFuncService(taskFunc func(ctx context.Context) error) *FuncService {
	if taskFunc == nil {
		panic("taskFunc is required")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	return &FuncService{
		taskFunc: taskFunc,
		ctx:      ctx,
		cancel:   cancel,
		doneC:    make(chan struct{}),
	}
}

// Start launches the background task in a separate goroutine.
func (b *FuncService) Start(ctx context.Context) error {
	go func() (xerr error) {
		defer close(b.doneC)
		defer func() { b.cancel(xerr) }()
		return b.taskFunc(ctx)
	}()

	return nil
}

// Stop cancels the background task and waits for completion.
func (b *FuncService) Stop(ctx context.Context) error {
	b.cancel(nil)

	// Wait for the background task to complete or context timeout
	select {
	case <-b.doneC:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Done returns a channel that is closed when the background task completes.
func (b *FuncService) Done() <-chan struct{} {
	return b.doneC
}

// Err returns any error that occurred during background task execution.
func (b *FuncService) Err() error {
	err := context.Cause(b.ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
