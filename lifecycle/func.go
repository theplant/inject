package lifecycle

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/pkg/errors"
)

var _ Actor = (*FuncActor)(nil)

// FuncActor wraps start and stop functions into an Actor implementation.
// This is useful for creating actors from simple functions without defining new types.
type FuncActor struct {
	startFunc func(ctx context.Context) error
	stopFunc  func(ctx context.Context) error
	name      string
}

// NewFuncActor creates a new FuncActor with the provided start and stop functions.
func NewFuncActor(start, stop func(ctx context.Context) error) *FuncActor {
	return &FuncActor{
		startFunc: start,
		stopFunc:  stop,
	}
}

// WithName sets the name of the actor.
func (f *FuncActor) WithName(name string) *FuncActor {
	f.name = name
	return f
}

// GetName returns the name of the actor.
func (f *FuncActor) GetName() string {
	return f.name
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

// acquired resources during construction and needs cleanup regardless of whether Start is called.
func (f *FuncActor) RequiresStop() bool {
	return f.startFunc == nil && f.stopFunc != nil
}

var _ Service = (*FuncService)(nil)

// FuncService runs a function in a background goroutine and implements Service interface.
// Useful for long-running background tasks that need to be monitored.
type FuncService struct {
	taskFunc  func(ctx context.Context) error
	stopFunc  func(ctx context.Context) error
	name      string
	ctx       context.Context
	cancel    context.CancelCauseFunc
	doneC     chan struct{}
	startOnce sync.Once
	started   atomic.Bool
}

// NewFuncService creates a new FuncService with the provided task function.
func NewFuncService(taskFunc func(ctx context.Context) error) *FuncService {
	if taskFunc == nil {
		panic("taskFunc is required")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	return &FuncService{
		taskFunc: taskFunc,
		stopFunc: nil,
		ctx:      ctx,
		cancel:   cancel,
		doneC:    make(chan struct{}),
	}
}

// WithStop sets the stop function for the service.
func (b *FuncService) WithStop(stop func(ctx context.Context) error) *FuncService {
	b.stopFunc = stop
	return b
}

// WithName sets the name of the service.
func (b *FuncService) WithName(name string) *FuncService {
	b.name = name
	return b
}

// GetName returns the name of the service.
func (b *FuncService) GetName() string {
	return b.name
}

// Start launches the background task in a separate goroutine.
func (b *FuncService) Start(_ context.Context) error {
	b.startOnce.Do(func() {
		b.started.Store(true)

		go func() (xerr error) {
			defer close(b.doneC)
			defer func() { b.cancel(xerr) }()
			return b.taskFunc(b.ctx)
		}() // nolint:errcheck
	})
	return nil
}

// Stop cancels the background task and waits for completion.
func (b *FuncService) Stop(ctx context.Context) error {
	b.cancel(nil)

	if b.stopFunc != nil {
		if err := b.stopFunc(ctx); err != nil {
			return err
		}
	}

	// Wait for the background task to complete or context timeout
	select {
	case <-b.doneC:
		return nil
	case <-ctx.Done():
		return errors.WithStack(ctx.Err())
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

// IsStarted returns whether the service has been started
func (b *FuncService) IsStarted() bool {
	return b.started.Load()
}
