package lifecycle

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/theplant/inject"
)

// Mock database following gorm.DB pattern and implementing Actor interface
type DB struct {
	Name    string
	Payload string
	closed  atomic.Bool
}

func (db *DB) Close() error {
	db.closed.Store(true)
	return nil
}

type ctxKeyPayload struct{}

func OpenDB(ctx context.Context, name string) (*DB, error) {
	payload, _ := ctx.Value(ctxKeyPayload{}).(string)
	return &DB{Name: name, Payload: payload}, nil
}

func SetupDB(ctx inject.Context, lc *Lifecycle) (*DB, error) {
	db, err := OpenDB(ctx, "test_db")
	if err != nil {
		return nil, err
	}
	lc.Add(
		NewFuncActor(nil, func(_ context.Context) error {
			return db.Close()
		}).WithName("db"),
	)
	return db, nil
}

type HTTPConfig struct {
	Port int
	DB   *DB `inject:""`
}

// HTTPService handles HTTP requests
type HTTPService struct {
	*HTTPConfig `inject:""`
	running     atomic.Bool
}

func (h *HTTPService) Serve() error {
	h.running.Store(true)
	for {
		if !h.IsRunning() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *HTTPService) Close() error {
	h.running.Store(false)
	return nil
}

func (h *HTTPService) IsRunning() bool {
	return h.running.Load()
}

var SetupHTTPService = []any{
	func() *HTTPConfig {
		return &HTTPConfig{Port: 8080}
	},
	func(lc *Lifecycle) *HTTPService {
		svc := &HTTPService{}
		lc.Add(
			NewFuncService(func(_ context.Context) error {
				return svc.Serve()
			}).WithStop(func(_ context.Context) error {
				return svc.Close()
			}).WithName("http"),
		)
		return svc
	},
}

// MockActor is a simple actor implementation for testing
type MockActor struct {
	name     string
	startErr error
	stopErr  error
}

func (m *MockActor) GetName() string                 { return m.name }
func (m *MockActor) Start(ctx context.Context) error { return m.startErr }
func (m *MockActor) Stop(ctx context.Context) error  { return m.stopErr }

func TestCircularDependencyDetectionInLifecycle(t *testing.T) {
	lc := New()

	type Config struct {
		Port int
	}

	type Service struct {
		Config *Config `inject:""`
	}

	// This should trigger circular dependency detection
	err := lc.Provide(func() (*Config, *Service) {
		conf := &Config{Port: 8080}
		svc := &Service{} // This will create circular dependency
		return conf, svc
	})

	// Should detect circular dependency at provide time, not runtime
	require.ErrorIs(t, err, inject.ErrCircularDependency)
	require.Contains(t, err.Error(), "*lifecycle.Config -> *lifecycle.Config@*lifecycle.Service")
}

func TestLifecycle(t *testing.T) {
	lc := New().WithLogger(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	type DummyActor struct {
		*FuncActor
		stopCause error
	}

	type DummyService struct {
		*FuncService
		stopCause error
	}

	// Provide dependencies directly to Lifecycle
	require.NoError(t, lc.Provide(
		// Signal service (Service)
		SetupSignal,
		// Database dependency (Actor) - can receive context during construction
		SetupDB,
		// HTTP service (Service)
		SetupHTTPService,
		// Dummy actor
		func(lc *Lifecycle) *DummyActor {
			d := &DummyActor{}
			d.FuncActor = NewFuncActor(func(_ context.Context) error {
				return nil
			}, func(ctx context.Context) error {
				d.stopCause = GetStopCause(ctx)
				return nil
			}).WithName("dummy-actor")
			return Add(lc, d)
		},
		// Dummy service
		func(lc *Lifecycle) *DummyService {
			d := &DummyService{}
			d.FuncService = NewFuncService(func(ctx context.Context) error {
				// Keep running until context is cancelled
				time.Sleep(100 * time.Millisecond) // Run longer than test timeout
				return ctx.Err()
			}).WithStop(func(ctx context.Context) error {
				d.stopCause = GetStopCause(ctx)
				return nil
			}).WithName("dummy-service")
			return Add(lc, d)
		},
	))

	// Provide context for manual resolution
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx = context.WithValue(ctx, ctxKeyPayload{}, "testDBPayload")

	// Test with timeout - context will be automatically provided to constructors
	err := lc.Serve(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// Verify dependency injection worked
	_, err = lc.Invoke(func(db *DB, httpService *HTTPService, dummyActor *DummyActor, dummyService *DummyService) {
		require.Equal(t, 8080, httpService.Port)
		require.Equal(t, db, httpService.DB)
		require.False(t, httpService.IsRunning())

		require.Equal(t, "test_db", db.Name)
		require.Equal(t, "testDBPayload", db.Payload)
		require.True(t, db.closed.Load())

		require.ErrorIs(t, dummyActor.stopCause, context.DeadlineExceeded)
		require.ErrorIs(t, dummyService.stopCause, context.DeadlineExceeded)
	})
	require.NoError(t, err)
}

func TestServeConvenienceFunction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := Serve(ctx,
		func() string { return "test-value" },
		SetupSignal,
	)

	// Should timeout since we have a service running
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// Test error case - no services
	err = Serve(context.Background(),
		func() string { return "test" },
	)
	require.ErrorIs(t, err, ErrNoServices)
}

// TestBuilderMethods tests all builder methods for comprehensive coverage
func TestBuilderMethods(t *testing.T) {
	lc := New()

	// Test all builder methods in a chain
	result := lc.
		WithName("test-lifecycle").
		WithStopTimeout(10 * time.Second).
		WithStopEachTimeout(2 * time.Second).
		WithLogger(slog.Default())

	require.Equal(t, lc, result, "Builder methods should return the same instance")
	require.Equal(t, "test-lifecycle", lc.GetName())
	require.Equal(t, 10*time.Second, lc.stopTimeout)
	require.Equal(t, 2*time.Second, lc.stopEachTimeout)
}

// TestIsStarted tests the IsStarted method - requires access to FuncService
func TestIsStarted(t *testing.T) {
	lc := New()

	// Initially not started
	require.False(t, lc.IsStarted())

	// Start the underlying FuncService
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, lc.FuncService.Start(ctx))
	require.True(t, lc.IsStarted())
}

// TestLazyMethods demonstrates correct usage of LazyAdd and LazyAddE for deferred initialization
func TestLazyMethods(t *testing.T) {
	t.Run("LazyAdd defers expensive actor creation", func(t *testing.T) {
		lc, err := Start(
			context.Background(),
			LazyAdd(func() *MockActor {
				return &MockActor{name: "expensive-resource"}
			}),
		)
		require.NoError(t, err)
		defer func() {
			require.NoError(t, lc.Stop(context.Background()))
		}()

		actor := inject.MustResolve[*MockActor](lc)
		require.Equal(t, "expensive-resource", actor.name)
	})

	t.Run("LazyAddE handles initialization failures gracefully", func(t *testing.T) {
		// Define a separate type to avoid DI container conflicts
		type FailingService struct {
			*MockActor
		}

		// Ensure FailingService implements Actor
		var _ Actor = (*FailingService)(nil)

		lc, err := Start(
			context.Background(),
			LazyAddE(func() (*MockActor, error) {
				return &MockActor{name: "expensive-resource"}, nil
			}),
			LazyAddE(func() (*FailingService, error) {
				return nil, errors.New("service unavailable")
			}),
		)
		require.ErrorContains(t, err, "service unavailable")
		require.Nil(t, lc)
	})
}

// TestAdd demonstrates error-safe actor addition patterns
func TestAdd(t *testing.T) {
	lc, err := Start(
		context.Background(),
		func(lc *Lifecycle) *MockActor {
			return Add(lc, &MockActor{name: "expensive-resource"})
		},
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, lc.Stop(context.Background()))
	}()

	actor := inject.MustResolve[*MockActor](lc)
	require.Equal(t, "expensive-resource", actor.name)
}

// TestServiceCompletion tests service completion scenarios (lines 289-298 in lifecycle.go)
func TestServiceCompletion(t *testing.T) {
	t.Run("service completes successfully without error", func(t *testing.T) {
		// Real scenario: A batch processing service that completes after processing
		type BatchProcessor struct {
			*FuncService
			ProcessedCount atomic.Int32
		}

		completionSignal := make(chan struct{})

		lc := New().WithLogger(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})))

		require.NoError(t, lc.Provide(
			func(lc *Lifecycle) *BatchProcessor {
				processor := &BatchProcessor{}

				// Create a service that processes batches and completes
				processor.FuncService = NewFuncService(func(ctx context.Context) error {
					// Simulate batch processing
					close(completionSignal)
					processor.ProcessedCount.Store(100)

					// Complete successfully after processing
					return nil
				}).WithName("batch-processor")

				lc.Add(processor)
				return processor
			},
		))

		// Serve should complete when batch processing finishes
		err := lc.Serve(context.Background())
		require.NoError(t, err, "Serve should complete successfully when batch processing completes")

		// Verify processing happened
		select {
		case <-completionSignal:
			// Good, processing started
		default:
			t.Fatal("Batch processing should have started")
		}

		// Verify we can access the processor and its results
		processor := inject.MustResolve[*BatchProcessor](lc)
		require.Equal(t, int32(100), processor.ProcessedCount.Load())
	})

	t.Run("service completes with error", func(t *testing.T) {
		// Real scenario: A database migration service that fails
		type MigrationService struct {
			*FuncService
			MigrationName string
			Error         error
		}

		expectedErr := errors.New("migration failed: table already exists")
		startedSignal := make(chan struct{})

		lc := New().WithLogger(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})))

		require.NoError(t, lc.Provide(
			func(lc *Lifecycle) *MigrationService {
				migrationSvc := &MigrationService{
					MigrationName: "create_users_table",
				}

				// Create a migration service that fails
				migrationSvc.FuncService = NewFuncService(func(ctx context.Context) error {
					close(startedSignal)
					// Simulate migration work
					time.Sleep(10 * time.Millisecond)
					migrationSvc.Error = expectedErr
					return expectedErr
				}).WithName("db-migration")

				lc.Add(migrationSvc)
				return migrationSvc
			},
		))

		// Serve should return the migration error
		err := lc.Serve(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr, "Serve should return the migration error")

		// Verify migration actually started
		select {
		case <-startedSignal:
			// Good, migration started
		case <-time.After(time.Second):
			t.Fatal("Migration should have started")
		}

		// Verify we can access the migration service and its error
		migrationSvc := inject.MustResolve[*MigrationService](lc)
		require.Equal(t, "create_users_table", migrationSvc.MigrationName)
		require.ErrorIs(t, migrationSvc.Error, expectedErr)
	})

	t.Run("multiple services - first error wins", func(t *testing.T) {
		// Real scenario: Multiple services running concurrently
		type APIServerService struct {
			*FuncService
			Port int
		}

		type JobWorkerService struct {
			*FuncService
			QueueName string
		}

		type AppServices struct {
			APIServer *APIServerService
			JobWorker *JobWorkerService
		}

		apiErr := errors.New("API server: port already in use")
		workerErr := errors.New("job worker: queue connection failed")

		apiStarted := make(chan struct{})
		workerStarted := make(chan struct{})

		lc := New().WithLogger(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})))

		require.NoError(t, lc.Provide(
			func(lc *Lifecycle) *AppServices {
				// API Server - fails quickly
				apiServer := &APIServerService{Port: 8080}
				apiServer.FuncService = NewFuncService(func(ctx context.Context) error {
					close(apiStarted)
					time.Sleep(10 * time.Millisecond)
					return apiErr
				}).WithName("api-server")

				// Job Worker - fails slowly
				jobWorker := &JobWorkerService{QueueName: "default"}
				jobWorker.FuncService = NewFuncService(func(ctx context.Context) error {
					close(workerStarted)
					time.Sleep(100 * time.Millisecond) // Slower than API
					return workerErr
				}).WithName("job-worker")

				lc.Add(apiServer)
				lc.Add(jobWorker)

				return &AppServices{
					APIServer: apiServer,
					JobWorker: jobWorker,
				}
			},
		))

		// Should return the first error (API server error)
		err := lc.Serve(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, apiErr, "Should return the API server error (first to fail)")

		// Both services should have started
		select {
		case <-apiStarted:
		case <-time.After(time.Second):
			t.Fatal("API server should have started")
		}
		select {
		case <-workerStarted:
		case <-time.After(time.Second):
			t.Fatal("Job worker should have started")
		}

		// Verify we can access the services configuration
		appServices := inject.MustResolve[*AppServices](lc)
		require.Equal(t, 8080, appServices.APIServer.Port)
		require.Equal(t, "default", appServices.JobWorker.QueueName)
	})

	t.Run("service completes successfully with proper logging", func(t *testing.T) {
		// Real scenario: A data sync service that completes with logging
		type DataSyncService struct {
			*FuncService
			SourceDB  string
			TargetDB  string
			SyncCount atomic.Int32
		}

		// Capture log output to verify correct logging
		var logOutput strings.Builder
		logger := slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))

		completionSignal := make(chan struct{})

		lc := New().WithLogger(logger)

		require.NoError(t, lc.Provide(
			func(lc *Lifecycle) *DataSyncService {
				syncService := &DataSyncService{
					SourceDB: "users_prod",
					TargetDB: "users_analytics",
				}

				syncService.FuncService = NewFuncService(func(ctx context.Context) error {
					close(completionSignal)
					// Simulate data sync work
					time.Sleep(5 * time.Millisecond)
					syncService.SyncCount.Store(1500) // Synced 1500 records
					return nil
				}).WithName("data-sync")

				lc.Add(syncService)
				return syncService
			},
		))

		err := lc.Serve(context.Background())
		require.NoError(t, err)

		// Verify sync completed
		select {
		case <-completionSignal:
		default:
			t.Fatal("Data sync should have completed")
		}

		// Check log output contains success message
		logStr := logOutput.String()
		require.Contains(t, logStr, "Service completed successfully", "Log should contain success message")
		require.Contains(t, logStr, "data-sync", "Log should contain service name")

		// Verify we can access the sync service and its results
		syncService := inject.MustResolve[*DataSyncService](lc)
		require.Equal(t, "users_prod", syncService.SourceDB)
		require.Equal(t, "users_analytics", syncService.TargetDB)
		require.Equal(t, int32(1500), syncService.SyncCount.Load())
	})
}

// TestProvideErrorCases tests error cases in Provide method
func TestProvideErrorCases(t *testing.T) {
	t.Run("Provide after served", func(t *testing.T) {
		lc := New()
		lc.served.Store(true) // Simulate already served

		err := lc.Provide(func() *MockActor {
			return &MockActor{name: "after-served"}
		})

		require.Error(t, err)
		require.ErrorIs(t, err, ErrServed)
	})
}

// TestGetActorNameEdgeCases tests edge cases in getActorName function
func TestGetActorNameEdgeCases(t *testing.T) {
	t.Run("Actor with empty name", func(t *testing.T) {
		actor := &MockActor{name: ""}
		name := getActorName(actor, 5)
		require.Equal(t, "[5](*lifecycle.MockActor)", name)
	})

	t.Run("Actor with custom name", func(t *testing.T) {
		actor := &MockActor{name: "custom-actor"}
		name := getActorName(actor, 10)
		require.Equal(t, "custom-actor", name)
	})
}

// TestWithStopPanicBehavior tests that WithStop properly panics as expected
func TestWithStopPanicBehavior(t *testing.T) {
	lc := New()

	require.Panics(t, func() {
		lc.WithStop(func(ctx context.Context) error { return nil })
	}, "WithStop should panic when called on Lifecycle")
}
