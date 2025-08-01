package inject

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenericResolve(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		injector := New()
		err := injector.Provide(func() string { return "test-value" })
		require.NoError(t, err)

		result, err := Resolve[string](injector)
		require.NoError(t, err)
		require.Equal(t, "test-value", result)
	})

	t.Run("error", func(t *testing.T) {
		injector := New()

		result, err := Resolve[string](injector)
		require.Error(t, err)
		require.Equal(t, "", result)
		require.ErrorIs(t, err, ErrTypeNotProvided)
	})
}

func TestGenericMustResolve(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		injector := New()
		err := injector.Provide(func() int { return 42 })
		require.NoError(t, err)

		result := MustResolve[int](injector)
		require.Equal(t, 42, result)
	})

	t.Run("panic on error", func(t *testing.T) {
		injector := New()

		require.Panics(t, func() {
			MustResolve[string](injector)
		})
	})
}

func TestGenericResolveContext(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		injector := New()
		err := injector.Provide(func(ctx context.Context) string {
			return "context-value"
		})
		require.NoError(t, err)

		result, err := ResolveContext[string](context.Background(), injector)
		require.NoError(t, err)
		require.Equal(t, "context-value", result)
	})

	t.Run("error", func(t *testing.T) {
		injector := New()

		result, err := ResolveContext[bool](context.Background(), injector)
		require.Error(t, err)
		require.False(t, result)
		require.ErrorIs(t, err, ErrTypeNotProvided)
	})
}

func TestGenericMustResolveContext(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		injector := New()
		err := injector.Provide(func(ctx context.Context) float64 {
			return 3.14
		})
		require.NoError(t, err)

		result := MustResolveContext[float64](context.Background(), injector)
		require.Equal(t, 3.14, result)
	})

	t.Run("panic on error", func(t *testing.T) {
		injector := New()

		require.Panics(t, func() {
			MustResolveContext[string](context.Background(), injector)
		})
	})
}
