package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/theplant/inject"
	"golang.org/x/sync/errgroup"
)

// DefaultCleanupTimeout is the default timeout for cleanup operations.
var DefaultCleanupTimeout = 30 * time.Second

// ErrAlreadyStarted is returned when Start is called more than once.
var ErrAlreadyStarted = errors.New("lifecycle can only be started once")

// ErrNotStarted is returned when Stop is called before Start.
var ErrNotStarted = errors.New("lifecycle must be started before stopping")

// ErrNoLongRunningServices is returned when no long-running services are registered.
var ErrNoLongRunningServices = errors.New("no long-running services to monitor")

// Actor defines the interface for simple actors that only need start/stop operations.
// Examples: configuration loaders, database migrations, one-time setup tasks.
type Actor interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Service defines the interface for long-running services that can signal completion.
// Examples: HTTP servers, gRPC servers, message queue consumers, background workers.
type Service interface {
	Actor
	Done() <-chan struct{} // Signals when the service has completed or failed
	Err() error            // Returns any error that caused the service to stop
}

// Lifecycle manages the lifecycle of multiple actor instances.
// It provides coordinated startup, monitoring, and cleanup of both simple actors and long-running services.
type Lifecycle struct {
	*inject.Injector // Embedded injector for dependency management

	actors         []Actor
	cleanupTimeout time.Duration
	started        int32 // atomic flag: 0 = not started, 1 = started
	mu             sync.RWMutex

	// Track all types for auto-resolution
	typesToResolve []reflect.Type
}

// New creates a new Lifecycle instance with embedded injector and default cleanup timeout.
func New() *Lifecycle {
	inj := inject.New()
	lc := &Lifecycle{
		Injector:       inj,
		cleanupTimeout: DefaultCleanupTimeout,
	}
	inj.Provide(func() *Lifecycle { return lc })
	return lc
}

// WithCleanupTimeout sets the timeout for cleanup operations and returns the Lifecycle instance.
// This allows for method chaining during initialization.
func (lc *Lifecycle) WithCleanupTimeout(timeout time.Duration) *Lifecycle {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.cleanupTimeout = timeout
	return lc
}

// Add registers an actor to the lifecycle manager.
// Actors are started in the order they are added and stopped in reverse order.
func (lc *Lifecycle) Add(actor Actor) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.actors = append(lc.actors, actor)
}

// Add is a helper function to add an actor to the lifecycle and return it.
func Add[A Actor](lc *Lifecycle, actor A) A {
	lc.Add(actor)
	return actor
}

// LazyAdd returns a function that when called will create an actor and add it to the lifecycle.
// This enables lazy initialization where the actor is only created when actually needed.
func LazyAdd[A Actor](lc *Lifecycle, f func() A) func() A {
	return func() A {
		return Add(lc, f())
	}
}

// AddE is a helper function to add an actor to the lifecycle and return it.
func AddE[A Actor](lc *Lifecycle, actor A, err error) (A, error) {
	if err == nil {
		lc.Add(actor)
	}
	return actor, err
}

// LazyAddE returns a function that when called will create an actor and add it to the lifecycle.
// This enables lazy initialization with error handling where the actor is only created when needed.
func LazyAddE[A Actor](lc *Lifecycle, f func() (A, error)) func() (A, error) {
	return func() (A, error) {
		actor, err := f()
		return AddE(lc, actor, err)
	}
}

// Start launches all registered actors in the order they were added.
// Returns immediately on the first error encountered, without starting remaining actors.
// Can only be called once; subsequent calls will return an error.
// The ctx parameter is only used for the startup process itself, not for long-running operations.
func (lc *Lifecycle) Start(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&lc.started, 0, 1) {
		return ErrAlreadyStarted
	}

	lc.mu.RLock()
	actors := slices.Clone(lc.actors)
	lc.mu.RUnlock()

	for _, actor := range actors {
		if err := actor.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Stop gracefully stops all registered actors in reverse order (LIFO).
// This ensures that dependencies are stopped in the correct order, mirroring their startup sequence.
// Continues stopping all actors even if some fail, and returns a joined error of all failures.
// The ctx parameter is only used for the shutdown process itself, not for long-running operations.
func (lc *Lifecycle) Stop(ctx context.Context) error {
	if atomic.LoadInt32(&lc.started) == 0 {
		return ErrNotStarted
	}

	lc.mu.RLock()
	actors := slices.Clone(lc.actors)
	lc.mu.RUnlock()

	var errs []error
	for i := len(actors) - 1; i >= 0; i-- {
		if err := actors[i].Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Wait monitors all long-running services and waits for any of them to complete.
// For each Service (actors that implement Done() and Err()), it starts a monitoring goroutine.
// Returns when:
// - Any service signals completion via Done() (returns that service's Err())
// - The provided context is cancelled (returns context error)
// - An error occurs in the monitoring process
//
// If no long-running services are registered, returns an error immediately.
// The ctx parameter controls the long-running monitoring process and can be used to cancel waiting.
func (lc *Lifecycle) Wait(ctx context.Context) error {
	// Collect all long-running services
	lc.mu.RLock()
	var services []Service
	for _, actor := range lc.actors {
		if svc, ok := actor.(Service); ok {
			services = append(services, svc)
		}
	}
	lc.mu.RUnlock()

	// Require at least one long-running service to monitor
	if len(services) == 0 {
		return ErrNoLongRunningServices
	}

	// Start monitoring goroutines for each service
	g, gCtx := errgroup.WithContext(ctx)
	for _, svc := range services {
		svc := svc // capture loop variable
		g.Go(func() error {
			select {
			case <-svc.Done():
				// Service completed, return its error (may be nil)
				return svc.Err()
			case <-gCtx.Done():
				// Context cancelled or another service completed
				return gCtx.Err()
			}
		})
	}

	// Wait for any service to complete or context cancellation
	err := g.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// Serve provides a complete lifecycle management solution.
// It automatically resolves all provided types, starts all actors, monitors long-running services, and ensures proper cleanup.
//
// Process:
// 1. Automatically resolves all provided types with context support
// 2. Starts all registered actors via Start()
// 3. Sets up automatic cleanup via defer (using background context with timeout)
// 4. Monitors long-running services via Wait()
// 5. Returns when any service completes or context is cancelled
//
// The cleanup is guaranteed to run even if Wait() returns an error,
// using a separate background context to avoid cancellation issues.
// The ctx parameter controls the long-running monitoring process and can be used to cancel the entire operation.
func (lc *Lifecycle) Serve(ctx context.Context) error {
	// Automatically resolve all provided types with context
	if err := lc.ResolveAll(ctx); err != nil {
		return err
	}

	// Start all actors
	if err := lc.Start(ctx); err != nil {
		return err
	}

	// Ensure cleanup on exit using background context to avoid cancellation
	defer func() {
		lc.mu.RLock()
		timeout := lc.cleanupTimeout
		lc.mu.RUnlock()

		cleanupCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		lc.Stop(cleanupCtx)
	}()

	// Monitor long-running services
	return lc.Wait(ctx)
}

var typeError = reflect.TypeOf((*error)(nil)).Elem()

// Provide registers constructors and tracks all non-error return types for auto-resolution.
// This overrides the embedded Injector's Provide method to enable auto-resolution of types.
func (lc *Lifecycle) Provide(ctors ...any) error {
	ctors = inject.Flatten(ctors...)

	// First, register with the embedded injector
	if err := lc.Injector.Provide(ctors...); err != nil {
		return err
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()

	// Track all non-error return types
	for _, ctor := range ctors {
		ctorType := reflect.TypeOf(ctor)

		// Check return types
		for i := 0; i < ctorType.NumOut(); i++ {
			returnType := ctorType.Out(i)

			// Skip error type
			if returnType == typeError {
				continue
			}

			// Store all non-error types for later resolution
			lc.typesToResolve = append(lc.typesToResolve, returnType)
		}
	}

	return nil
}

// ResolveAll automatically resolves all provided types using a clean child injector.
func (lc *Lifecycle) ResolveAll(ctx context.Context) error {
	lc.mu.Lock()
	typesToResolve := slices.Clone(lc.typesToResolve)
	lc.mu.Unlock()

	slices.Reverse(typesToResolve)

	for _, ty := range typesToResolve {
		// Create a pointer to the type for resolution
		ptr := reflect.New(ty)

		// Resolve the type using the child injector
		if err := lc.ResolveWithContext(ctx, ptr.Interface()); err != nil {
			return err
		}
	}

	return nil
}
