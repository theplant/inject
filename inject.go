package inject

import (
	"cmp"
	"context"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/pkg/errors"
	"golang.org/x/sync/singleflight"
)

var (
	ErrTypeNotProvided     = errors.New("type not provided")
	ErrTypeAlreadyProvided = errors.New("type already provided")
	ErrParentAlreadySet    = errors.New("parent already set")
	ErrTypeNotAllowed      = errors.New("type not allowed")
	ErrCircularDependency  = errors.New("circular dependency detected")
	ErrErrorTypeMustBeLast = errors.New("error type must be the last return value")
	ErrInvalidProvider     = errors.New("provide only accepts a function that returns at least one type except error")
	ErrInvalidInvokeTarget = errors.New("invoke only accepts a function")
	ErrInvalidApplyTarget  = errors.New("apply only accepts a struct")
)

type ctxKeyDependencyPath struct{}

type dependencyPath []typeKey

func (dp dependencyPath) String() string {
	if len(dp) == 0 {
		return ""
	}
	var parts []string
	for _, k := range dp {
		s := k.rt.String()
		if k.index > 0 {
			s += "[" + strconv.Itoa(k.index) + "]"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " -> ")
}

func getDependencyPath(ctx context.Context) dependencyPath {
	path, _ := ctx.Value(ctxKeyDependencyPath{}).(dependencyPath)
	return path
}

func appendDependencyToPath(ctx context.Context, key typeKey) context.Context {
	path := getDependencyPath(ctx)
	newPath := append(dependencyPath{}, path...)
	newPath = append(newPath, key)
	return context.WithValue(ctx, ctxKeyDependencyPath{}, newPath)
}

var (
	typeError       = reflect.TypeOf((*error)(nil)).Elem()
	typeContext     = reflect.TypeOf((*context.Context)(nil)).Elem()
	typeElementVoid = reflect.TypeOf((*Element[*Void])(nil))
)

// IsTypeAllowed checks if a type is allowed as a function input or output parameter.
func IsTypeAllowed(typ reflect.Type) bool {
	return typ != typeContext && typ != typeError
}

// IsInputTypeAllowed checks if a type is allowed as a function input parameter.
// Element[T] types are not allowed as input - use Slice[T] instead.
func IsInputTypeAllowed(typ reflect.Type) bool {
	if !IsTypeAllowed(typ) {
		return false
	}
	return !isElementType(typ)
}

// IsOutputTypeAllowed checks if a type is allowed as a function output.
// Slice[T] types are not allowed as output - use Element[T] instead.
func IsOutputTypeAllowed(typ reflect.Type) bool {
	if !IsTypeAllowed(typ) {
		return false
	}
	return !isSliceType(typ)
}

type provider struct {
	seq  uint64
	ctor any // value func
}

// typeKey is used as a map key to support multiple providers for Element[T] types.
// For normal types, index is always 0.
// For Element[T] types, index increments for each provider.
type typeKey struct {
	rt    reflect.Type
	index int
}

type Injector struct {
	mu sync.RWMutex

	values    map[typeKey]reflect.Value
	providers map[typeKey]*provider
	parent    *Injector

	// elementCounts tracks the count of providers for each Element[T] type
	elementCounts map[reflect.Type]int

	maxProviderSeq uint64
	sfg            singleflight.Group
}

func New() *Injector {
	inj := &Injector{
		values:         map[typeKey]reflect.Value{},
		providers:      map[typeKey]*provider{},
		elementCounts:  map[reflect.Type]int{},
		maxProviderSeq: 0,
	}
	_ = inj.Provide(func() *Injector { return inj })
	return inj
}

func (inj *Injector) SetParent(parent *Injector) error {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if inj.parent != nil {
		return errors.WithStack(ErrParentAlreadySet)
	}
	inj.parent = parent
	return nil
}

func (inj *Injector) unsafeProvide(ctor any) error {
	rv := reflect.ValueOf(ctor)
	rt := rv.Type()
	if rt.Kind() != reflect.Func {
		return errors.Wrap(ErrInvalidProvider, "ctor is not a function")
	}

	// Get valid output types with error position validation
	outputTypes, err := getValidOutputTypes(rt)
	if err != nil {
		return err
	}

	p := &provider{
		seq:  inj.maxProviderSeq,
		ctor: ctor,
	}
	inj.maxProviderSeq++

	// Collect all keys for cycle detection
	var keysToCheck []typeKey

	for _, outType := range outputTypes {
		// Check if this is *Element[T] type - allow multiple registrations with incrementing index
		if isElementType(outType) {
			idx := inj.elementCounts[outType]
			key := typeKey{rt: outType, index: idx}
			inj.providers[key] = p
			inj.elementCounts[outType] = idx + 1
			keysToCheck = append(keysToCheck, key)
			continue
		}

		// Normal types: index is always 0
		key := typeKey{rt: outType, index: 0}

		if _, ok := inj.values[key]; ok {
			return errors.Wrap(ErrTypeAlreadyProvided, outType.String())
		}

		if _, ok := inj.providers[key]; ok {
			return errors.Wrap(ErrTypeAlreadyProvided, outType.String())
		}

		inj.providers[key] = p
		keysToCheck = append(keysToCheck, key)
	}

	// Cycle detection for all types (including Element types)
	for _, key := range keysToCheck {
		if err := inj.unsafeDFSDetectCycle(key); err != nil {
			return err
		}
	}

	return nil
}

func (inj *Injector) invoke(ctx context.Context, f any) ([]reflect.Value, error) {
	rt := reflect.TypeOf(f)
	if rt.Kind() != reflect.Func {
		return nil, errors.Wrap(ErrInvalidInvokeTarget, "f is not a function")
	}

	numIn := rt.NumIn()
	in := make([]reflect.Value, numIn)
	for i := 0; i < numIn; i++ {
		argType := rt.In(i)
		if argType == typeContext {
			in[i] = reflect.ValueOf(ctx)
			continue
		}
		if !IsInputTypeAllowed(argType) {
			return nil, errors.Wrapf(ErrTypeNotAllowed, "arg %d: %s", i, argType.String())
		}
		argValue, err := inj.resolve(ctx, typeKey{rt: argType, index: 0})
		if err != nil {
			return nil, err
		}
		in[i] = argValue
	}

	outs := reflect.ValueOf(f).Call(in)

	// apply if possible
	for _, out := range outs {
		unwrapped := unwrapPtr(out)
		if unwrapped.Kind() == reflect.Struct {
			if err := inj.applyStruct(ctx, unwrapped); err != nil {
				return nil, err
			}
		}
	}

	numOut := len(outs)
	if numOut > 0 && rt.Out(numOut-1) == typeError {
		rvErr := outs[numOut-1]
		outs = outs[:numOut-1]
		if !rvErr.IsNil() {
			return outs, rvErr.Interface().(error)
		}
	}

	// If no outputs (void function), return *Element[*Void]
	if len(outs) == 0 {
		outs = append(outs, reflect.ValueOf(NewVoidElement()))
	}

	return outs, nil
}

func (inj *Injector) resolve(ctx context.Context, key typeKey) (reflect.Value, error) {
	originalCtx := ctx
	ctx = appendDependencyToPath(ctx, key)

	// Check if this is a Slice[T] type that should be resolved from Element[T] providers
	if isSliceType(key.rt) {
		return inj.resolveSlice(ctx, key.rt, getSliceInnerType(key.rt))
	}

	inj.mu.RLock()
	rv := inj.values[key]
	if rv.IsValid() {
		inj.mu.RUnlock()
		return rv, nil
	}
	p, ok := inj.providers[key]
	parent := inj.parent
	inj.mu.RUnlock()

	if ok {
		// ensure that the provider is only executed once same time
		_, err, _ := inj.sfg.Do(strconv.FormatUint(p.seq, 10), func() (any, error) {
			// must recheck the provider, because it may be deleted by prev inj.sfg.Do
			inj.mu.RLock()
			_, ok := inj.providers[key]
			inj.mu.RUnlock()
			if !ok {
				return nil, nil
			}

			results, err := inj.invoke(ctx, p.ctor)
			if err != nil {
				return nil, err
			}

			// Find all keys that share this provider (for Element types that return multiple values)
			inj.mu.Lock()
			keysForProvider := make(map[reflect.Type][]typeKey)
			for k, prov := range inj.providers {
				if prov.seq == p.seq {
					keysForProvider[k.rt] = append(keysForProvider[k.rt], k)
				}
			}

			// Process results: assign each result to its corresponding key
			typeCounts := make(map[reflect.Type]int)
			for _, result := range results {
				resultType := result.Type()
				keys := keysForProvider[resultType]
				idx := typeCounts[resultType]
				if idx >= len(keys) {
					inj.mu.Unlock()
					return nil, errors.Errorf("provider seq %d returned more values of type %v than registered", p.seq, resultType)
				}
				inj.values[keys[idx]] = result
				delete(inj.providers, keys[idx])
				typeCounts[resultType]++
			}
			inj.mu.Unlock()

			return nil, nil
		})
		if err != nil {
			return rv, err
		}
		return inj.resolve(ctx, key)
	}

	if parent == nil {
		return rv, errors.Wrapf(ErrTypeNotProvided, "dependency path: %s", getDependencyPath(ctx).String())
	}

	return parent.resolve(originalCtx, key)
}

// resolveSlice resolves a Slice[T] type by collecting all *Element[T] values
func (inj *Injector) resolveSlice(ctx context.Context, sliceType reflect.Type, innerType reflect.Type) (reflect.Value, error) {
	// sliceType is Slice[T] which is []T
	// Create the result slice
	result := reflect.MakeSlice(sliceType, 0, 0)

	// Collect from parent first (if any)
	if inj.parent != nil {
		parentResult, err := inj.parent.resolveSlice(ctx, sliceType, innerType)
		if err != nil && !errors.Is(err, ErrTypeNotProvided) {
			return reflect.Value{}, err
		}
		if err == nil && parentResult.Len() > 0 {
			result = reflect.AppendSlice(result, parentResult)
		}
	}

	// Find *Element[T] type (all Element types are normalized to pointer)
	var elementType reflect.Type
	inj.mu.RLock()
	for et := range inj.elementCounts {
		if inner := getElementInnerType(et); inner != nil && inner == innerType {
			elementType = et
			break
		}
	}
	count := 0
	if elementType != nil {
		count = inj.elementCounts[elementType]
	}
	inj.mu.RUnlock()

	if count == 0 && result.Len() == 0 {
		return reflect.Value{}, errors.Wrapf(ErrTypeNotProvided, "dependency path: %s", getDependencyPath(ctx).String())
	}

	// Resolve each *Element[T] by calling resolve directly
	for i := 0; i < count; i++ {
		key := typeKey{rt: elementType, index: i}

		rv, err := inj.resolve(ctx, key)
		if err != nil {
			return reflect.Value{}, errors.Wrapf(err, "failed to resolve element %d for %s", i, elementType.String())
		}

		if rv.IsValid() {
			// rv is *Element[T], dereference to get Value
			if rv.IsNil() {
				continue
			}
			elem := rv.Elem()
			valueField := elem.FieldByName("Value")
			if valueField.IsValid() && valueField.Type() == innerType {
				result = reflect.Append(result, valueField)
			}
		}
	}

	return result, nil
}

func unwrapPtr(rv reflect.Value) reflect.Value {
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	return rv
}

func (inj *Injector) Apply(val any) error {
	return inj.ApplyContext(context.Background(), val)
}

func (inj *Injector) ApplyContext(ctx context.Context, val any) error {
	rv := unwrapPtr(reflect.ValueOf(val))
	if rv.Kind() != reflect.Struct {
		return errors.Wrap(ErrInvalidApplyTarget, "val is not a struct")
	}
	return inj.applyStruct(ctx, rv)
}

var (
	TagName          = "inject"
	TagValueOptional = "optional"
)

func (inj *Injector) applyStruct(ctx context.Context, rv reflect.Value) error {
	rt := rv.Type()

	for i := 0; i < rv.NumField(); i++ {
		structField := rt.Field(i)
		if tag, ok := structField.Tag.Lookup(TagName); ok {
			if !IsInputTypeAllowed(structField.Type) {
				return errors.Wrapf(ErrTypeNotAllowed, "field %d: %s", i, structField.Type.String())
			}

			dep, err := inj.resolve(ctx, typeKey{rt: structField.Type, index: 0})
			if err != nil {
				if errors.Is(err, ErrTypeNotProvided) {
					if strings.TrimSpace(tag) == TagValueOptional {
						continue
					}
				}

				return err
			}

			field := rv.Field(i)
			if !field.CanSet() {
				// If the field is unexported, we need to create a new field that is settable.
				field = reflect.NewAt(structField.Type, unsafe.Pointer(field.UnsafeAddr())).Elem()
			}
			field.Set(dep)
		}
	}

	return nil
}

// flatten recursively flattens any arrays/slices found in the arguments.
// This allows grouping related constructors together for better organization.
func flatten(vs ...any) []any {
	var result []any

	for _, f := range vs {
		rv := reflect.ValueOf(f)

		// If it's a slice or array, recursively flatten it
		if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
			// Convert slice/array elements to []any
			subItems := make([]any, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				subItems[i] = rv.Index(i).Interface()
			}
			// Recursively flatten the sub-items
			result = append(result, flatten(subItems...)...)
		} else {
			// Regular constructor function, add directly
			result = append(result, f)
		}
	}

	return result
}

func (inj *Injector) Provide(ctors ...any) (xerr error) {
	ctors = flatten(ctors...)

	inj.mu.Lock()
	defer inj.mu.Unlock()

	originalProviders := maps.Clone(inj.providers)
	originalElementCounts := maps.Clone(inj.elementCounts)
	originalMaxSeq := inj.maxProviderSeq
	defer func() {
		if xerr != nil {
			inj.providers = originalProviders
			inj.elementCounts = originalElementCounts
			inj.maxProviderSeq = originalMaxSeq
		}
	}()

	for _, ctor := range ctors {
		if err := inj.unsafeProvide(ctor); err != nil {
			return err
		}
	}

	return nil
}

func (inj *Injector) Invoke(f any) ([]any, error) {
	return inj.InvokeContext(context.Background(), f)
}

func (inj *Injector) InvokeContext(ctx context.Context, f any) ([]any, error) {
	results, err := inj.invoke(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(results))
	for i, result := range results {
		out[i] = result.Interface()
	}
	return out, nil
}

func (inj *Injector) Resolve(refs ...any) error {
	return inj.ResolveContext(context.Background(), refs...)
}

func (inj *Injector) ResolveContext(ctx context.Context, refs ...any) error {
	for _, ref := range refs {
		refType := reflect.TypeOf(ref)
		if refType == nil || refType.Kind() != reflect.Ptr {
			return errors.Errorf("resolve requires pointer arguments, got %T", ref)
		}

		rv, err := inj.resolve(ctx, typeKey{rt: refType.Elem(), index: 0})
		if err != nil {
			return err
		}
		reflect.ValueOf(ref).Elem().Set(rv)
	}
	return nil
}

// Build automatically resolves all provided types using background context.
// This will trigger the creation of all registered constructors,
// ensuring that all dependencies are properly instantiated.
func (inj *Injector) Build(ctors ...any) error {
	return inj.BuildContext(context.Background(), ctors...)
}

// BuildContext automatically resolves all provided types.
// This will trigger the creation of all registered constructors,
// ensuring that all dependencies are properly instantiated.
func (inj *Injector) BuildContext(ctx context.Context, ctors ...any) error {
	if len(ctors) > 0 {
		if err := inj.Provide(ctors...); err != nil {
			return err
		}
	}

	inj.mu.RLock()
	var keysToResolve []typeKey
	keyToSeq := make(map[typeKey]uint64)
	for key, p := range inj.providers {
		keysToResolve = append(keysToResolve, key)
		keyToSeq[key] = p.seq
	}
	inj.mu.RUnlock()

	// Sort keys by their provider sequence for stable order based on registration sequence
	slices.SortFunc(keysToResolve, func(a, b typeKey) int {
		return cmp.Compare(keyToSeq[a], keyToSeq[b])
	})

	for _, key := range keysToResolve {
		if _, err := inj.resolve(ctx, key); err != nil {
			return err
		}
	}

	return nil
}

// getValidOutputTypes extracts all valid output types from a constructor function type,
// performing error type position validation and filtering out error types.
// For Element[T] types, duplicates are allowed (same type can appear multiple times).
// For normal types, duplicates are deduplicated.
// If the function has no return values (or only returns error), it returns *Element[*Void].
func getValidOutputTypes(rt reflect.Type) ([]reflect.Type, error) {
	if rt.Kind() != reflect.Func {
		return nil, nil
	}

	var outputTypes []reflect.Type
	seen := make(map[reflect.Type]bool) // Use map for deduplication of normal types
	numOut := rt.NumOut()

	for i := 0; i < numOut; i++ {
		outType := rt.Out(i)

		// Validate error type position: error can only be the last return value
		if outType == typeError {
			if i != numOut-1 {
				return nil, errors.Wrapf(ErrErrorTypeMustBeLast, "error type found at position %d, but must be at position %d", i, numOut-1)
			}
			// Skip error type if it is the last return value
			continue
		}

		if !IsOutputTypeAllowed(outType) {
			return nil, errors.Wrapf(ErrTypeNotAllowed, "out %d: %s", i, outType.String())
		}

		// Element types: allow duplicates (each gets its own index)
		// Normal types: deduplicate
		if isElementType(outType) {
			outputTypes = append(outputTypes, outType)
		} else if !seen[outType] {
			outputTypes = append(outputTypes, outType)
			seen[outType] = true
		}
	}

	// If no valid output types, treat as returning *Element[*Void]
	if len(outputTypes) == 0 {
		outputTypes = append(outputTypes, typeElementVoid)
	}

	return outputTypes, nil
}
