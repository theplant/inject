package inject

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestVoidCtor(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		inj := New()

		executed := false
		err := inj.Provide(func() {
			executed = true
		})
		require.NoError(t, err)

		// Before Resolve, the ctor should not be executed
		require.False(t, executed)

		// Should be able to resolve Slice[*Void]
		var voids Slice[*Void]
		err = inj.Resolve(&voids)
		require.NoError(t, err)
		require.Len(t, voids, 1)
		require.True(t, executed)
	})

	t.Run("error only return", func(t *testing.T) {
		inj := New()

		executed := false
		err := inj.Provide(func() error {
			executed = true
			return nil
		})
		require.NoError(t, err)

		var voids Slice[*Void]
		err = inj.Resolve(&voids)
		require.NoError(t, err)
		require.True(t, executed)
	})

	t.Run("error propagation", func(t *testing.T) {
		inj := New()

		expectedErr := errors.New("void ctor failed")
		err := inj.Provide(func() error {
			return expectedErr
		})
		require.NoError(t, err)

		var voids Slice[*Void]
		err = inj.Resolve(&voids)
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("multiple void ctors", func(t *testing.T) {
		inj := New()

		var order []int
		err := inj.Provide(
			func() { order = append(order, 1) },
			func() { order = append(order, 2) },
			func() { order = append(order, 3) },
		)
		require.NoError(t, err)

		var voids Slice[*Void]
		err = inj.Resolve(&voids)
		require.NoError(t, err)
		require.Len(t, voids, 3)
		require.Equal(t, []int{1, 2, 3}, order)
	})

	t.Run("only executed once", func(t *testing.T) {
		inj := New()

		callCount := 0
		err := inj.Provide(func() {
			callCount++
		})
		require.NoError(t, err)

		// Resolve twice
		var voids1, voids2 Slice[*Void]
		err = inj.Resolve(&voids1)
		require.NoError(t, err)
		err = inj.Resolve(&voids2)
		require.NoError(t, err)
		require.Equal(t, 1, callCount)
	})

	t.Run("with context", func(t *testing.T) {
		inj := New()

		type ctxKey string
		const key ctxKey = "value"

		var capturedValue string
		err := inj.Provide(func(ctx context.Context) {
			capturedValue = ctx.Value(key).(string)
		})
		require.NoError(t, err)

		ctx := context.WithValue(context.Background(), key, "from-context")
		var voids Slice[*Void]
		err = inj.ResolveContext(ctx, &voids)
		require.NoError(t, err)
		require.Equal(t, "from-context", capturedValue)
	})

	t.Run("not executed if only other types resolved", func(t *testing.T) {
		inj := New()

		voidExecuted := false
		err := inj.Provide(
			func() string { return "test" },
			func() { voidExecuted = true },
		)
		require.NoError(t, err)

		var str string
		err = inj.Resolve(&str)
		require.NoError(t, err)
		require.Equal(t, "test", str)
		require.False(t, voidExecuted)
	})

	t.Run("executed via Build", func(t *testing.T) {
		inj := New()

		executed := false
		err := inj.Provide(func() {
			executed = true
		})
		require.NoError(t, err)
		require.False(t, executed)

		err = inj.Build()
		require.NoError(t, err)
		require.True(t, executed)
	})

	t.Run("modifies dependency", func(t *testing.T) {
		inj := New()

		type Config struct {
			Debug bool
		}

		configCreated := false
		voidExecuted := false

		err := inj.Provide(
			func() *Config {
				configCreated = true
				return &Config{Debug: false}
			},
			func(cfg *Config) {
				voidExecuted = true
				cfg.Debug = true
			},
		)
		require.NoError(t, err)

		// Resolve only Config - void ctor should not run
		var cfg *Config
		err = inj.Resolve(&cfg)
		require.NoError(t, err)
		require.True(t, configCreated)
		require.False(t, voidExecuted)
		require.False(t, cfg.Debug)

		// Resolve Slice[*Void] - void ctor runs and modifies config
		var voids Slice[*Void]
		err = inj.Resolve(&voids)
		require.NoError(t, err)
		require.True(t, voidExecuted)
		require.True(t, cfg.Debug)
	})
}
