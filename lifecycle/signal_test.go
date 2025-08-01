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
	// Create a signal service using the constructor
	svc := NewSignalService()

	// Start the service
	err := svc.Start(context.Background())
	require.NoError(t, err)

	// Verify it's started
	require.True(t, svc.IsStarted())

	// Service should not be done yet
	select {
	case <-svc.Done():
		t.Fatal("Service should not be done before Stop is called")
	case <-time.After(10 * time.Millisecond):
		// Expected behavior
	}

	// Call Stop - this should make the taskFunc return
	stopCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = svc.Stop(stopCtx)
	require.NoError(t, err)

	// Now Done() should be closed
	select {
	case <-svc.Done():
		// Expected behavior - taskFunc should have returned
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Service should be done after Stop is called")
	}

	err = svc.Err()
	require.Nil(t, err) // FuncService.Err() returns nil for context.Canceled
}

func TestSignalService_EmbeddingWorks(t *testing.T) {
	svc := NewSignalService()

	// Should have all FuncService methods available
	require.False(t, svc.IsStarted())
	require.NotNil(t, svc.Done())
	require.Nil(t, svc.Err())
}

func TestSignalService_Constructor(t *testing.T) {
	svc := NewSignalService()

	// Should be properly initialized
	require.NotNil(t, svc)
	require.NotNil(t, svc.FuncService)

	// Should implement Service interface
	var _ Service = svc

	// Should not be started initially
	require.False(t, svc.IsStarted())
}

func TestSetupSignal_Integration(t *testing.T) {
	lc := New()

	// SetupSignal should return a properly configured SignalService
	svc := SetupSignal(lc)

	require.NotNil(t, svc)
	require.NotNil(t, svc.FuncService)

	// Should be added to lifecycle
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

		// Test with custom signals
		setupFunc := SetupSignalWith(syscall.SIGTERM, syscall.SIGUSR1)
		signal := setupFunc(lc)

		require.NotNil(t, signal)
		require.Len(t, lc.actors, 1)
	})

	t.Run("with no signals (defaults)", func(t *testing.T) {
		lc := New()

		// Test with no signals (should default to SIGINT, SIGTERM)
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

		// Start the service
		err := svc.Start(context.Background())
		require.NoError(t, err)
		require.True(t, svc.IsStarted())

		// Service should not be done initially
		select {
		case <-svc.Done():
			t.Fatal("Service should not be done before signal")
		case <-time.After(10 * time.Millisecond):
			// Expected behavior
		}

		// Send SIGINT to current process
		go func() {
			time.Sleep(50 * time.Millisecond)
			err := syscall.Kill(os.Getpid(), signum)
			require.NoError(t, err)
		}()

		// Service should complete when signal is received
		select {
		case <-svc.Done():
			// Expected behavior - signal was received
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Service should complete when SIGINT is received")
		}

		// Error should be nil for graceful signal shutdown
		err = svc.Err()
		require.Nil(t, err)
	}
	t.Run("SIGINT", func(t *testing.T) {
		testSignal(syscall.SIGINT)
	})
	t.Run("SIGTERM", func(t *testing.T) {
		testSignal(syscall.SIGTERM)
	})
}
