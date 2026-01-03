package lifecycle

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/pkg/errors"
)

var (
	_ Actor                  = (*FuncActor)(nil)
	_ RequiresReadinessProbe = (*FuncActor)(nil)
)

// FuncActor wraps start and stop functions into an Actor implementation.
// This is useful for creating actors from simple functions without defining new types.
type FuncActor struct {
	startFunc      func(ctx context.Context) error
	stopFunc       func(ctx context.Context) error
	name           string
	stage          int
	stageSet       bool
	readinessProbe *ReadinessProbe
}

// NewFuncActor creates a new FuncActor with the provided start and stop functions.
func NewFuncActor(start, stop func(ctx context.Context) error) *FuncActor {
	return &FuncActor{
		startFunc: start,
		stopFunc:  stop,
		stage:     StageDefault,
		stageSet:  false,
	}
}

// WithName sets the name of the actor.
// If a readiness probe is enabled, its name is also updated.
func (f *FuncActor) WithName(name string) *FuncActor {
	f.name = name
	if f.readinessProbe != nil {
		f.readinessProbe.WithName(name)
	}
	return f
}

// GetName returns the name of the actor.
func (f *FuncActor) GetName() string {
	return f.name
}

// Start calls the wrapped start function.
// If readiness probe is enabled via WithReadiness, it signals completion after start.
func (f *FuncActor) Start(ctx context.Context) (xerr error) {
	defer func() {
		if f.readinessProbe != nil {
			f.readinessProbe.Signal(xerr)
		}
	}()

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

// WithReadiness enables a readiness probe for this actor.
// Note: When enabled, this actor is assigned a very high stage value (for example,
// close to math.MaxInt) so that it starts after most other actors. This is typically
// desirable because readiness/health endpoints depend on the rest of the system
// being up, not the other way around. If another actor must start after this one,
// give that actor a higher stage (but still lower than the readiness-probe stage)
// by using WithStage to override its default behavior. Returns the actor for method
// chaining.
func (f *FuncActor) WithReadiness() *FuncActor {
	if f.readinessProbe == nil {
		f.readinessProbe = NewReadinessProbe().WithName(f.name)
	}
	return f
}

// RequiresReadinessProbe returns the readiness probe if enabled via WithReadiness.
func (f *FuncActor) RequiresReadinessProbe() *ReadinessProbe {
	return f.readinessProbe
}

// WithStage sets the stage for the actor, overriding the default behavior.
// This affects the order of actor startup: lower stages start first.
func (f *FuncActor) WithStage(stage int) *FuncActor {
	f.stage = stage
	f.stageSet = true
	return f
}

// GetStage returns the stage of the actor.
func (f *FuncActor) GetStage() int {
	if f.stageSet {
		return f.stage
	}
	if f.readinessProbe == nil {
		return StageDefault
	}
	return StageReadiness
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
