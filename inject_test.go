package inject

import (
	"context"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
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
		err := injector.Provide("testNotFunc")
		require.ErrorIs(t, err, ErrInvalidProvider)
	}
	{
		injector := New()
		err := injector.Provide(func() {})
		require.ErrorIs(t, err, ErrInvalidProvider)
	}
	{
		injector := New()
		err := injector.Provide(func() error { return nil })
		require.ErrorIs(t, err, ErrInvalidProvider)
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

	// Test transactional behavior: if one constructor fails, all should be rolled back
	{
		injector := New()
		originalCount := len(injector.providers)

		err := injector.Provide(
			func() string { return "test1" },
			func() int { return 42 },
			func() string { return "test2" }, // This should conflict with the first one
		)
		require.ErrorIs(t, err, ErrTypeAlreadyProvided)
		// Should have same number of providers as before (rollback)
		require.Equal(t, originalCount, len(injector.providers))
	}

	// Test transactional behavior with nested arrays
	{
		injector := New()
		originalCount := len(injector.providers)

		httpGroup := []any{
			func() string { return "http-config" },
			func() int { return 8080 },
		}

		err := injector.Provide(
			func() bool { return true },
			httpGroup,
			func() string { return "conflict" }, // This should conflict with the string in httpGroup
		)
		require.ErrorIs(t, err, ErrTypeAlreadyProvided)
		// Should have same number of providers as before (rollback)
		require.Equal(t, originalCount, len(injector.providers))
	}
}

func TestInvoke(t *testing.T) {
	injector := New()
	err := injector.Provide(func() string { return "test" })
	require.NoError(t, err)

	_, err = injector.Invoke("testNotFunc")
	require.ErrorIs(t, err, ErrInvalidInvokeTarget)

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

	// Test invalid argument types for Resolve
	{
		injector := New()
		err := injector.Provide(func() string { return "test" })
		require.NoError(t, err)

		// Test non-pointer argument
		var str string
		err = injector.ResolveContext(context.Background(), str)
		require.Error(t, err)
		require.Contains(t, err.Error(), "resolve requires pointer arguments")

		// Test nil argument
		err = injector.ResolveContext(context.Background(), nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "resolve requires pointer arguments")
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

	err = injector.Apply("testNotStruct")
	require.ErrorIs(t, err, ErrInvalidApplyTarget)
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
	require.Contains(t, err.Error(), "dependency path: int")
}

func TestDependencyPathDisplay(t *testing.T) {
	t.Run("nested dependency path with struct field injection", func(t *testing.T) {
		injector := New()

		type Database struct {
			DSN string `inject:""`
		}

		type Repository struct {
			DB *Database `inject:""`
		}

		type Service struct {
			Repo *Repository `inject:""`
		}

		err := injector.Provide(
			func() *Service { return &Service{} },
			func() *Repository { return &Repository{} },
			func() *Database { return &Database{} },
		)
		require.NoError(t, err)

		var service *Service
		err = injector.Resolve(&service)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrTypeNotProvided)
		require.Equal(t, "dependency path: *inject.Service -> *inject.Repository -> *inject.Database -> string: type not provided", err.Error())
	})

	t.Run("constructor parameter dependency path", func(t *testing.T) {
		injector := New()

		type Logger struct{}
		type Config struct{}
		type Service struct{}

		err := injector.Provide(
			func(logger *Logger, config *Config) *Service {
				return &Service{}
			},
		)
		require.NoError(t, err)

		var service *Service
		err = injector.Resolve(&service)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrTypeNotProvided)
		require.Equal(t, "dependency path: *inject.Service -> *inject.Logger: type not provided", err.Error())
	})

	t.Run("dependency resolution within provider", func(t *testing.T) {
		injector := New()

		type Database struct {
			DSN string `inject:""`
		}

		type Service struct {
			DB *Database `inject:""`
		}

		err := injector.Provide(
			func() *Service { return &Service{} },
			func() (*Database, error) {
				// When Resolve is called within a provider, it uses background context
				// So the dependency path will only show the directly resolved type
				var dsn string
				if err := injector.Resolve(&dsn); err != nil {
					return nil, err
				}
				return &Database{DSN: dsn}, nil
			},
		)
		require.NoError(t, err)

		var service *Service
		err = injector.Resolve(&service)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrTypeNotProvided)
		require.Equal(t, "dependency path: string: type not provided", err.Error())
	})

	t.Run("dependency resolution with ResolveContext within provider", func(t *testing.T) {
		injector := New()

		type Config struct {
			Value string
		}

		type Database struct {
			DSN string
		}

		type Service struct {
			DB *Database `inject:""`
		}

		err := injector.Provide(
			func() *Service { return &Service{} },
			func(ctx context.Context) (*Database, error) {
				// Dynamically resolve Config at runtime within provider
				// The dependency on Config is decided at runtime, not declared via inject tag
				// When ResolveContext is called with the passed context, the dependency path is preserved
				var config *Config
				if err := injector.ResolveContext(ctx, &config); err != nil {
					return nil, err
				}
				return &Database{DSN: config.Value}, nil
			},
		)
		require.NoError(t, err)

		var service *Service
		err = injector.Resolve(&service)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrTypeNotProvided)
		require.Equal(t, "dependency path: *inject.Service -> *inject.Database -> *inject.Config: type not provided", err.Error())
	})

	t.Run("multi-level injector chain avoids duplicate types in path", func(t *testing.T) {
		parent := New()
		parent.SetParent(New())

		child := New()
		child.SetParent(parent)

		type Service struct {
			Name string `inject:""`
		}

		err := child.Provide(func() *Service { return &Service{} })
		require.NoError(t, err)

		var service *Service
		err = child.Resolve(&service)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrTypeNotProvided)
		require.Equal(t, "dependency path: *inject.Service -> string: type not provided", err.Error())
	})
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
		require.NoError(t, err)
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
		require.NoError(t, err)
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

func TestResolveContext(t *testing.T) {
	// Test basic functionality and context-aware constructors
	{
		injector := New()
		type contextKey string
		const key contextKey = "testKey"

		err := injector.Provide(func(ctx context.Context) string {
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
		err = injector.ResolveContext(ctx, &str)
		require.NoError(t, err)
		require.Equal(t, "custom-value", str)
	}

	// Test with background context using new injector
	{
		injector := New()
		err := injector.Provide(func(ctx context.Context) string {
			require.NotNil(t, ctx)
			return "context-aware"
		})
		require.NoError(t, err)

		var str string
		err = injector.ResolveContext(context.Background(), &str)
		require.NoError(t, err)
		require.Equal(t, "context-aware", str)
	}

	// Test context timeout and error handling
	{
		injector := New()
		testErr := errors.New("test error")

		err := injector.Provide(func(ctx context.Context) (string, error) {
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
		err = injector.ResolveContext(ctx, &str)
		require.ErrorIs(t, err, testErr)
	}

	// Test multiple values and nested dependencies
	{
		injector := New()
		err := injector.Provide(func(ctx context.Context) string {
			return "dependency"
		})
		require.NoError(t, err)
		err = injector.Provide(func(ctx context.Context, dep string) int {
			require.Equal(t, "dependency", dep)
			return 42
		})
		require.NoError(t, err)

		var num int
		var str string
		err = injector.ResolveContext(context.Background(), &num, &str)
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
		// Test ResolveContext
		err = child.ResolveContext(context.Background(), &str2)
		require.NoError(t, err)
		require.Equal(t, "from-parent", str1)
		require.Equal(t, "from-parent", str2)
	}

	// Test Context passed to function requiring context.Context
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

		err := injector.Provide(func(ctx context.Context) string {
			// Pass Context to function expecting context.Context
			return processWithContext(ctx)
		})
		require.NoError(t, err)

		ctx := context.WithValue(context.Background(), key, "passed-through")
		var str string
		err = injector.ResolveContext(ctx, &str)
		require.NoError(t, err)
		require.Equal(t, "passed-through", str)
	}

	// Test what happens if Context is also provided
	{
		injector := New()

		type ctxKeyProvided struct{}
		type ctxKeyActual struct{}

		// Provide a Context (this should be ignored)
		err := injector.Provide(func() context.Context {
			return context.WithValue(context.Background(), ctxKeyProvided{}, "ignored")
		})
		require.ErrorIs(t, err, ErrTypeNotAllowed)

		err = injector.Provide(func(ctx context.Context) string {
			// Should get the actual inject context, not the provided one
			if val := ctx.Value(ctxKeyActual{}); val != nil {
				return val.(string)
			}
			return "no-actual-value"
		})
		require.NoError(t, err)

		actualCtx := context.WithValue(context.Background(), ctxKeyActual{}, "from-resolve")
		var str string
		err = injector.ResolveContext(actualCtx, &str)
		require.NoError(t, err)
		require.Equal(t, "from-resolve", str)
	}
}

func TestApplyContext(t *testing.T) {
	type TestStruct struct {
		Value string `inject:""`
		Num   int    `inject:""`
	}

	// Test with context values
	{
		injector := New()
		type ctxKey string
		const key ctxKey = "applyKey"
		const numKey ctxKey = "numKey"

		err := injector.Provide(func(ctx context.Context) string {
			if val := ctx.Value(key); val != nil {
				return val.(string)
			}
			return "default-value"
		})
		require.NoError(t, err)
		err = injector.Provide(func(ctx context.Context) int {
			if val := ctx.Value(numKey); val != nil {
				return val.(int)
			}
			return 0
		})
		require.NoError(t, err)

		ctx := context.WithValue(context.Background(), key, "context-value")
		ctx = context.WithValue(ctx, numKey, 42)

		testStruct := &TestStruct{}
		err = injector.ApplyContext(ctx, testStruct)
		require.NoError(t, err)
		require.Equal(t, "context-value", testStruct.Value)
		require.Equal(t, 42, testStruct.Num)
	}

	// Test compatibility with regular Apply method using separate injector
	{
		injector := New()
		type contextKey string
		const key contextKey = "applyKey"

		err := injector.Provide(func(ctx context.Context) string {
			if val := ctx.Value(key); val != nil {
				return val.(string)
			}
			return "default-value"
		})
		require.NoError(t, err)
		err = injector.Provide(func(ctx context.Context) int {
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

func TestInvokeContext(t *testing.T) {
	// Test with context values
	{
		injector := New()
		type contextKey string
		const key contextKey = "invokeKey"

		err := injector.Provide(func(ctx context.Context) string {
			if val := ctx.Value(key); val != nil {
				return val.(string)
			}
			return "default-invoke"
		})
		require.NoError(t, err)

		ctx := context.WithValue(context.Background(), key, "invoke-value")
		results, err := injector.InvokeContext(ctx, func(s string) (string, int) {
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

		err := injector.Provide(func(ctx context.Context) string {
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

		err := injector.Provide(func(ctx context.Context) string {
			if val := ctx.Value(key); val != nil {
				return val.(string)
			}
			return "default-invoke"
		})
		require.NoError(t, err)

		ctx := context.WithValue(context.Background(), key, "invoke-value")
		results, err := injector.InvokeContext(ctx, func(s string, ctx context.Context) string {
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

func TestTypeNotAllowed(t *testing.T) {
	injector := New()

	// Test that providing context.Context is not allowed
	err := injector.Provide(func() context.Context {
		return context.Background()
	})
	require.ErrorIs(t, err, ErrTypeNotAllowed)

	// Test that providing context.Context with other types is also not allowed
	err = injector.Provide(func() (string, context.Context) {
		return "test", context.Background()
	})
	require.ErrorIs(t, err, ErrTypeNotAllowed)

	// Test that normal context.Context usage in constructors still works
	err = injector.Provide(func(ctx context.Context) string {
		return "works"
	})
	require.NoError(t, err)

	var result string
	err = injector.ResolveContext(context.Background(), &result)
	require.NoError(t, err)
	require.Equal(t, "works", result)

	// Test that error type in Invoke function parameters is not allowed
	{
		injector2 := New()
		_, err := injector2.Invoke(func(err error) string {
			return "should not work"
		})
		require.ErrorIs(t, err, ErrTypeNotAllowed)
	}

	// Test that context.Context type in Invoke function parameters is not allowed
	{
		injector3 := New()
		_, err := injector3.Invoke(func(ctx context.Context, err error) string {
			return "should not work"
		})
		require.ErrorIs(t, err, ErrTypeNotAllowed)
	}

	// Test that error type in struct field with inject tag is not allowed
	{
		injector4 := New()
		type StructWithErrorField struct {
			Err error `inject:""`
		}
		structWithError := &StructWithErrorField{}
		err := injector4.Apply(structWithError)
		require.ErrorIs(t, err, ErrTypeNotAllowed)
	}

	// Test that Context type in struct field with inject tag is not allowed
	{
		injector5 := New()
		type StructWithContextField struct {
			Ctx context.Context `inject:""`
		}
		structWithContext := &StructWithContextField{}
		err := injector5.Apply(structWithContext)
		require.ErrorIs(t, err, ErrTypeNotAllowed)
	}

	// Test that struct fields without inject tags are now allowed to have error/Context types
	{
		injector6 := New()
		type StructWithoutInjectTags struct {
			Err error           // No inject tag, should be allowed
			Ctx context.Context // No inject tag, should be allowed
			Val string          `inject:""`
		}
		err := injector6.Provide(func() string { return "test-value" })
		require.NoError(t, err)

		structWithoutTags := &StructWithoutInjectTags{}
		err = injector6.Apply(structWithoutTags)
		require.NoError(t, err)
		require.Equal(t, "test-value", structWithoutTags.Val)
		// Error and Context fields should remain zero values since they don't have inject tags
		require.Nil(t, structWithoutTags.Err)
		require.Nil(t, structWithoutTags.Ctx)

		err = injector6.Provide(func() *StructWithoutInjectTags {
			return &StructWithoutInjectTags{
				Val: "test-value",
			}
		})
		require.NoError(t, err)

		structWithoutTags = nil
		err = injector6.Resolve(&structWithoutTags)
		require.NoError(t, err)
		require.Equal(t, "test-value", structWithoutTags.Val)
		require.Nil(t, structWithoutTags.Err)
		require.Nil(t, structWithoutTags.Ctx)
	}
}

func TestProvideArrayFlattening(t *testing.T) {
	inj := New()

	// Group related HTTP constructors
	httpGroup := []any{
		func() string { return "http-config" },
		func() int { return 8080 },
	}

	// Group related DB constructors
	dbGroup := []any{
		func() bool { return true },
		func() float64 { return 3.14 },
	}

	// Provide with mixed individual and grouped constructors
	err := inj.Provide(
		func() byte { return 42 },  // Individual constructor
		httpGroup,                  // Array of constructors
		dbGroup,                    // Another array
		func() rune { return 'A' }, // Another individual constructor
	)

	require.NoError(t, err)

	// Verify all types were provided correctly
	var str string
	var port int
	var enabled bool
	var pi float64
	var b byte
	var r rune

	err = inj.Resolve(&str, &port, &enabled, &pi, &b, &r)
	require.NoError(t, err)

	require.Equal(t, "http-config", str)
	require.Equal(t, 8080, port)
	require.True(t, enabled)
	require.Equal(t, 3.14, pi)
	require.Equal(t, byte(42), b)
	require.Equal(t, 'A', r)
}

func TestProvideNestedArrayFlattening(t *testing.T) {
	inj := New()

	// Test nested arrays (array containing arrays)
	nestedGroup := []any{
		[]any{
			func() string { return "nested-1" },
			func() int { return 100 },
		},
		[]any{
			func() bool { return false },
			func() float64 { return 2.71 },
		},
	}

	err := inj.Provide(nestedGroup)
	require.NoError(t, err)

	// Verify nested arrays were flattened correctly
	var str string
	var num int
	var flag bool
	var val float64

	err = inj.Resolve(&str, &num, &flag, &val)
	require.NoError(t, err)

	require.Equal(t, "nested-1", str)
	require.Equal(t, 100, num)
	require.False(t, flag)
	require.Equal(t, 2.71, val)
}

func TestProvideEmptyArrayHandling(t *testing.T) {
	inj := New()

	// Test with empty arrays
	err := inj.Provide(
		func() string { return "test" },
		[]any{}, // Empty array should be handled gracefully
		func() int { return 42 },
	)

	require.NoError(t, err)

	var str string
	var num int
	err = inj.Resolve(&str, &num)
	require.NoError(t, err)

	require.Equal(t, "test", str)
	require.Equal(t, 42, num)
}

func TestCircularDependencyDetection(t *testing.T) {
	// Test case: Simple A -> B -> A circular dependency
	{
		injector := New()

		type A struct {
			Value string
		}

		type B struct {
			Value string
		}

		// This creates circular dependency: A constructor depends on *B, B constructor depends on *A
		err := injector.Provide(
			func(b *B) *A { return &A{Value: "A needs B"} },
			func(a *A) *B { return &B{Value: "B needs A"} },
		)

		assert.ErrorIs(t, err, ErrCircularDependency)
		assert.Contains(t, err.Error(), "*inject.B -> *inject.A -> *inject.B")
	}

	// Test case: Three-way circular dependency A -> B -> C -> A
	{
		injector := New()

		type A struct {
			Value string
		}

		type B struct {
			Value string
		}

		type C struct {
			Value string
		}

		// This creates circular dependency: A -> B -> C -> A
		err := injector.Provide(
			func(c *C) *A { return &A{Value: "A needs C"} },
			func(a *A) *B { return &B{Value: "B needs A"} },
			func(b *B) *C { return &C{Value: "C needs B"} },
		)

		assert.ErrorIs(t, err, ErrCircularDependency)
		assert.Contains(t, err.Error(), "*inject.C -> *inject.B -> *inject.A -> *inject.C")
	}
}

func TestCoConstructorCircularDependencyDetection(t *testing.T) {
	// Test case: Reproduce the real circular dependency problem we encountered
	{
		injector := New()

		type Config struct {
			Port int
		}

		type Service struct {
			Config *Config `inject:""`
		}

		// This mimics the original problem: a function that returns both Config and Service
		// where Service has an inject tag for Config - this creates a circular dependency
		err := injector.Provide(func() (*Config, *Service) {
			conf := &Config{Port: 8080}
			svc := &Service{} // Service will be auto-injected with Config
			return conf, svc
		})

		assert.ErrorIs(t, err, ErrCircularDependency)
		assert.Contains(t, err.Error(), "*inject.Config -> *inject.Config@*inject.Service")
	}

	// Test case: Cross-constructor circular dependency detection
	{
		injector := New()

		// Define all types in the same scope
		type (
			ConfigA struct {
				Port int
			}

			ProxyA struct {
				Config *ConfigA `inject:""`
			}

			ServiceA struct {
				Proxy *ProxyA `inject:""`
			}
		)

		// *inject.ProxyA@*inject.ServiceA ->
		// *inject.ConfigA@*inject.ProxyA ->
		// *inject.ProxyA@*inject.ServiceA (through same constructor)
		err := injector.Provide(
			func() *ProxyA {
				return &ProxyA{}
			},
			func() (*ServiceA, *ConfigA) {
				return &ServiceA{}, &ConfigA{Port: 8080}
			})

		assert.ErrorIs(t, err, ErrCircularDependency)
		assert.Contains(t, err.Error(), "*inject.ProxyA@*inject.ServiceA -> *inject.ConfigA@*inject.ProxyA -> *inject.ProxyA@*inject.ServiceA")
	}

	// Test case: Complex co-constructor circular dependency
	// A -> C -> B, where A and B are co-constructors
	{
		injector := New()

		type (
			B struct {
				// B doesn't directly depend on anything
			}

			C struct {
				B *B `inject:""`
			}

			A struct {
				C *C `inject:""`
			}
		)

		err := injector.Provide(
			// Cycle: A -> C@A -> B@C -> C@A (A and B are co-constructors)
			func() (*A, *B) { // A and B are co-constructors
				return &A{}, &B{}
			},
			func() *C {
				return &C{}
			},
		)
		assert.ErrorIs(t, err, ErrCircularDependency)
		assert.Contains(t, err.Error(), "*inject.C@*inject.A -> *inject.B@*inject.C -> *inject.C@*inject.A")
	}
}

func TestErrorTypePositionValidation(t *testing.T) {
	{
		// Test error in middle position (invalid)
		injector := New()
		err := injector.Provide(func() (string, error, int) { return "", nil, 0 })
		require.ErrorIs(t, err, ErrErrorTypeMustBeLast)
	}

	{
		// Test error as last return value (valid)
		injector := New()
		err := injector.Provide(func() (string, int, error) { return "", 0, nil })
		require.NoError(t, err)
	}

	{
		// Test multiple errors (invalid)
		injector := New()
		err := injector.Provide(func() (error, string, error) { return nil, "", nil })
		require.ErrorIs(t, err, ErrErrorTypeMustBeLast)
	}

	{
		// Test only error return (invalid)
		injector := New()
		err := injector.Provide(func() error { return nil })
		require.ErrorIs(t, err, ErrInvalidProvider)
	}
}

func TestBuild(t *testing.T) {
	t.Run("basic functionality", func(t *testing.T) {
		injector := New()

		// Provide multiple types
		err := injector.Provide(
			func() string { return "test" },
			func() int { return 42 },
			func() bool { return true },
		)
		require.NoError(t, err)

		// Build should trigger all constructors
		err = injector.Build()
		require.NoError(t, err)

		// Verify all types are now resolved as values (including *Injector)
		require.Len(t, injector.providers, 0) // All providers should be resolved
		require.Len(t, injector.values, 4)    // string, int, bool, *Injector
	})

	t.Run("stable order", func(t *testing.T) {
		// Test multiple times to ensure consistent ordering
		for i := 0; i < 5; i++ {
			injector := New()

			var resolved []string
			err := injector.Provide(
				func() string { resolved = append(resolved, "string"); return "test" },
				func() int { resolved = append(resolved, "int"); return 42 },
				func() bool { resolved = append(resolved, "bool"); return true },
			)
			require.NoError(t, err)

			err = injector.Build()
			require.NoError(t, err)

			// Order should be consistent (by provider registration sequence)
			expected := []string{"string", "int", "bool"}
			require.Equal(t, expected, resolved)
		}
	})

	t.Run("constructor error", func(t *testing.T) {
		injector := New()
		expectedErr := errors.New("constructor failed")

		err := injector.Provide(
			func() string { return "test" },
			func() (int, error) { return 0, expectedErr },
		)
		require.NoError(t, err)

		err = injector.Build()
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("empty injector", func(t *testing.T) {
		injector := New()

		// Should not fail on empty injector (only has *Injector)
		err := injector.Build()
		require.NoError(t, err)
	})
}
