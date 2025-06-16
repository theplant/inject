package lifecycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/theplant/inject/lifecycle"
)

func TestFuncService_Done(t *testing.T) {
	taskStarted := make(chan bool, 1)
	taskCanFinish := make(chan bool, 1)

	service := lifecycle.NewFuncService(func(ctx context.Context) error {
		taskStarted <- true
		// Wait for permission to finish
		select {
		case <-taskCanFinish:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	ctx := context.Background()

	// Start the service
	require.NoError(t, service.Start(ctx))

	// Wait for task to start
	<-taskStarted

	// At this point, Done() should NOT be signaled yet
	select {
	case <-service.Done():
		t.Fatal("Done() should not be signaled while task is running")
	case <-time.After(10 * time.Millisecond):
		// Good - Done() not signaled yet
	}

	// Allow task to finish
	taskCanFinish <- true

	// Now Done() should be signaled
	select {
	case <-service.Done():
		// Good - Done() signaled after task completion
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Done() should be signaled after task completion")
	}

	// Error should be nil for successful completion
	require.Nil(t, service.Err())
}

func TestFuncService_StopWaitsForCompletion(t *testing.T) {
	taskStarted := make(chan bool, 1)
	taskFinished := make(chan bool, 1)

	service := lifecycle.NewFuncService(func(ctx context.Context) error {
		taskStarted <- true
		// Simulate some work that takes time
		time.Sleep(50 * time.Millisecond)
		taskFinished <- true
		return nil
	})

	ctx := context.Background()

	// Start the service
	require.NoError(t, service.Start(ctx))

	// Wait for task to start
	<-taskStarted

	// Stop should wait for completion
	stopCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	require.NoError(t, service.Stop(stopCtx))

	// Task should have finished by now
	select {
	case <-taskFinished:
		// Good - task finished
	default:
		t.Fatal("Task should have finished after Stop() returns")
	}
}
