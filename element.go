package inject

import (
	"reflect"
	"strings"
)

// Element is a wrapper type that allows multiple values of the same type T
// to be provided to the injector. When resolving Slice[T], all Element[T]
// values will be collected and returned.
//
// Usage:
//
//	inj.Provide(func() Element[http.Handler] { return Element[http.Handler]{Value: handler1} })
//	inj.Provide(func() Element[http.Handler] { return Element[http.Handler]{Value: handler2} })
//
//	// Resolve as Slice[T]
//	var handlers Slice[http.Handler]
//	inj.Resolve(&handlers) // handlers.Values = []http.Handler{handler1, handler2}
type Element[T any] struct {
	Value T
}

// NewElement creates a new Element[T] with the given value.
func NewElement[T any](value T) Element[T] {
	return Element[T]{Value: value}
}

// Slice is a container type that collects all Element[T] values into a slice of T.
// Use this type to resolve multiple Element[T] providers at once.
type Slice[T any] struct {
	Values []T
}

// isElementType checks if the type is an Element[T] type.
// Uses type name prefix check for reliability.
func isElementType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	// Check type name starts with "Element[" and is from this package
	return strings.HasPrefix(t.Name(), "Element[") && t.PkgPath() == "github.com/theplant/inject"
}

// getElementInnerType extracts T from Element[T].
// Returns nil if not an Element type.
func getElementInnerType(t reflect.Type) reflect.Type {
	if !isElementType(t) {
		return nil
	}
	field, ok := t.FieldByName("Value")
	if !ok {
		panic("Element[T] must have a Value field")
	}
	return field.Type
}

// isSliceType checks if the type is Slice[T].
func isSliceType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
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
	// Get the element type from Values field
	valuesField, ok := t.FieldByName("Values")
	if !ok || valuesField.Type.Kind() != reflect.Slice {
		panic("Slice[T] must have a Values field")
	}
	return valuesField.Type.Elem()
}
