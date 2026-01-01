package inject

import (
	"context"
	"reflect"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestElementBasic(t *testing.T) {
	inj := New()

	// Provide multiple *Element[string]
	err := inj.Provide(
		func() *Element[string] { return NewElement("hello") },
		func() *Element[string] { return NewElement("world") },
	)
	require.NoError(t, err)

	// Resolve as Slice[string]
	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs[:], 2)
	require.Contains(t, strs[:], "hello")
	require.Contains(t, strs[:], "world")
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

	// Provide multiple *Element[testHandler]
	err := inj.Provide(
		func() *Element[testHandler] { return NewElement[testHandler](testHandlerA{}) },
		func() *Element[testHandler] { return NewElement[testHandler](testHandlerB{}) },
	)
	require.NoError(t, err)

	// Resolve as Slice[testHandler]
	var handlers Slice[testHandler]
	err = inj.Resolve(&handlers)
	require.NoError(t, err)
	require.Len(t, handlers[:], 2)

	// Verify both handlers are present
	results := make(map[string]bool)
	for _, h := range handlers[:] {
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
		func() *Element[*Service] { return NewElement(&Service{Name: "svc1"}) },
		func() *Element[*Service] { return NewElement(&Service{Name: "svc2"}) },
	)
	require.NoError(t, err)

	// Resolve as Slice[*Service]
	var services Slice[*Service]
	err = inj.Resolve(&services)
	require.NoError(t, err)
	require.Len(t, services[:], 2)

	names := make(map[string]bool)
	for _, s := range services[:] {
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
		func(cfg *Config) *Element[string] { return NewElement(cfg.Prefix + "one") },
		func(cfg *Config) *Element[string] { return NewElement(cfg.Prefix + "two") },
	)
	require.NoError(t, err)

	// Resolve as Slice[string]
	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs[:], 2)
	require.Contains(t, strs[:], "test-one")
	require.Contains(t, strs[:], "test-two")
}

func TestElementWithParentInjector(t *testing.T) {
	parent := New()
	child := New()
	err := child.SetParent(parent)
	require.NoError(t, err)

	// Provide Element[string] in parent
	err = parent.Provide(
		func() *Element[string] { return NewElement("from-parent") },
	)
	require.NoError(t, err)

	// Provide Element[string] in child
	err = child.Provide(
		func() *Element[string] { return NewElement("from-child") },
	)
	require.NoError(t, err)

	// Resolve from child should get both
	var strs Slice[string]
	err = child.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs[:], 2)
	require.Contains(t, strs[:], "from-parent")
	require.Contains(t, strs[:], "from-child")
}

func TestElementWithParentInjectorParentResolveFirst(t *testing.T) {
	parent := New()
	child := New()
	err := child.SetParent(parent)
	require.NoError(t, err)

	// Provide Element[string] in parent
	err = parent.Provide(
		func() *Element[string] { return NewElement("from-parent") },
	)
	require.NoError(t, err)

	// Provide Element[string] in child
	err = child.Provide(
		func() *Element[string] { return NewElement("from-child") },
	)
	require.NoError(t, err)

	// Resolve from parent first
	var parentStrs Slice[string]
	err = parent.Resolve(&parentStrs)
	require.NoError(t, err)
	require.Len(t, parentStrs[:], 1)
	require.Contains(t, parentStrs[:], "from-parent")

	// Resolve from child should still get both
	var childStrs Slice[string]
	err = child.Resolve(&childStrs)
	require.NoError(t, err)
	require.Len(t, childStrs[:], 2)
	require.Contains(t, childStrs[:], "from-parent")
	require.Contains(t, childStrs[:], "from-child")
}

func TestElementWithContext(t *testing.T) {
	inj := New()

	type ctxKey string
	const key ctxKey = "prefix"

	// Provide Element[string] that uses context
	err := inj.Provide(
		func(ctx context.Context) *Element[string] {
			prefix := ctx.Value(key).(string)
			return NewElement(prefix + "one")
		},
		func(ctx context.Context) *Element[string] {
			prefix := ctx.Value(key).(string)
			return NewElement(prefix + "two")
		},
	)
	require.NoError(t, err)

	// Resolve with context
	ctx := context.WithValue(context.Background(), key, "ctx-")
	var strs Slice[string]
	err = inj.ResolveContext(ctx, &strs)
	require.NoError(t, err)
	require.Len(t, strs[:], 2)
	require.Contains(t, strs[:], "ctx-one")
	require.Contains(t, strs[:], "ctx-two")
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
		func() *Element[int] { return NewElement(1) },
		func() *Element[int] { return NewElement(2) },
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
	require.Len(t, ints[:], 2)
	require.Contains(t, ints[:], 1)
	require.Contains(t, ints[:], 2)
}

func TestElementWithError(t *testing.T) {
	inj := New()

	expectedErr := ErrInvalidProvider // reuse existing error for test

	// Provide Element[string] that returns error
	err := inj.Provide(
		func() *Element[string] { return NewElement("ok") },
		func() (*Element[string], error) { return nil, expectedErr },
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
		func() *Element[string] {
			callCount++
			return NewElement("value")
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
		func() *Element[string] { return NewElement("injected1") },
		func() *Element[string] { return NewElement("injected2") },
	)
	require.NoError(t, err)

	type MyStruct struct {
		Strings Slice[string] `inject:""`
	}

	var s MyStruct
	err = inj.Apply(&s)
	require.NoError(t, err)
	require.Len(t, s.Strings[:], 2)
	require.Contains(t, s.Strings[:], "injected1")
	require.Contains(t, s.Strings[:], "injected2")
}

func TestElementProviderReturnsMultipleSameType(t *testing.T) {
	inj := New()

	// Provider returns two Element[string] of the same type
	err := inj.Provide(
		func() (*Element[string], *Element[string]) {
			return NewElement("first"), NewElement("second")
		},
	)
	require.NoError(t, err)

	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs[:], 2)
	require.Contains(t, strs[:], "first")
	require.Contains(t, strs[:], "second")
}

func TestElementProviderReturnsMixedTypes(t *testing.T) {
	inj := New()

	type Config struct {
		Name string
	}

	callCount := 0
	// Provider returns both Element[string] and *Config
	err := inj.Provide(
		func() (*Element[string], *Config) {
			callCount++
			return NewElement("element-value"), &Config{Name: "config-name"}
		},
	)
	require.NoError(t, err)

	// Resolve Element as Slice
	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs[:], 1)
	require.Equal(t, "element-value", strs[:][0])
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

	// Provider returns *Element[string] and *Element[int]
	err := inj.Provide(
		func() (*Element[string], *Element[int]) {
			return NewElement("str"), NewElement(42)
		},
	)
	require.NoError(t, err)

	// Resolve strings
	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs[:], 1)
	require.Equal(t, "str", strs[:][0])

	// Resolve ints
	var ints Slice[int]
	err = inj.Resolve(&ints)
	require.NoError(t, err)
	require.Len(t, ints[:], 1)
	require.Equal(t, 42, ints[:][0])
}

func TestIsElementType(t *testing.T) {
	// Test Element[string] (value type - not supported)
	elemStr := Element[string]{Value: "test"}
	require.False(t, isElementType(reflect.TypeOf(elemStr)))

	// Test Element[int] (value type - not supported)
	elemInt := Element[int]{Value: 42}
	require.False(t, isElementType(reflect.TypeOf(elemInt)))

	// Test *Element[string] (pointer - supported)
	elemStrPtr := &Element[string]{Value: "test"}
	require.True(t, isElementType(reflect.TypeOf(elemStrPtr)))

	// Test *Element[int] (pointer - supported)
	elemIntPtr := &Element[int]{Value: 42}
	require.True(t, isElementType(reflect.TypeOf(elemIntPtr)))

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
	// Test Slice[string] - now a slice type alias
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

	// Test non-Slice type (regular slice)
	require.False(t, isSliceType(reflect.TypeOf([]string{})))

	// Test non-Slice type (string)
	require.False(t, isSliceType(reflect.TypeOf("string")))
	innerType = getSliceInnerType(reflect.TypeOf("string"))
	require.Nil(t, innerType)
}

func TestProvideSliceTypeDirectly(t *testing.T) {
	inj := New()

	// Providing Slice[T] directly should fail
	err := inj.Provide(
		func() Slice[string] {
			return Slice[string]{"a", "b"}
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
		func(dep string) *Element[int] {
			return NewElement(len(dep))
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

	// Cannot depend on *Element[T] directly, should use Slice[T] instead
	err := inj.Provide(
		func(e *Element[string]) int {
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

	// *Element[A] depends on Slice[B], *Element[B] depends on Slice[A]
	type A struct{ Name string }
	type B struct{ Name string }

	err := inj.Provide(
		func() *Element[A] {
			return NewElement(A{Name: "just-a1"})
		},
		func(bs Slice[B]) *Element[A] {
			return NewElement(A{Name: "a-depends-on-b"})
		},
		func() *Element[A] {
			return NewElement(A{Name: "just-a2"})
		},
		func(as Slice[A]) *Element[B] {
			return NewElement(B{Name: "b-depends-on-a"})
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

	// provides *Element[B] that depends on Slice[A]
	err := inj.Provide(
		func() *Element[B] {
			return NewElement(B{Name: "just-b"})
		},
		func(_ Slice[A]) *Element[B] {
			return NewElement(B{Name: "b-depends-on-a"})
		},
	)
	require.NoError(t, err)

	var bs Slice[B]
	err = inj.Resolve(&bs)
	require.Error(t, err)
	// The error should indicate the dependency path
	t.Log(err)
}

func TestElementPointerType(t *testing.T) {
	inj := New()

	// Provide *Element[string] - pointer type
	err := inj.Provide(
		func() *Element[string] {
			return &Element[string]{Value: "hello"}
		},
		func() (*Element[string], error) {
			return &Element[string]{Value: "world"}, nil
		},
	)
	require.NoError(t, err)

	// Resolve as Slice[string]
	var strs Slice[string]
	err = inj.Resolve(&strs)
	require.NoError(t, err)
	require.Len(t, strs[:], 2)
	require.Contains(t, strs[:], "hello")
	require.Contains(t, strs[:], "world")
}

func TestElementPointerWithError(t *testing.T) {
	inj := New()

	// Provide *Element[int] that returns error
	err := inj.Provide(
		func() (*Element[int], error) {
			return nil, errors.New("failed to create element")
		},
	)
	require.NoError(t, err)

	// Resolve should fail
	var ints Slice[int]
	err = inj.Resolve(&ints)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create element")
}
