package lifecycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/theplant/inject/lifecycle"
)

// === FuncActor Tests ===

func TestFuncActor_BasicOperation(t *testing.T) {
	var started, stopped int

	actor := lifecycle.NewFuncActor(
		func(ctx context.Context) error {
			started++
			return nil
		},
		func(ctx context.Context) error {
			stopped++
			return nil
		},
	)

	ctx := context.Background()

	// Test basic start/stop cycle
	require.NoError(t, actor.Start(ctx))
	require.Equal(t, 1, started)
	require.Equal(t, 0, stopped)

	require.NoError(t, actor.Stop(ctx))
	require.Equal(t, 1, started)
	require.Equal(t, 1, stopped)

	// Test multiple calls
	require.NoError(t, actor.Start(ctx))
	require.NoError(t, actor.Stop(ctx))
	require.Equal(t, 2, started)
	require.Equal(t, 2, stopped)
}

func TestFuncActor_NilFunctions(t *testing.T) {
	// Test all combinations of nil functions
	testCases := []struct {
		name      string
		startFunc func(ctx context.Context) error
		stopFunc  func(ctx context.Context) error
	}{
		{"both nil", nil, nil},
		{"start nil", nil, func(ctx context.Context) error { return nil }},
		{"stop nil", func(ctx context.Context) error { return nil }, nil},
	}

	ctx := context.Background()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actor := lifecycle.NewFuncActor(tc.startFunc, tc.stopFunc)
			require.NoError(t, actor.Start(ctx))
			require.NoError(t, actor.Stop(ctx))
		})
	}
}

func TestFuncActor_ErrorHandling(t *testing.T) {
	startErr := errors.New("start error")
	stopErr := errors.New("stop error")

	actor := lifecycle.NewFuncActor(
		func(ctx context.Context) error { return startErr },
		func(ctx context.Context) error { return stopErr },
	)

	ctx := context.Background()

	// Test error propagation
	require.Equal(t, startErr, actor.Start(ctx))
	require.Equal(t, stopErr, actor.Stop(ctx))
}

// === FuncService Tests ===

func TestFuncService_BasicOperation(t *testing.T) {
	taskExecuted := make(chan bool, 1)
	stopExecuted := make(chan bool, 1)

	service := lifecycle.NewFuncService(
		func(ctx context.Context) error {
			taskExecuted <- true
			// Simulate some work
			time.Sleep(20 * time.Millisecond)
			return nil
		},
	).WithStop(func(ctx context.Context) error {
		stopExecuted <- true
		return nil
	})

	ctx := context.Background()

	// Start service
	require.NoError(t, service.Start(ctx))

	// Verify task started
	require.True(t, <-taskExecuted)

	// Wait for completion
	select {
	case <-service.Done():
		// Task completed successfully
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Service should complete")
	}

	// Check final state
	require.Nil(t, service.Err())

	// Stop should execute stop function
	require.NoError(t, service.Stop(ctx))
	require.True(t, <-stopExecuted)
}

func TestFuncService_TaskError(t *testing.T) {
	expectedErr := errors.New("task failed")

	service := lifecycle.NewFuncService(
		func(ctx context.Context) error {
			return expectedErr
		},
	)

	ctx := context.Background()
	require.NoError(t, service.Start(ctx))

	// Wait for completion
	<-service.Done()

	// Should have the task error
	require.Equal(t, expectedErr, service.Err())
}

func TestFuncService_StopBehavior(t *testing.T) {
	taskStarted := make(chan bool, 1)
	stopCalled := make(chan bool, 1)

	service := lifecycle.NewFuncService(
		func(ctx context.Context) error {
			taskStarted <- true
			// Wait for permission to finish or cancellation
			<-ctx.Done()
			return errors.WithStack(ctx.Err())
		},
	).WithStop(func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return errors.WithStack(ctx.Err())
		case stopCalled <- true:
			return nil
		}
	})

	ctx := context.Background()
	require.NoError(t, service.Start(ctx))

	// Wait for task to start
	<-taskStarted

	// Done should not be signaled yet
	select {
	case <-service.Done():
		t.Fatal("Done should not be signaled while task is running")
	case <-time.After(10 * time.Millisecond):
		// Good
	}

	// Stop the service
	stopCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	require.NoError(t, service.Stop(stopCtx))

	// Stop function should have been called
	require.True(t, <-stopCalled)

	// Service should be done
	select {
	case <-service.Done():
		// Good
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Service should be done after stop")
	}

	// No error for normal completion
	require.Nil(t, service.Err())
}

func TestFuncService_StopTimeout(t *testing.T) {
	taskStarted := make(chan bool, 1)
	stopCalled := make(chan bool, 1)

	service := lifecycle.NewFuncService(
		func(ctx context.Context) error {
			taskStarted <- true
			// Task responds to context cancellation
			<-ctx.Done()
			return errors.WithStack(ctx.Err())
		},
	).WithStop(func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return errors.WithStack(ctx.Err())
		case <-time.After(100 * time.Millisecond):
			stopCalled <- true
			return nil
		}
	})

	ctx := context.Background()
	require.NoError(t, service.Start(ctx))
	<-taskStarted

	// Stop with short timeout - should timeout waiting for stop function
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := service.Stop(stopCtx)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded))

	// Verify stop function was interrupted (didn't complete)
	select {
	case <-stopCalled:
		t.Fatal("Stop function should not have completed due to timeout")
	case <-time.After(20 * time.Millisecond):
		// Good - stop function was interrupted
	}
}

func TestFuncService_StopFunctionError(t *testing.T) {
	stopErr := errors.New("stop failed")

	service := lifecycle.NewFuncService(
		func(ctx context.Context) error {
			return nil
		},
	).WithStop(func(ctx context.Context) error {
		return stopErr
	})

	ctx := context.Background()
	require.NoError(t, service.Start(ctx))

	// Wait for task completion
	<-service.Done()

	// No error for normal completion
	require.Nil(t, service.Err())

	// Stop should return the stop function error
	err := service.Stop(ctx)
	require.Equal(t, stopErr, err)
}

func TestFuncService_PanicOnNilTaskFunc(t *testing.T) {
	require.Panics(t, func() {
		lifecycle.NewFuncService(nil)
	})
}
