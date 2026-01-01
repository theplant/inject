package lifecycle_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/theplant/inject"
	"github.com/theplant/inject/lifecycle"
)

func SetupHTTPListener(lc *lifecycle.Lifecycle) (net.Listener, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	lc.Add(lifecycle.NewFuncActor(nil, func(ctx context.Context) error {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	}).WithName("http-listener"))
	return listener, nil
}

func SetupHTTPServer(lc *lifecycle.Lifecycle, listener net.Listener) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    listener.Addr().String(),
		Handler: mux,
	}

	lc.Add(lifecycle.NewFuncService(func(ctx context.Context) error {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}).WithStop(func(ctx context.Context) error {
		return server.Shutdown(ctx)
	}).WithName("http-server"))

	return server, nil
}

func SetupReadinessProbe(lc *lifecycle.Lifecycle, listener net.Listener) *inject.Element[*lifecycle.ReadinessProbe] {
	probe := lifecycle.NewReadinessProbe()

	addr := fmt.Sprintf("http://%s/health", listener.Addr().String())

	// Add a readiness check actor that signals when HTTP server is ready
	lc.Add(lifecycle.NewFuncActor(func(ctx context.Context) (xerr error) {
		defer func() {
			probe.Signal(xerr)
		}()
		err := WaitForReady(ctx, addr)
		if err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		return nil
	}, nil).WithName("readiness-probe"))

	return inject.NewElement(probe)
}

func SetupFailingReadinessProbe(lc *lifecycle.Lifecycle) *inject.Element[*lifecycle.ReadinessProbe] {
	probe := lifecycle.NewReadinessProbe()

	// Add a dummy service to satisfy lifecycle requirements
	lc.Add(lifecycle.NewFuncService(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}).WithName("dummy-service"))

	// Add a readiness check that always fails
	lc.Add(lifecycle.NewFuncActor(func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond)
		probe.Signal(errors.New("readiness check failed"))
		return nil
	}, nil).WithName("failing-readiness-probe"))

	return inject.NewElement(probe)
}

// WaitForReady polls the given endpoint until it returns a successful response or context is cancelled.
func WaitForReady(ctx context.Context, endpoint string) error {
	client := &http.Client{Timeout: 100 * time.Millisecond}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			resp, err := client.Get(endpoint)
			if resp != nil {
				defer resp.Body.Close()
			}
			if err == nil && resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

func TestSetupReadinessProbe(t *testing.T) {
	t.Run("Start blocks until HTTP server is ready", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		startTime := time.Now()
		lc, err := lifecycle.Start(ctx,
			SetupHTTPListener,
			SetupHTTPServer,
			SetupReadinessProbe,
		)
		elapsed := time.Since(startTime)
		require.NoError(t, err)
		require.True(t, lc.IsStarted())

		require.True(t, elapsed >= 100*time.Millisecond)
		t.Logf("Start completed in %v", elapsed)

		require.NoError(t, lc.Stop(context.Background()))
	})

	t.Run("Start returns immediately without ReadinessProbe", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		lc, err := lifecycle.Start(ctx,
			SetupHTTPListener,
			SetupHTTPServer,
		)
		require.NoError(t, err)
		require.True(t, lc.IsStarted())

		require.NoError(t, lc.Stop(context.Background()))
	})

	t.Run("Start respects context cancellation while waiting for readiness", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		_, err := lifecycle.Start(ctx,
			SetupHTTPListener,
			SetupHTTPServer,
			SetupReadinessProbe,
		)
		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("Start returns error when readiness probe fails", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := lifecycle.Start(ctx,
			SetupFailingReadinessProbe,
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "readiness check failed")
	})
}

func TestMultipleReadinessProbes(t *testing.T) {
	t.Run("Start waits for all probes to be ready", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var probe1Ready, probe2Ready bool

		lc, err := lifecycle.Start(ctx,
			SetupHTTPListener,
			SetupHTTPServer,
			// First probe - signals after 50ms
			func(lc *lifecycle.Lifecycle) *inject.Element[*lifecycle.ReadinessProbe] {
				probe := lifecycle.NewReadinessProbe()
				lc.Add(lifecycle.NewFuncActor(func(ctx context.Context) error {
					time.Sleep(50 * time.Millisecond)
					probe1Ready = true
					probe.Signal(nil)
					return nil
				}, nil).WithName("probe1"))
				return inject.NewElement(probe)
			},
			// Second probe - signals after 100ms
			func(lc *lifecycle.Lifecycle) *inject.Element[*lifecycle.ReadinessProbe] {
				probe := lifecycle.NewReadinessProbe()
				lc.Add(lifecycle.NewFuncActor(func(ctx context.Context) error {
					time.Sleep(100 * time.Millisecond)
					probe2Ready = true
					probe.Signal(nil)
					return nil
				}, nil).WithName("probe2"))
				return inject.NewElement(probe)
			},
		)
		require.NoError(t, err)
		require.True(t, lc.IsStarted())

		// Both probes should be ready after Start returns
		require.True(t, probe1Ready, "probe1 should be ready")
		require.True(t, probe2Ready, "probe2 should be ready")

		require.NoError(t, lc.Stop(context.Background()))
	})

	t.Run("Start fails if any probe fails", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		type dummyService struct{}

		_, err := lifecycle.Start(ctx,
			// Dummy service to satisfy lifecycle requirements
			func(lc *lifecycle.Lifecycle) *dummyService {
				lc.Add(lifecycle.NewFuncService(func(ctx context.Context) error {
					<-ctx.Done()
					return ctx.Err()
				}).WithName("dummy-service"))
				return &dummyService{}
			},
			// First probe - succeeds
			func(lc *lifecycle.Lifecycle) *inject.Element[*lifecycle.ReadinessProbe] {
				probe := lifecycle.NewReadinessProbe()
				lc.Add(lifecycle.NewFuncActor(func(ctx context.Context) error {
					time.Sleep(20 * time.Millisecond)
					probe.Signal(nil)
					return nil
				}, nil).WithName("probe1"))
				return inject.NewElement(probe)
			},
			// Second probe - fails
			func(lc *lifecycle.Lifecycle) *inject.Element[*lifecycle.ReadinessProbe] {
				probe := lifecycle.NewReadinessProbe()
				lc.Add(lifecycle.NewFuncActor(func(ctx context.Context) error {
					time.Sleep(50 * time.Millisecond)
					probe.Signal(errors.New("probe2 failed"))
					return nil
				}, nil).WithName("probe2"))
				return inject.NewElement(probe)
			},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "probe2 failed")
	})
}

func TestRequiresReadinessProbeInterface(t *testing.T) {
	t.Run("Lifecycle implements RequiresReadinessProbe", func(t *testing.T) {
		lc := lifecycle.New()
		var _ lifecycle.RequiresReadinessProbe = lc
		require.NotNil(t, lc.RequiresReadinessProbe())
	})

	t.Run("Nested lifecycle - parent waits for child", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		type subSystem struct {
			*lifecycle.Lifecycle
		}

		startTime := time.Now()
		lc, err := lifecycle.Start(ctx,
			func(parent *lifecycle.Lifecycle) (*subSystem, error) {
				sub, err := lifecycle.Provide(
					SetupHTTPListener,
					SetupHTTPServer,
					SetupReadinessProbe,
				)
				if err != nil {
					return nil, err
				}
				parent.Add(sub.WithName("subsystem"))
				return &subSystem{sub}, nil
			},
		)
		elapsed := time.Since(startTime)
		require.NoError(t, err)
		require.True(t, lc.IsStarted())

		require.True(t, elapsed >= 100*time.Millisecond)
		t.Logf("Start completed in %v", elapsed)

		require.NoError(t, lc.Stop(context.Background()))
	})
}

func TestReadinessProbe(t *testing.T) {
	t.Run("Signal with nil works correctly", func(t *testing.T) {
		probe := lifecycle.NewReadinessProbe()
		require.NotNil(t, probe)

		// Before signal - need to use reflection to access private field or just test behavior
		require.Nil(t, probe.Error())

		// Signal success
		probe.Signal(nil)

		// After signal
		require.Nil(t, probe.Error())
	})

	t.Run("Signal with error works correctly", func(t *testing.T) {
		probe := lifecycle.NewReadinessProbe()
		testErr := errors.New("test error")

		// Before signal
		require.Nil(t, probe.Error())

		// Signal failure
		probe.Signal(testErr)

		// After signal
		require.ErrorIs(t, probe.Error(), testErr)
	})

	t.Run("Only one signal can be sent", func(t *testing.T) {
		probe := lifecycle.NewReadinessProbe()

		probe.Signal(nil)
		probe.Signal(errors.New("should be ignored"))

		require.Nil(t, probe.Error(), "Error should still be nil after first signal")
	})

	t.Run("Second signal after first is ignored", func(t *testing.T) {
		probe := lifecycle.NewReadinessProbe()

		probe.Signal(errors.New("first error"))
		probe.Signal(nil) // Should be ignored

		require.Error(t, probe.Error(), "Error should still be set after first signal")
		require.Contains(t, probe.Error().Error(), "first error")
	})
}
