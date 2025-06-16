package lifecycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/theplant/inject"
	"github.com/theplant/inject/lifecycle"
)

// Mock database following gorm.DB pattern and implementing Actor interface
type DB struct {
	Name   string
	closed bool
}

func OpenDB(_ context.Context, name string) (*DB, error) {
	return &DB{Name: name}, nil
}

func (db *DB) Start(ctx context.Context) error {
	// Simulate database connection/initialization
	return nil
}

func (db *DB) Stop(ctx context.Context) error {
	return db.Close()
}

func (db *DB) Close() error {
	db.closed = true
	return nil
}

// HTTPService handles HTTP requests
type HTTPService struct {
	DB      *DB `inject:""`
	done    chan struct{}
	running bool
}

func (h *HTTPService) Start(ctx context.Context) error {
	h.done = make(chan struct{})
	h.running = true

	// Simulate HTTP server start - run in background
	go func() {
		// Keep running until stopped
		for h.running {
			time.Sleep(10 * time.Millisecond)
		}
		// Signal completion when stopped
		close(h.done)
	}()
	return nil
}

func (h *HTTPService) Stop(ctx context.Context) error {
	h.running = false
	// Wait for the service to actually stop
	<-h.done
	return nil
}

func (h *HTTPService) Done() <-chan struct{} {
	return h.done
}

func (h *HTTPService) Err() error {
	return nil
}

func NewHTTPService() *HTTPService {
	return &HTTPService{}
}

func TestLifeCycleWithInject(t *testing.T) {
	lc := lifecycle.New()

	// Provide dependencies directly to Lifecycle
	require.NoError(t, lc.Provide(
		// Signal service (Service)
		lifecycle.AddSignalService,
		// Database dependency (Actor) - can receive context during construction
		func(ctx inject.Context) (*DB, error) {
			db, err := OpenDB(ctx, "test_db")
			return lifecycle.AddE(lc, db, err)
		},
		// HTTP service (Service)
		lifecycle.LazyAdd(lc, NewHTTPService),
	))

	// Provide context for manual resolution
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	type contextKey string
	const key contextKey = "testKey"
	ctx = context.WithValue(ctx, key, "testValue")
	lc.Provide(func(ctx inject.Context) string {
		return ctx.Value(key).(string)
	})

	// Test with timeout - context will be automatically provided to constructors
	err := lc.Serve(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// Verify dependency injection worked
	lc.Invoke(func(db *DB, httpService *HTTPService, str string) {
		require.Equal(t, db, httpService.DB)
		require.Equal(t, "test_db", db.Name)
		require.True(t, db.closed)
		require.Equal(t, "testValue", str)
	})
}
