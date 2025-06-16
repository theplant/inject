package inject

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	injector := New()
	require.NotNil(t, injector)
	require.NotNil(t, injector.values)
	require.NotNil(t, injector.providers)
}

func TestSetParent(t *testing.T) {
	injector := New()
	parent := New()
	err := injector.SetParent(parent)
	require.NoError(t, err)
	require.Equal(t, parent, injector.parent)
	err = injector.SetParent(parent)
	require.ErrorIs(t, err, ErrParentAlreadySet)
}

func TestProvide(t *testing.T) {
	{
		injector := New()
		require.Panics(t, func() {
			injector.Provide("testNotFunc")
		})
	}
	{
		injector := New()
		require.Panics(t, func() {
			injector.Provide(func() {})
		})
	}
	{
		injector := New()
		require.Panics(t, func() {
			injector.Provide(func() error { return nil })
		})
	}
	{
		injector := New()
		err := injector.Provide(func() string { return "test" })
		require.NoError(t, err)
	}
	{
		injector := New()
		err := injector.Provide(func() (string, int) { return "test", 0 })
		require.NoError(t, err)
		require.Len(t, injector.providers, 3)

		err = injector.Provide(func() (int64, int) { return 1, 2 })
		require.ErrorIs(t, err, ErrTypeAlreadyProvided)
		require.Len(t, injector.providers, 3)
	}
	{
		injector := New()
		err := injector.Provide(func() (string, error) { return "test", nil })
		require.NoError(t, err)
		require.Len(t, injector.providers, 2)

		_, err = injector.Invoke(func(s string) {})
		require.NoError(t, err)
		require.Len(t, injector.providers, 1)

		err = injector.Provide(func() (int64, string) { return 1, "" })
		require.ErrorIs(t, err, ErrTypeAlreadyProvided)
		require.Len(t, injector.providers, 1)
	}
}

func TestInvoke(t *testing.T) {
	injector := New()
	err := injector.Provide(func() string { return "test" })
	require.NoError(t, err)

	require.Panics(t, func() {
		injector.Invoke("testNotFunc")
	})

	results, err := injector.Invoke(func(s string) string { return s })
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "test", results[0])

	{
		errTemp := errors.New("temp")
		results, err := injector.Invoke(func(s string) (string, error) { return "", errTemp })
		require.ErrorIs(t, err, errTemp)
		require.Len(t, results, 0)
	}
}

func TestResolve(t *testing.T) {
	injector := New()

	{
		err := injector.Provide(func() string { return "test" })
		require.NoError(t, err)
		var str string
		err = injector.Resolve(&str)
		require.NoError(t, err)
		require.Equal(t, "test", str)
	}

	{
		err := injector.Provide(func() *string {
			a := "testPtr"
			return &a
		})
		require.NoError(t, err)
		var str *string
		err = injector.Resolve(&str)
		require.NoError(t, err)
		require.Equal(t, "testPtr", *str)
	}

	{
		injector := New()

		errTemp := errors.New("temp")
		// if error is not nil, the value will be ignored
		err := injector.Provide(func() (string, error) { return "test", errTemp })
		require.NoError(t, err)
		str := "xxx"
		err = injector.Resolve(&str)
		require.ErrorIs(t, err, errTemp)
		require.Equal(t, "xxx", str)
	}

	{
		err := injector.Provide(func() int { return 1 })
		require.NoError(t, err)
		err = injector.Provide(func() int32 { return 2 })
		require.NoError(t, err)
		var a int
		var b int
		var c int32
		err = injector.Resolve(&a, &b, &c)
		require.NoError(t, err)
		require.Equal(t, 1, a)
		require.Equal(t, 1, b)
		require.Equal(t, int32(2), c)
	}
}

func TestApply(t *testing.T) {
	type TestStruct struct {
		*Injector `inject:""`
		Value     string `inject:"" json:"value,omitempty"`
		value     string `inject:""`
		optional0 *int64 `inject:"optional"`
		optional1 uint64 `inject:"optional"`
		ID        string `json:"id,omitempty"`
	}
	injector := New()
	err := injector.Provide(
		func() string { return "test" },
		func() uint64 { return 123 },
	)
	require.NoError(t, err)
	testStruct := &TestStruct{}
	err = injector.Apply(testStruct)
	require.NoError(t, err)
	require.Equal(t, "test", testStruct.Value)
	require.Equal(t, "test", testStruct.value)
	require.Nil(t, testStruct.optional0)
	require.Equal(t, uint64(123), testStruct.optional1)
	require.Equal(t, "", testStruct.ID)
	require.Equal(t, injector, testStruct.Injector)

	require.Panics(t, func() {
		injector.Apply("testNotStruct")
	})
}

func TestMultipleProviders(t *testing.T) {
	injector := New()
	err := injector.Provide(func() string { return "test1" })
	require.NoError(t, err)
	err = injector.Provide(func() string { return "test2" })
	require.ErrorIs(t, err, ErrTypeAlreadyProvided)
	results, err := injector.Invoke(func(s1, s2 string) string { return s1 + s2 })
	require.NoError(t, err)
	require.Equal(t, "test1test1", results[0])
}

func TestUnresolvedDependency(t *testing.T) {
	injector := New()
	err := injector.Provide(func() string { return "test" })
	require.NoError(t, err)
	_, err = injector.Invoke(func(s string, i int) string { return s })
	require.ErrorIs(t, err, ErrTypeNotProvided)
}

func TestParentInjection(t *testing.T) {
	parent := New()
	err := parent.Provide(func() string { return "test" })
	require.NoError(t, err)
	child := New()
	err = child.SetParent(parent)
	require.NoError(t, err)
	results, err := child.Invoke(func(s string) string { return s })
	require.NoError(t, err)
	require.Equal(t, "test", results[0])

	// override
	err = child.Provide(func() string { return "test2" })
	require.NoError(t, err)
	results, err = child.Invoke(func(s string) string { return s })
	require.NoError(t, err)
	require.Equal(t, "test2", results[0])
}

type TestInterface interface {
	Test() string
}

type TestStruct struct {
	Name string
}

func (t *TestStruct) Test() string {
	return t.Name
}

func TestInterfaceType(t *testing.T) {
	injector := New()

	err := injector.Provide(func() TestInterface { return &TestStruct{Name: "hello"} })
	require.NoError(t, err)
	var testIface TestInterface
	err = injector.Resolve(&testIface)
	require.NoError(t, err)
	require.NotNil(t, testIface)
	require.Equal(t, "hello", testIface.Test())

	{
		type TestInterfaceDummy TestInterface
		var testIfaceDummy TestInterfaceDummy
		err = injector.Resolve(&testIfaceDummy)
		require.ErrorIs(t, err, ErrTypeNotProvided)
		require.Nil(t, testIfaceDummy)

		err = injector.Provide(func() TestInterfaceDummy { return &TestStruct{Name: "hello"} })
		require.NoError(t, err)

		err = injector.Resolve(&testIfaceDummy)
		require.NotNil(t, testIfaceDummy)
		require.Equal(t, "hello", testIfaceDummy.Test())
	}

	{
		type TestInterfaceDummy interface {
			TestInterface
		}
		var testIfaceDummy TestInterfaceDummy
		err = injector.Resolve(&testIfaceDummy)
		require.ErrorIs(t, err, ErrTypeNotProvided)
		require.Nil(t, testIfaceDummy)

		err = injector.Provide(func() TestInterfaceDummy { return &TestStruct{Name: "hello"} })
		require.NoError(t, err)

		err = injector.Resolve(&testIfaceDummy)
		require.NotNil(t, testIfaceDummy)
		require.Equal(t, "hello", testIfaceDummy.Test())
	}

	type Visibility string
	err = injector.Provide(func() Visibility { return "public" })
	require.NoError(t, err)
	var visibility Visibility
	err = injector.Resolve(&visibility)
	require.NoError(t, err)
	require.Equal(t, Visibility("public"), visibility)

	type StructToApply struct {
		iface      TestInterface `inject:""`
		Visibility Visibility    `inject:""`
		str        string        `inject:""`
		ID         string        `json:"id,omitempty"`
	}
	err = injector.Provide(func() string { return "str" })
	require.NoError(t, err)

	structToApply := &StructToApply{}
	err = injector.Apply(structToApply)
	require.NoError(t, err)
	require.Equal(t, "hello", structToApply.iface.Test())
	require.Equal(t, Visibility("public"), structToApply.Visibility)
	require.Equal(t, "str", structToApply.str)
	require.Equal(t, "", structToApply.ID)
}

func TestAutoApply(t *testing.T) {
	type TestStruct struct {
		Injector *Injector `inject:""`
		Value    string    `inject:"" json:"value,omitempty"`
		value    string    `inject:""`
		ID       string    `json:"id,omitempty"`
	}
	injector := New()
	err := injector.Provide(
		func() string { return "test" },
	)
	require.NoError(t, err)
	results, err := injector.Invoke(func() *TestStruct {
		return &TestStruct{
			ID: "testID",
		}
	})
	require.NoError(t, err)
	testStruct := results[0].(*TestStruct)
	require.Equal(t, "test", testStruct.Value)
	require.Equal(t, "test", testStruct.value)
	require.Equal(t, "testID", testStruct.ID)
	require.Equal(t, injector, testStruct.Injector)

	{
		err = injector.Provide(func() *TestStruct { return &TestStruct{ID: "testID2"} })
		require.NoError(t, err)

		testStruct := &TestStruct{}
		err := injector.Resolve(&testStruct)
		require.NoError(t, err)
		require.Equal(t, "test", testStruct.Value)
		require.Equal(t, "test", testStruct.value)
		require.Equal(t, "testID2", testStruct.ID)
		require.Equal(t, injector, testStruct.Injector)
	}
}

func TestResolveWithContext(t *testing.T) {
	// Test basic functionality and context-aware constructors
	{
		injector := New()
		type contextKey string
		const key contextKey = "testKey"

		err := injector.Provide(func(ctx Context) string {
			require.NotNil(t, ctx)
			if val := ctx.Value(key); val != nil {
				return val.(string)
			}
			return "default"
		})
		require.NoError(t, err)

		// Test with custom context value
		ctx := context.WithValue(context.Background(), key, "custom-value")
		var str string
		err = injector.ResolveWithContext(ctx, &str)
		require.NoError(t, err)
		require.Equal(t, "custom-value", str)
	}

	// Test with background context using new injector
	{
		injector := New()
		err := injector.Provide(func(ctx Context) string {
			require.NotNil(t, ctx)
			return "context-aware"
		})
		require.NoError(t, err)

		var str string
		err = injector.ResolveWithContext(context.Background(), &str)
		require.NoError(t, err)
		require.Equal(t, "context-aware", str)
	}

	// Test context timeout and error handling
	{
		injector := New()
		testErr := errors.New("test error")

		err := injector.Provide(func(ctx Context) (string, error) {
			select {
			case <-time.After(100 * time.Millisecond):
				return "completed", nil
			case <-ctx.Done():
				return "", testErr
			}
		})
		require.NoError(t, err)

		// Test with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		var str string
		err = injector.ResolveWithContext(ctx, &str)
		require.ErrorIs(t, err, testErr)
	}

	// Test multiple values and nested dependencies
	{
		injector := New()
		err := injector.Provide(func(ctx Context) string {
			return "dependency"
		})
		require.NoError(t, err)
		err = injector.Provide(func(ctx Context, dep string) int {
			require.Equal(t, "dependency", dep)
			return 42
		})
		require.NoError(t, err)

		var num int
		var str string
		err = injector.ResolveWithContext(context.Background(), &num, &str)
		require.NoError(t, err)
		require.Equal(t, 42, num)
		require.Equal(t, "dependency", str)
	}

	// Test compatibility and parent injector
	{
		parent := New()
		err := parent.Provide(func() string { return "from-parent" })
		require.NoError(t, err)

		child := New()
		err = child.SetParent(parent)
		require.NoError(t, err)

		var str1, str2 string
		// Test compatibility with regular Resolve
		err = child.Resolve(&str1)
		require.NoError(t, err)
		// Test ResolveWithContext
		err = child.ResolveWithContext(context.Background(), &str2)
		require.NoError(t, err)
		require.Equal(t, "from-parent", str1)
		require.Equal(t, "from-parent", str2)
	}

	// Test InjectContext passed to function requiring context.Context
	{
		injector := New()
		type contextKey string
		const key contextKey = "passedKey"

		// Helper function that takes regular context.Context
		processWithContext := func(ctx context.Context) string {
			if val := ctx.Value(key); val != nil {
				return val.(string)
			}
			return "no-value"
		}

		err := injector.Provide(func(ctx Context) string {
			// Pass InjectContext to function expecting context.Context
			return processWithContext(ctx)
		})
		require.NoError(t, err)

		ctx := context.WithValue(context.Background(), key, "passed-through")
		var str string
		err = injector.ResolveWithContext(ctx, &str)
		require.NoError(t, err)
		require.Equal(t, "passed-through", str)
	}

	// Test what happens if InjectContext is also provided
	{
		injector := New()

		// Provide a InjectContext (this should be ignored)
		err := injector.Provide(func() Context {
			return context.WithValue(context.Background(), "provided", "ignored")
		})
		require.ErrorIs(t, err, ErrContextNotAllowed)

		err = injector.Provide(func(ctx Context) string {
			// Should get the actual inject context, not the provided one
			if val := ctx.Value("actual"); val != nil {
				return val.(string)
			}
			return "no-actual-value"
		})
		require.NoError(t, err)

		actualCtx := context.WithValue(context.Background(), "actual", "from-resolve")
		var str string
		err = injector.ResolveWithContext(actualCtx, &str)
		require.NoError(t, err)
		require.Equal(t, "from-resolve", str)
	}
}

func TestApplyWithContext(t *testing.T) {
	type TestStruct struct {
		Value string `inject:""`
		Num   int    `inject:""`
	}

	// Test with context values
	{
		injector := New()
		type contextKey string
		const key contextKey = "applyKey"

		err := injector.Provide(func(ctx Context) string {
			if val := ctx.Value(key); val != nil {
				return val.(string)
			}
			return "default-value"
		})
		require.NoError(t, err)
		err = injector.Provide(func(ctx Context) int {
			if val := ctx.Value("numKey"); val != nil {
				return val.(int)
			}
			return 0
		})
		require.NoError(t, err)

		ctx := context.WithValue(context.Background(), key, "context-value")
		ctx = context.WithValue(ctx, "numKey", 42)

		testStruct := &TestStruct{}
		err = injector.ApplyWithContext(ctx, testStruct)
		require.NoError(t, err)
		require.Equal(t, "context-value", testStruct.Value)
		require.Equal(t, 42, testStruct.Num)
	}

	// Test compatibility with regular Apply method using separate injector
	{
		injector := New()
		type contextKey string
		const key contextKey = "applyKey"

		err := injector.Provide(func(ctx Context) string {
			if val := ctx.Value(key); val != nil {
				return val.(string)
			}
			return "default-value"
		})
		require.NoError(t, err)
		err = injector.Provide(func(ctx Context) int {
			if val := ctx.Value("numKey"); val != nil {
				return val.(int)
			}
			return 0
		})
		require.NoError(t, err)

		testStruct := &TestStruct{}
		err = injector.Apply(testStruct)
		require.NoError(t, err)
		require.Equal(t, "default-value", testStruct.Value)
		require.Equal(t, 0, testStruct.Num)
	}
}

func TestInvokeWithContext(t *testing.T) {
	// Test with context values
	{
		injector := New()
		type contextKey string
		const key contextKey = "invokeKey"

		err := injector.Provide(func(ctx Context) string {
			if val := ctx.Value(key); val != nil {
				return val.(string)
			}
			return "default-invoke"
		})
		require.NoError(t, err)

		ctx := context.WithValue(context.Background(), key, "invoke-value")
		results, err := injector.InvokeWithContext(ctx, func(s string) (string, int) {
			return s + "-processed", len(s)
		})
		require.NoError(t, err)
		require.Len(t, results, 2)
		require.Equal(t, "invoke-value-processed", results[0])
		require.Equal(t, 12, results[1])
	}

	// Test compatibility with regular Invoke method using separate injector
	{
		injector := New()
		type contextKey string
		const key contextKey = "invokeKey"

		err := injector.Provide(func(ctx Context) string {
			if val := ctx.Value(key); val != nil {
				return val.(string)
			}
			return "default-invoke"
		})
		require.NoError(t, err)

		results, err := injector.Invoke(func(s string) string {
			return s + "-regular"
		})
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, "default-invoke-regular", results[0])
	}

	// Test context passed to function parameter
	{
		injector := New()
		type contextKey string
		const key contextKey = "invokeKey"

		err := injector.Provide(func(ctx Context) string {
			if val := ctx.Value(key); val != nil {
				return val.(string)
			}
			return "default-invoke"
		})
		require.NoError(t, err)

		ctx := context.WithValue(context.Background(), key, "invoke-value")
		results, err := injector.InvokeWithContext(ctx, func(s string, ctx Context) string {
			if val := ctx.Value(key); val != nil {
				return s + "-" + val.(string)
			}
			return s + "-no-context"
		})
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, "invoke-value-invoke-value", results[0])
	}
}

func TestContextNotAllowed(t *testing.T) {
	injector := New()

	// Test that providing inject.Context is not allowed
	err := injector.Provide(func() Context {
		return context.Background()
	})
	require.ErrorIs(t, err, ErrContextNotAllowed)

	// Test that providing inject.Context with other types is also not allowed
	err = injector.Provide(func() (string, Context) {
		return "test", context.Background()
	})
	require.ErrorIs(t, err, ErrContextNotAllowed)

	// Test that normal context.Context usage in constructors still works
	err = injector.Provide(func(ctx Context) string {
		return "works"
	})
	require.NoError(t, err)

	var result string
	err = injector.ResolveWithContext(context.Background(), &result)
	require.NoError(t, err)
	require.Equal(t, "works", result)
}
