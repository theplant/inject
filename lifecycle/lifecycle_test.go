package lifecycle_test

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/theplant/inject"
	"github.com/theplant/inject/lifecycle"
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

func SetupDB(ctx inject.Context, lc *lifecycle.Lifecycle) (*DB, error) {
	db, err := OpenDB(ctx, "test_db")
	if err != nil {
		return nil, err
	}
	lc.Add(
		lifecycle.NewFuncActor(nil, func(_ context.Context) error {
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
	func(lc *lifecycle.Lifecycle) *HTTPService {
		svc := &HTTPService{}
		lc.Add(
			lifecycle.NewFuncService(
				func(_ context.Context) error {
					return svc.Serve()
				},
			).WithStop(func(_ context.Context) error {
				return svc.Close()
			}).WithName("http"),
		)
		return svc
	},
}

func TestCircularDependencyDetectionInLifecycle(t *testing.T) {
	lc := lifecycle.New()

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
	require.Contains(t, err.Error(), "*lifecycle_test.Config -> *lifecycle_test.Config@*lifecycle_test.Service")
}

func TestLifecycle(t *testing.T) {
	lc := lifecycle.New().
		WithLogger(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})))

	type DummyActor struct {
		*lifecycle.FuncActor
		stopCause error
	}

	type DummyService struct {
		*lifecycle.FuncService
		stopCause error
	}

	// Provide dependencies directly to Lifecycle
	require.NoError(t, lc.Provide(
		// Signal service (Service)
		lifecycle.SetupSignal,
		// Database dependency (Actor) - can receive context during construction
		SetupDB,
		// HTTP service (Service)
		SetupHTTPService,
		// Dummy actor
		func(lc *lifecycle.Lifecycle) *DummyActor {
			d := &DummyActor{}
			d.FuncActor = lifecycle.NewFuncActor(func(_ context.Context) error {
				return nil
			}, func(ctx context.Context) error {
				d.stopCause = lifecycle.GetStopCause(ctx)
				return nil
			}).WithName("dummy-actor")
			return lifecycle.Add(lc, d)
		},
		// Dummy service
		func(lc *lifecycle.Lifecycle) *DummyService {
			d := &DummyService{}
			d.FuncService = lifecycle.NewFuncService(func(_ context.Context) error {
				return nil
			}).WithStop(func(ctx context.Context) error {
				d.stopCause = lifecycle.GetStopCause(ctx)
				return nil
			}).WithName("dummy-service")
			return lifecycle.Add(lc, d)
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

		require.Equal(t, context.DeadlineExceeded, dummyActor.stopCause)
		require.Equal(t, context.DeadlineExceeded, dummyService.stopCause)
	})
	require.NoError(t, err)
}

func TestServeConvenienceFunction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := lifecycle.Serve(ctx,
		func() string { return "test-value" },
		lifecycle.SetupSignal,
	)

	// Should timeout since we have a service running
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// Test error case - no services
	err = lifecycle.Serve(context.Background(),
		func() string { return "test" },
	)
	require.ErrorIs(t, err, lifecycle.ErrNoServices)
}
