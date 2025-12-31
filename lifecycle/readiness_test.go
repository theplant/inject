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
		w.Write([]byte("OK"))
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

func SetupReadinessProbe(lc *lifecycle.Lifecycle, listener net.Listener) *lifecycle.ReadinessProbe {
	probe := lifecycle.NewReadinessProbe()

	addr := fmt.Sprintf("http://%s/health", listener.Addr().String())

	// Add a readiness check actor that signals when HTTP server is ready
	lc.Add(lifecycle.NewFuncActor(func(ctx context.Context) error {
		err := WaitForReady(ctx, addr)
		if err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		probe.SignalReady()
		return nil
	}, nil).WithName("readiness-probe"))

	return probe
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
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
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
}
