package lifecycle_test

import (
	"context"
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
	closed  bool
}

func (db *DB) Close() error {
	db.closed = true
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
	lifecycle.Add(lc, lifecycle.NewFuncActor(nil, func(_ context.Context) error {
		return db.Close()
	}))
	return db, nil
}

type HTTPConfig struct {
	Port int
	DB   *DB `inject:""`
}

// HTTPService handles HTTP requests
type HTTPService struct {
	*HTTPConfig `inject:""`
	running     int32
}

func (h *HTTPService) Serve() error {
	atomic.StoreInt32(&h.running, 1)
	for {
		if atomic.LoadInt32(&h.running) == 0 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *HTTPService) Close() error {
	atomic.StoreInt32(&h.running, 0)
	return nil
}

func (h *HTTPService) IsRunning() bool {
	return atomic.LoadInt32(&h.running) == 1
}

var SetupHTTPService = []any{
	func() *HTTPConfig {
		return &HTTPConfig{Port: 8080}
	},
	func(lc *lifecycle.Lifecycle) *HTTPService {
		svc := &HTTPService{}
		lc.Add(lifecycle.NewFuncService(
			func(_ context.Context) error {
				return svc.Serve()
			},
			func(_ context.Context) error {
				return svc.Close()
			},
		))
		return svc
	},
}

func TestLifecycle(t *testing.T) {
	lc := lifecycle.New()

	// Provide dependencies directly to Lifecycle
	require.NoError(t, lc.Provide(
		// Signal service (Service)
		lifecycle.SetupSignalService,
		// Database dependency (Actor) - can receive context during construction
		SetupDB,
		// HTTP service (Service)
		SetupHTTPService,
	))

	// Provide context for manual resolution
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx = context.WithValue(ctx, ctxKeyPayload{}, "testDBPayload")

	// Test with timeout - context will be automatically provided to constructors
	err := lc.Serve(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// Verify dependency injection worked
	lc.Invoke(func(db *DB, httpService *HTTPService, str string) {
		require.Equal(t, 8080, httpService.Port)
		require.Equal(t, db, httpService.DB)
		require.False(t, httpService.IsRunning())

		require.Equal(t, "test_db", db.Name)
		require.Equal(t, "testDBPayload", db.Payload)
		require.True(t, db.closed)
	})
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
