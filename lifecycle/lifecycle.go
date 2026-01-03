package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/theplant/inject"
	"golang.org/x/sync/errgroup"
)

// DefaultStopTimeout is the default timeout for stopping all actors during shutdown.
var DefaultStopTimeout = 30 * time.Second

// DefaultStopEachTimeout is the default timeout for stopping each individual actor.
var DefaultStopEachTimeout = 5 * time.Second

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

// RequiresStop defines an interface for actors that need cleanup even if their Start method is not called.
// This is useful for actors that acquire resources during construction and need cleanup regardless of whether Start is called.
type RequiresStop interface {
	RequiresStop() bool
}

const (
	// StageDefault is the default stage for actors without explicit stage.
	StageDefault = 0

	// StageReadiness is the default stage for actors with readiness probe enabled.
	// It's very high to ensure readiness-probe actors start last.
	StageReadiness = math.MaxInt - 1000
)

// Staged defines an optional interface for actors that can specify their execution stage
type Staged interface {
	GetStage() int
}

// Lifecycle also implements Service and RequiresReadinessProbe interfaces.
var (
	_ inject.Resolver        = (*Lifecycle)(nil)
	_ Service                = (*Lifecycle)(nil)
	_ RequiresReadinessProbe = (*Lifecycle)(nil)
)

// Lifecycle manages the lifecycle of multiple actor instances.
// It provides coordinated startup, monitoring, and cleanup of both simple actors and long-running services.
type Lifecycle struct {
	*inject.Injector // Embedded injector for dependency management
	*FuncService

	actors          []Actor
	stopTimeout     time.Duration // Global timeout for stopping all actors
	stopEachTimeout time.Duration // Timeout for each individual actor
	served          atomic.Bool
	mu              sync.RWMutex
	logger          *slog.Logger
	readinessProbe  *ReadinessProbe // Probe signaled when Start completes
}

// New creates a new Lifecycle instance with embedded injector and default stop timeout.
func New() *Lifecycle {
	inj := inject.New()
	lc := &Lifecycle{
		Injector:        inj,
		stopTimeout:     DefaultStopTimeout,
		stopEachTimeout: DefaultStopEachTimeout,
		logger:          slog.Default(),
		readinessProbe:  NewReadinessProbe(),
	}
	lc.FuncService = NewFuncService(func(ctx context.Context) error {
		return lc.Serve(ctx)
	}).WithName("lifecycle")
	_ = inj.Provide(func() *Lifecycle { return lc })
	return lc
}

// RequiresReadinessProbe returns the readiness probe for this lifecycle.
// The probe is signaled when Start completes successfully.
// This allows parent lifecycles to wait for nested lifecycles to be ready.
func (lc *Lifecycle) RequiresReadinessProbe() *ReadinessProbe {
	return lc.readinessProbe
}

// WithStop is not supported for Lifecycle.
func (lc *Lifecycle) WithStop(stop func(ctx context.Context) error) *Lifecycle {
	panic("WithStop is not supported for Lifecycle")
}

// IsStarted returns true if the lifecycle has been started.
func (lc *Lifecycle) IsStarted() bool {
	return lc.FuncService.IsStarted() || lc.served.Load()
}

func collectServices(actors []Actor) []Service {
	var services []Service
	for _, actor := range actors {
		if svc, ok := actor.(Service); ok {
			services = append(services, svc)
		}
	}
	return services
}

// sortActorsByStage sorts actors by their stage value (lower stages first).
// Actors without Staged interface are treated as stage 0.
// This is a stable sort, preserving the original order for actors with the same stage.
func sortActorsByStage(actors []Actor) []Actor {
	slices.SortStableFunc(actors, func(a, b Actor) int {
		stageA := StageDefault
		stageB := StageDefault
		if staged, ok := a.(Staged); ok {
			stageA = staged.GetStage()
		}
		if staged, ok := b.(Staged); ok {
			stageB = staged.GetStage()
		}
		if stageA < stageB {
			return -1
		}
		if stageA > stageB {
			return 1
		}
		return 0
	})
	return actors
}

// Start builds the context and starts the lifecycle.
func (lc *Lifecycle) Start(ctx context.Context) (xerr error) {
	// Signal readiness probe when Start completes
	defer func() { lc.readinessProbe.Signal(xerr) }()

	if err := lc.BuildContext(ctx); err != nil {
		return err
	}

	lc.mu.RLock()
	actors := sortActorsByStage(slices.Clone(lc.actors))
	logger := lc.logger
	lc.mu.RUnlock()

	// Check for long-running services before starting actors
	services := collectServices(actors)
	if len(services) == 0 {
		return errors.WithStack(ErrNoServices)
	}

	err := lc.FuncService.Start(ctx)
	if err != nil {
		return err
	}

	// Collect all readiness probes from multiple sources
	var allProbes []*ReadinessProbe

	// 1. Resolve probes from Element/Slice pattern
	var probes inject.Slice[*ReadinessProbe]
	if err := lc.ResolveContext(ctx, &probes); err != nil && !errors.Is(err, inject.ErrTypeNotProvided) {
		return err
	}
	if len(probes) > 0 {
		allProbes = append(allProbes, probes...)
	}

	// 2. Collect probes from actors implementing RequiresReadinessProbe interface
	for _, actor := range actors {
		if prober, ok := actor.(RequiresReadinessProbe); ok {
			if probe := prober.RequiresReadinessProbe(); probe != nil {
				allProbes = append(allProbes, probe)
			}
		}
	}

	// Wait for all probes to signal ready
	for i, probe := range allProbes {
		probeName := probe.GetName()
		if probeName == "" {
			probeName = fmt.Sprintf("probe[%d]", i)
		}
		logger.DebugContext(ctx, "Waiting for readiness probe", "probe", probeName)
		select {
		case <-lc.Done():
			return lc.Err()
		case <-ctx.Done():
			return errors.WithStack(ctx.Err())
		case <-probe.Done():
			if err := probe.Error(); err != nil {
				logger.ErrorContext(ctx, "Readiness probe failed", "probe", probeName, "error", err)
				return err
			}
			logger.DebugContext(ctx, "Readiness probe signaled ready", "probe", probeName)
		}
	}

	return nil
}

// WithName sets the name for the lifecycle.
// Also updates the readiness probe name.
func (lc *Lifecycle) WithName(name string) *Lifecycle {
	lc.FuncService.WithName(name)
	lc.readinessProbe.WithName(name)
	return lc
}

// WithStopTimeout sets the global timeout duration for stopping all actors during shutdown.
func (lc *Lifecycle) WithStopTimeout(timeout time.Duration) *Lifecycle {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.stopTimeout = timeout
	return lc
}

// WithStopEachTimeout sets the timeout duration for stopping each individual actor.
func (lc *Lifecycle) WithStopEachTimeout(timeout time.Duration) *Lifecycle {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.stopEachTimeout = timeout
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

// Provide registers constructors and tracks all non-error return types for auto-resolution.
// This overrides the embedded Injector's Provide method to enable auto-resolution of types.
func (lc *Lifecycle) Provide(ctors ...any) error {
	if lc.served.Load() {
		return errors.WithStack(ErrServed)
	}

	return lc.Injector.Provide(ctors...)
}

// errServiceCompleted is used internally to signal that a service has completed normally
var errServiceCompleted = errors.New("service completed")

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
func (lc *Lifecycle) Serve(ctx context.Context, ctors ...any) (xerr error) {
	if !lc.served.CompareAndSwap(false, true) {
		return errors.WithStack(ErrServed)
	}

	// Track which actors have been started using actor identity.
	// This works reliably for pointer types and most value types.
	startedActors := make(map[Actor]bool)

	defer func() {
		lc.mu.RLock()
		actors := sortActorsByStage(slices.Clone(lc.actors))
		stopTimeout := lc.stopTimeout
		stopEachTimeout := lc.stopEachTimeout
		logger := lc.logger
		lc.mu.RUnlock()

		stopCtx, stopCancel := context.WithTimeout(context.Background(), stopTimeout)
		stopCtx = withStopCause(stopCtx, xerr)
		defer stopCancel()

		for i := len(actors) - 1; i >= 0; i-- {
			actor := actors[i]

			needsStop := false
			if rs, ok := actor.(RequiresStop); ok && rs.RequiresStop() {
				needsStop = true
			}
			if !needsStop && startedActors[actor] {
				needsStop = true
			}

			if needsStop {
				actorName := getActorName(actor, i)
				actorType := "Actor"
				if _, ok := actor.(Service); ok {
					actorType = "Service"
				}

				stopEachCtx, stopEachCancel := context.WithTimeout(stopCtx, stopEachTimeout)
				if err := actor.Stop(stopEachCtx); err != nil {
					logger.ErrorContext(ctx, fmt.Sprintf("Failed to stop %s during cleanup", strings.ToLower(actorType)), "actor", actorName, "error", err)
				} else {
					logger.DebugContext(ctx, fmt.Sprintf("%s stopped successfully during cleanup", actorType), "actor", actorName)
				}
				stopEachCancel()
			}
		}
	}()

	// Automatically resolve all provided types with context
	if err := lc.BuildContext(ctx, ctors...); err != nil {
		return err
	}

	lc.mu.RLock()
	actors := sortActorsByStage(slices.Clone(lc.actors))
	logger := lc.logger
	lc.mu.RUnlock()

	// Check for long-running services before starting actors
	services := collectServices(actors)
	if len(services) == 0 {
		return errors.WithStack(ErrNoServices)
	}

	logger.InfoContext(ctx, "Starting lifecycle", "actor_count", len(actors), "service_count", len(services))

	// Start all actors
	for i, actor := range actors {
		actorName := getActorName(actor, i)
		actorType := "Actor"
		if _, ok := actor.(Service); ok {
			actorType = "Service"
		}

		logger.DebugContext(ctx, fmt.Sprintf("%s starting", actorType), "actor", actorName)

		if err := actor.Start(ctx); err != nil {
			logger.ErrorContext(ctx, fmt.Sprintf("Failed to start %s", strings.ToLower(actorType)), "actor", actorName, "error", err)
			return err
		}

		startedActors[actor] = true
		logger.DebugContext(ctx, fmt.Sprintf("%s started successfully", actorType), "actor", actorName)
	}

	logger.InfoContext(ctx, "All actors started successfully", "actor_count", len(actors))

	// Start monitoring goroutines for each service
	g, gCtx := errgroup.WithContext(ctx)
	for i, actor := range actors {
		svc, ok := actor.(Service)
		if !ok {
			continue
		}
		actorName := getActorName(actor, i)
		g.Go(func() error {
			select {
			case <-svc.Done():
				err := svc.Err()
				if err != nil {
					logger.ErrorContext(gCtx, "Service completed with error", "actor", actorName, "error", err)
					return err
				}
				logger.InfoContext(gCtx, "Service completed successfully", "actor", actorName)
				// Return special error to trigger lifecycle shutdown when service completes normally
				return errors.WithStack(errServiceCompleted)
			case <-gCtx.Done():
				err := errors.WithStack(gCtx.Err())
				logger.DebugContext(gCtx, "Service monitoring cancelled", "actor", actorName, "cause", err)
				return err
			}
		})
	}

	logger.InfoContext(ctx, "Monitoring services", "service_count", len(services))

	// Wait for any service to complete or context cancellation
	err := g.Wait()
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, errServiceCompleted) {
		logger.InfoContext(ctx, "Lifecycle completed")
		return nil
	}
	logger.ErrorContext(ctx, "Lifecycle failed", "error", err)
	return err
}

// Serve is a convenience function that creates a new Lifecycle instance and calls Serve on it.
func Serve(ctx context.Context, ctors ...any) error {
	lc := New()
	return lc.Serve(ctx, ctors...)
}

// Provide is a convenience function that creates a new Lifecycle instance and calls Provide on it.
func Provide(ctors ...any) (*Lifecycle, error) {
	lc := New()
	if err := lc.Provide(ctors...); err != nil {
		return nil, err
	}
	return lc, nil
}

// Start is a convenience function that creates a new Lifecycle instance, provides the constructors, and calls Start on it.
func Start(ctx context.Context, ctors ...any) (*Lifecycle, error) {
	lc, err := Provide(ctors...)
	if err != nil {
		return nil, err
	}
	if err := lc.Start(ctx); err != nil {
		return nil, err
	}
	return lc, nil
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
