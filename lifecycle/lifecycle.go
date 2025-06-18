package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/theplant/inject"
	"golang.org/x/sync/errgroup"
)

// DefaultStopTimeout is the default timeout for stop operations.
var DefaultStopTimeout = 30 * time.Second

// ErrServed is returned when Serve is called.
var ErrServed = errors.New("lifecycle already served")

// ErrNoServices is returned when no long-running services are registered.
var ErrNoServices = errors.New("no long-running services to serve")

type ctxKeyStopCause struct{}

func withStopCause(ctx context.Context, cause error) context.Context {
	return context.WithValue(ctx, ctxKeyStopCause{}, cause)
}

func GetStopCause(ctx context.Context) error {
	cause, _ := ctx.Value(ctxKeyStopCause{}).(error)
	return cause
}

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

// Named defines an optional interface for actors that can provide a human-readable name.
// This is useful for logging and debugging purposes.
type Named interface {
	GetName() string
}

// Lifecycle manages the lifecycle of multiple actor instances.
// It provides coordinated startup, monitoring, and cleanup of both simple actors and long-running services.
type Lifecycle struct {
	*inject.Injector // Embedded injector for dependency management

	actors      []Actor
	stopTimeout time.Duration
	served      atomic.Bool
	mu          sync.RWMutex
	logger      *slog.Logger
}

// New creates a new Lifecycle instance with embedded injector and default stop timeout.
func New() *Lifecycle {
	inj := inject.New()
	lc := &Lifecycle{
		Injector:    inj,
		stopTimeout: DefaultStopTimeout,
		logger:      slog.Default(),
	}
	_ = inj.Provide(func() *Lifecycle { return lc })
	return lc
}

// WithStopTimeout sets the timeout for stop operations and returns the Lifecycle instance.
// This allows for method chaining during initialization.
func (lc *Lifecycle) WithStopTimeout(timeout time.Duration) *Lifecycle {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.stopTimeout = timeout
	return lc
}

// WithLogger sets the logger for the lifecycle and returns the Lifecycle instance.
// This allows for method chaining during initialization.
func (lc *Lifecycle) WithLogger(logger *slog.Logger) *Lifecycle {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.logger = logger
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

// Provide registers constructors and tracks all non-error return types for auto-resolution.
// This overrides the embedded Injector's Provide method to enable auto-resolution of types.
func (lc *Lifecycle) Provide(ctors ...any) error {
	if lc.served.Load() {
		return ErrServed
	}

	return lc.Injector.Provide(ctors...)
}

// Serve provides a complete lifecycle management solution.
// It automatically resolves all provided types, starts all actors, monitors long-running services, and ensures proper cleanup.
//
// Process:
// 1. Automatically resolves all provided types with context support
// 2. Checks if there are long-running services to monitor
// 3. Starts all registered actors via internal start logic
// 4. Sets up automatic cleanup via defer for each started actor
// 5. Monitors long-running services
// 6. Returns when any service completes or context is cancelled
//
// The cleanup is guaranteed to run even if any step fails,
// using defer to ensure each started actor is properly stopped.
// The ctx parameter controls the long-running monitoring process and can be used to cancel the entire operation.
func (lc *Lifecycle) Serve(ctx context.Context) (xerr error) {
	if !lc.served.CompareAndSwap(false, true) {
		return ErrServed
	}

	// Automatically resolve all provided types with context
	if err := lc.Injector.BuildContext(inject.Context(ctx)); err != nil {
		return err
	}

	lc.mu.RLock()
	actors := slices.Clone(lc.actors)
	logger := lc.logger
	stopTimeout := lc.stopTimeout
	lc.mu.RUnlock()

	// Check for long-running services before starting actors
	var services []Service
	for _, actor := range actors {
		if svc, ok := actor.(Service); ok {
			services = append(services, svc)
		}
	}
	if len(services) == 0 {
		return ErrNoServices
	}

	actorNames := make([]string, len(actors))
	for i, actor := range actors {
		actorNames[i] = getActorName(actor, i)
	}

	logger.InfoContext(ctx, "Starting lifecycle", "actor_count", len(actors), "service_count", len(services))

	// Start all actors with individual defer cleanup
	for i, actor := range actors {
		actorName := actorNames[i]
		actorType := "Actor"
		if _, ok := actor.(Service); ok {
			actorType = "Service"
		}

		if err := actor.Start(ctx); err != nil {
			logger.ErrorContext(ctx, fmt.Sprintf("Failed to start %s", strings.ToLower(actorType)), "actor", actorName, "error", err)
			return err
		}
		logger.DebugContext(ctx, fmt.Sprintf("%s started successfully", actorType), "actor", actorName)

		// Set up cleanup for this specific actor
		defer func(actor Actor, actorName string) {
			stopCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
			defer cancel()

			stopCtx = withStopCause(stopCtx, xerr)
			if err := actor.Stop(stopCtx); err != nil {
				logger.ErrorContext(ctx, fmt.Sprintf("Failed to stop %s during cleanup", strings.ToLower(actorType)), "actor", actorName, "error", err)
			} else {
				logger.DebugContext(ctx, fmt.Sprintf("%s stopped successfully", actorType), "actor", actorName)
			}
		}(actor, actorName)
	}

	logger.InfoContext(ctx, "All actors started successfully", "actor_count", len(actors))

	// Start monitoring goroutines for each service
	g, gCtx := errgroup.WithContext(ctx)
	for i, actor := range actors {
		svc, ok := actor.(Service)
		if !ok {
			continue
		}
		actorName := actorNames[i]
		g.Go(func() error {
			select {
			case <-svc.Done():
				// Service completed, return its error (may be nil)
				err := svc.Err()
				if err != nil {
					logger.ErrorContext(gCtx, "Service completed with error", "actor", actorName, "error", err)
				} else {
					logger.InfoContext(gCtx, "Service completed successfully", "actor", actorName)
				}
				return err
			case <-gCtx.Done():
				// Context cancelled or another service completed
				logger.DebugContext(gCtx, "Service monitoring cancelled", "actor", actorName)
				return context.Cause(gCtx)
			}
		})
	}

	logger.InfoContext(ctx, "Monitoring services", "service_count", len(services))

	// Wait for any service to complete or context cancellation
	err := g.Wait()
	if err == nil || errors.Is(err, context.Canceled) {
		logger.InfoContext(ctx, "Lifecycle cancelled")
		return nil
	}
	logger.ErrorContext(ctx, "Lifecycle failed", "error", err)
	return err
}

// getActorName returns a human-readable name for an actor, using Named interface if available
func getActorName(actor Actor, index int) string {
	var name string
	if named, ok := actor.(Named); ok {
		name = named.GetName()
	}
	if name == "" {
		name = fmt.Sprintf("[%d](%T)", index, actor)
	}
	return name
}
