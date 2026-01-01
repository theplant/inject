package inject

import (
	"reflect"
	"strings"
)

// Element is a wrapper type that allows multiple values of the same type T
// to be provided to the injector. When resolving Slice[T], all *Element[T]
// values will be collected and returned.
//
// Usage:
//
//	inj.Provide(func() *Element[http.Handler] { return NewElement(handler1) })
//	inj.Provide(func() *Element[http.Handler] { return NewElement(handler2) })
//
//	// Resolve as Slice[T]
//	var handlers Slice[http.Handler]
//	inj.Resolve(&handlers) // handlers = []http.Handler{handler1, handler2}
type Element[T any] struct {
	Value T
}

// NewElement creates a new *Element[T] with the given value.
func NewElement[T any](value T) *Element[T] {
	return &Element[T]{Value: value}
}

// Slice is a container type that collects all *Element[T] values into a slice of T.
// Use Slice[T] to resolve multiple *Element[T] providers at once.
type Slice[T any] []T

// isElementType checks if the type is *Element[T] type.
// Only pointer type is supported.
func isElementType(t reflect.Type) bool {
	if t.Kind() != reflect.Ptr {
		return false
	}
	t = t.Elem()
	if t.Kind() != reflect.Struct {
		return false
	}
	// Check type name starts with "Element[" and is from this package
	return strings.HasPrefix(t.Name(), "Element[") && t.PkgPath() == "github.com/theplant/inject"
}

// getElementInnerType extracts T from *Element[T].
// Returns nil if not an Element type.
func getElementInnerType(t reflect.Type) reflect.Type {
	if !isElementType(t) {
		return nil
	}
	// t is *Element[T], get Element[T]
	elemType := t.Elem()
	field, ok := elemType.FieldByName("Value")
	if !ok {
		// If the Value field is missing, treat this as an invalid Element[T] type.
		return nil
	}
	return field.Type
}

// isSliceType checks if the type is Slice[T].
// Slice[T] is a slice type alias, so it has Kind() == reflect.Slice.
func isSliceType(t reflect.Type) bool {
	if t.Kind() != reflect.Slice {
		return false
	}
	return strings.HasPrefix(t.Name(), "Slice[") && t.PkgPath() == "github.com/theplant/inject"
}

// getSliceInnerType extracts T from Slice[T].
// Returns nil if not a Slice type.
func getSliceInnerType(t reflect.Type) reflect.Type {
	if !isSliceType(t) {
		return nil
	}
	// Slice[T] is []T, so Elem() returns T
	return t.Elem()
}
