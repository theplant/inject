package inject

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestElementBasic(t *testing.T) {
	inj := New()

	// Provide multiple Element[string]
	err := inj.Provide(
		func() Element[string] { return Element[string]{Value: "hello"} },
		func() Element[string] { return Element[string]{Value: "world"} },
	)
	require.NoError(t, err)

	// Resolve as Slice[string]
	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs.Values, 2)
	require.Contains(t, strs.Values, "hello")
	require.Contains(t, strs.Values, "world")
}

type testHandler interface {
	Handle() string
}

type testHandlerA struct{}

func (h testHandlerA) Handle() string { return "A" }

type testHandlerB struct{}

func (h testHandlerB) Handle() string { return "B" }

func TestElementWithInterface(t *testing.T) {
	inj := New()

	// Provide multiple Element[testHandler]
	err := inj.Provide(
		func() Element[testHandler] { return Element[testHandler]{Value: testHandlerA{}} },
		func() Element[testHandler] { return Element[testHandler]{Value: testHandlerB{}} },
	)
	require.NoError(t, err)

	// Resolve as Slice[testHandler]
	var handlers Slice[testHandler]
	err = inj.Resolve(&handlers)
	require.NoError(t, err)
	require.Len(t, handlers.Values, 2)

	// Verify both handlers are present
	results := make(map[string]bool)
	for _, h := range handlers.Values {
		results[h.Handle()] = true
	}
	require.True(t, results["A"])
	require.True(t, results["B"])
}

func TestElementWithPointer(t *testing.T) {
	inj := New()

	type Service struct {
		Name string
	}

	// Provide multiple Element[*Service]
	err := inj.Provide(
		func() Element[*Service] { return Element[*Service]{Value: &Service{Name: "svc1"}} },
		func() Element[*Service] { return Element[*Service]{Value: &Service{Name: "svc2"}} },
	)
	require.NoError(t, err)

	// Resolve as Slice[*Service]
	var services Slice[*Service]
	err = inj.Resolve(&services)
	require.NoError(t, err)
	require.Len(t, services.Values, 2)

	names := make(map[string]bool)
	for _, s := range services.Values {
		names[s.Name] = true
	}
	require.True(t, names["svc1"])
	require.True(t, names["svc2"])
}

func TestElementWithDependencies(t *testing.T) {
	inj := New()

	type Config struct {
		Prefix string
	}

	// Provide Config
	err := inj.Provide(func() *Config { return &Config{Prefix: "test-"} })
	require.NoError(t, err)

	// Provide Element[string] that depends on Config
	err = inj.Provide(
		func(cfg *Config) Element[string] { return Element[string]{Value: cfg.Prefix + "one"} },
		func(cfg *Config) Element[string] { return Element[string]{Value: cfg.Prefix + "two"} },
	)
	require.NoError(t, err)

	// Resolve as Slice[string]
	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs.Values, 2)
	require.Contains(t, strs.Values, "test-one")
	require.Contains(t, strs.Values, "test-two")
}

func TestElementWithParentInjector(t *testing.T) {
	parent := New()
	child := New()
	err := child.SetParent(parent)
	require.NoError(t, err)

	// Provide Element[string] in parent
	err = parent.Provide(
		func() Element[string] { return Element[string]{Value: "from-parent"} },
	)
	require.NoError(t, err)

	// Provide Element[string] in child
	err = child.Provide(
		func() Element[string] { return Element[string]{Value: "from-child"} },
	)
	require.NoError(t, err)

	// Resolve from child should get both
	var strs Slice[string]
	err = child.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs.Values, 2)
	require.Contains(t, strs.Values, "from-parent")
	require.Contains(t, strs.Values, "from-child")
}

func TestElementWithParentInjectorParentResolveFirst(t *testing.T) {
	parent := New()
	child := New()
	err := child.SetParent(parent)
	require.NoError(t, err)

	// Provide Element[string] in parent
	err = parent.Provide(
		func() Element[string] { return Element[string]{Value: "from-parent"} },
	)
	require.NoError(t, err)

	// Provide Element[string] in child
	err = child.Provide(
		func() Element[string] { return Element[string]{Value: "from-child"} },
	)
	require.NoError(t, err)

	// Resolve from parent first
	var parentStrs Slice[string]
	err = parent.Resolve(&parentStrs)
	require.NoError(t, err)
	require.Len(t, parentStrs.Values, 1)
	require.Contains(t, parentStrs.Values, "from-parent")

	// Resolve from child should still get both
	var childStrs Slice[string]
	err = child.Resolve(&childStrs)
	require.NoError(t, err)
	require.Len(t, childStrs.Values, 2)
	require.Contains(t, childStrs.Values, "from-parent")
	require.Contains(t, childStrs.Values, "from-child")
}

func TestElementWithContext(t *testing.T) {
	inj := New()

	type ctxKey string
	const key ctxKey = "prefix"

	// Provide Element[string] that uses context
	err := inj.Provide(
		func(ctx context.Context) Element[string] {
			prefix := ctx.Value(key).(string)
			return Element[string]{Value: prefix + "one"}
		},
		func(ctx context.Context) Element[string] {
			prefix := ctx.Value(key).(string)
			return Element[string]{Value: prefix + "two"}
		},
	)
	require.NoError(t, err)

	// Resolve with context
	ctx := context.WithValue(context.Background(), key, "ctx-")
	var strs Slice[string]
	err = inj.ResolveContext(ctx, &strs)
	require.NoError(t, err)
	require.Len(t, strs.Values, 2)
	require.Contains(t, strs.Values, "ctx-one")
	require.Contains(t, strs.Values, "ctx-two")
}

func TestElementEmptySlice(t *testing.T) {
	inj := New()

	// No Element[string] provided, resolve should return ErrTypeNotProvided
	var strs Slice[string]
	err := inj.Resolve(&strs)
	require.ErrorIs(t, err, ErrTypeNotProvided)
}

func TestElementMixedWithRegularProvider(t *testing.T) {
	inj := New()

	// Provide regular string
	err := inj.Provide(func() string { return "regular" })
	require.NoError(t, err)

	// Provide Element[int]
	err = inj.Provide(
		func() Element[int] { return Element[int]{Value: 1} },
		func() Element[int] { return Element[int]{Value: 2} },
	)
	require.NoError(t, err)

	// Resolve regular string
	var str string
	err = inj.Resolve(&str)
	require.NoError(t, err)
	require.Equal(t, "regular", str)

	// Resolve Slice[int] from Element[int]
	var ints Slice[int]
	err = inj.Resolve(&ints)
	require.NoError(t, err)
	require.Len(t, ints.Values, 2)
	require.Contains(t, ints.Values, 1)
	require.Contains(t, ints.Values, 2)
}

func TestElementWithError(t *testing.T) {
	inj := New()

	expectedErr := ErrInvalidProvider // reuse existing error for test

	// Provide Element[string] that returns error
	err := inj.Provide(
		func() Element[string] { return Element[string]{Value: "ok"} },
		func() (Element[string], error) { return Element[string]{}, expectedErr },
	)
	require.NoError(t, err)

	// Resolve should fail with the error
	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.ErrorIs(t, err, expectedErr)
}

func TestElementProviderOnlyCalledOnce(t *testing.T) {
	inj := New()

	callCount := 0
	err := inj.Provide(
		func() Element[string] {
			callCount++
			return Element[string]{Value: "value"}
		},
	)
	require.NoError(t, err)

	// Resolve twice
	var strs1 Slice[string]
	err = inj.Resolve(&strs1)
	require.NoError(t, err)
	require.Equal(t, 1, callCount)

	var strs2 Slice[string]
	err = inj.Resolve(&strs2)
	require.NoError(t, err)
	require.Equal(t, 1, callCount) // Should still be 1, not 2
}

func TestElementWithInjectTag(t *testing.T) {
	inj := New()

	// Provide Element[string]
	err := inj.Provide(
		func() Element[string] { return Element[string]{Value: "injected1"} },
		func() Element[string] { return Element[string]{Value: "injected2"} },
	)
	require.NoError(t, err)

	type MyStruct struct {
		Strings Slice[string] `inject:""`
	}

	var s MyStruct
	err = inj.Apply(&s)
	require.NoError(t, err)
	require.Len(t, s.Strings.Values, 2)
	require.Contains(t, s.Strings.Values, "injected1")
	require.Contains(t, s.Strings.Values, "injected2")
}

func TestElementProviderReturnsMultipleSameType(t *testing.T) {
	inj := New()

	// Provider returns two Element[string] of the same type
	err := inj.Provide(
		func() (Element[string], Element[string]) {
			return Element[string]{Value: "first"}, Element[string]{Value: "second"}
		},
	)
	require.NoError(t, err)

	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs.Values, 2)
	require.Contains(t, strs.Values, "first")
	require.Contains(t, strs.Values, "second")
}

func TestElementProviderReturnsMixedTypes(t *testing.T) {
	inj := New()

	type Config struct {
		Name string
	}

	callCount := 0
	// Provider returns both Element[string] and *Config
	err := inj.Provide(
		func() (Element[string], *Config) {
			callCount++
			return Element[string]{Value: "element-value"}, &Config{Name: "config-name"}
		},
	)
	require.NoError(t, err)

	// Resolve Element as Slice
	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs.Values, 1)
	require.Equal(t, "element-value", strs.Values[0])
	require.Equal(t, 1, callCount)

	// Resolve Config normally
	var cfg *Config
	err = inj.Resolve(&cfg)
	require.NoError(t, err)
	require.Equal(t, "config-name", cfg.Name)
	require.Equal(t, 1, callCount)
}

func TestElementProviderReturnsMultipleDifferentElementTypes(t *testing.T) {
	inj := New()

	// Provider returns Element[string] and Element[int]
	err := inj.Provide(
		func() (Element[string], Element[int]) {
			return Element[string]{Value: "str"}, Element[int]{Value: 42}
		},
	)
	require.NoError(t, err)

	// Resolve strings
	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs.Values, 1)
	require.Equal(t, "str", strs.Values[0])

	// Resolve ints
	var ints Slice[int]
	err = inj.Resolve(&ints)
	require.NoError(t, err)
	require.Len(t, ints.Values, 1)
	require.Equal(t, 42, ints.Values[0])
}

func TestIsElementType(t *testing.T) {
	// Test Element[string]
	elemStr := Element[string]{Value: "test"}
	require.True(t, isElementType(reflect.TypeOf(elemStr)))

	// Test Element[int]
	elemInt := Element[int]{Value: 42}
	require.True(t, isElementType(reflect.TypeOf(elemInt)))

	// Test non-Element type (not a struct)
	require.False(t, isElementType(reflect.TypeOf("string")))

	// Test struct that looks like Element but is user-defined (different package)
	type NotElement struct {
		Value string
	}
	// This should return false because it's not from inject package
	require.False(t, isElementType(reflect.TypeOf(NotElement{})))
}

func TestIsSliceType(t *testing.T) {
	// Test Slice[string]
	var strSlice Slice[string]
	require.True(t, isSliceType(reflect.TypeOf(strSlice)))
	innerType := getSliceInnerType(reflect.TypeOf(strSlice))
	require.NotNil(t, innerType)
	require.Equal(t, reflect.TypeOf(""), innerType)

	// Test Slice[int]
	var intSlice Slice[int]
	require.True(t, isSliceType(reflect.TypeOf(intSlice)))
	innerType = getSliceInnerType(reflect.TypeOf(intSlice))
	require.NotNil(t, innerType)
	require.Equal(t, reflect.TypeOf(0), innerType)

	// Test non-Slice type
	require.False(t, isSliceType(reflect.TypeOf("string")))
	innerType = getSliceInnerType(reflect.TypeOf("string"))
	require.Nil(t, innerType)
}

func TestProvideSliceTypeDirectly(t *testing.T) {
	inj := New()

	// Providing Slice[T] directly should fail
	err := inj.Provide(
		func() Slice[string] {
			return Slice[string]{Values: []string{"a", "b"}}
		},
	)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTypeNotAllowed)
	t.Log(err)
}

func TestElementProviderWithDependencyError(t *testing.T) {
	inj := New()

	// Element provider depends on a type that is not provided
	err := inj.Provide(
		func(dep string) Element[int] {
			return Element[int]{Value: len(dep)}
		},
	)
	require.NoError(t, err)

	// Resolving Slice[int] should fail because string dependency is not provided
	var ints Slice[int]
	err = inj.Resolve(&ints)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTypeNotProvided)
	t.Log(err)
}

func TestCannotDependOnElementTypeDirectly(t *testing.T) {
	inj := New()

	// Cannot depend on Element[T] directly, should use Slice[T] instead
	err := inj.Provide(
		func(e Element[string]) int {
			return len(e.Value)
		},
	)
	require.NoError(t, err) // Provide succeeds

	// Error should occur at resolve/invoke time
	var i int
	err = inj.Resolve(&i)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTypeNotAllowed)
	t.Log(err)
}

func TestElementCircularDependency(t *testing.T) {
	inj := New()

	// Element[A] depends on Slice[B], Element[B] depends on Slice[A]
	type A struct{ Name string }
	type B struct{ Name string }

	err := inj.Provide(
		func() Element[A] {
			return Element[A]{Value: A{Name: "just-a1"}}
		},
		func(bs Slice[B]) Element[A] {
			return Element[A]{Value: A{Name: "a-depends-on-b"}}
		},
		func() Element[A] {
			return Element[A]{Value: A{Name: "just-a2"}}
		},
		func(as Slice[A]) Element[B] {
			return Element[B]{Value: B{Name: "b-depends-on-a"}}
		},
	)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrCircularDependency)
	t.Log(err)
}

func TestElementTypeNotProvided(t *testing.T) {
	inj := New()

	type A struct{ Name string }
	type B struct{ Name string }

	// provides Element[A] that depends on Slice[B]
	err := inj.Provide(
		func() Element[B] {
			return Element[B]{Value: B{Name: "just-b"}}
		},
		func(_ Slice[A]) Element[B] {
			return Element[B]{Value: B{Name: "b-depends-on-a"}}
		},
	)
	require.NoError(t, err)

	var bs Slice[B]
	err = inj.Resolve(&bs)
	require.Error(t, err)
	// The error should indicate the dependency path
	t.Log(err)
}
