package lifecycle

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSignalService_StopBehavior(t *testing.T) {
	svc := NewSignalService()

	err := svc.Start(context.Background())
	require.NoError(t, err)
	require.True(t, svc.IsStarted())

	select {
	case <-svc.Done():
		t.Fatal("Service should not be done before Stop is called")
	case <-time.After(10 * time.Millisecond):
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = svc.Stop(stopCtx)
	require.NoError(t, err)

	select {
	case <-svc.Done():
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Service should be done after Stop is called")
	}

	err = svc.Err()
	require.Nil(t, err) // FuncService.Err() returns nil for context.Canceled
}

func TestSignalService_EmbeddingWorks(t *testing.T) {
	svc := NewSignalService()

	require.False(t, svc.IsStarted())
	require.NotNil(t, svc.Done())
	require.Nil(t, svc.Err())
}

func TestSignalService_Constructor(t *testing.T) {
	svc := NewSignalService()

	require.NotNil(t, svc)
	require.NotNil(t, svc.FuncService)

	// Should implement Service interface
	var _ Service = svc

	require.False(t, svc.IsStarted())
}

func TestSetupSignal_Integration(t *testing.T) {
	lc := New()

	svc := SetupSignal(lc)

	require.NotNil(t, svc)
	require.NotNil(t, svc.FuncService)
	require.Len(t, lc.actors, 1)
}

func TestSignalService_WithName(t *testing.T) {
	signal := NewSignalService()
	result := signal.WithName("custom-signal")

	require.Equal(t, signal, result)
	require.Equal(t, "custom-signal", signal.GetName())
}

func TestSetupSignalWith(t *testing.T) {
	t.Run("with custom signals", func(t *testing.T) {
		lc := New()

		setupFunc := SetupSignalWith(syscall.SIGTERM, syscall.SIGUSR1)
		signal := setupFunc(lc)

		require.NotNil(t, signal)
		require.Len(t, lc.actors, 1)
	})

	t.Run("with no signals (defaults)", func(t *testing.T) {
		lc := New()

		// Defaults to SIGINT, SIGTERM when no signals provided
		setupFunc := SetupSignalWith()
		signal := setupFunc(lc)

		require.NotNil(t, signal)
		require.Len(t, lc.actors, 1)
	})
}

// TestSignalService_RealSignals tests that SignalService actually responds to SIGINT and SIGTERM
func TestSignalService_RealSignals(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping signal test in short mode")
	}

	testSignal := func(signum syscall.Signal) {
		svc := NewSignalService()

		err := svc.Start(context.Background())
		require.NoError(t, err)
		require.True(t, svc.IsStarted())

		select {
		case <-svc.Done():
			t.Fatal("Service should not be done before signal")
		case <-time.After(10 * time.Millisecond):
		}

		go func() {
			time.Sleep(50 * time.Millisecond)
			err := syscall.Kill(os.Getpid(), signum)
			require.NoError(t, err)
		}()

		select {
		case <-svc.Done():
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Service should complete when signal is received")
		}

		err = svc.Err()
		require.Nil(t, err) // Graceful signal shutdown should not return error
	}
	t.Run("SIGINT", func(t *testing.T) {
		testSignal(syscall.SIGINT)
	})
	t.Run("SIGTERM", func(t *testing.T) {
		testSignal(syscall.SIGTERM)
	})
}
