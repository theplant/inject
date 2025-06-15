package inject

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sync/singleflight"
)

var (
	ErrTypeNotProvided     = errors.New("type not provided")
	ErrTypeAlreadyProvided = errors.New("type already provided")
	ErrParentAlreadySet    = errors.New("parent already set")
	ErrContextNotAllowed   = errors.New("inject.Context not allowed")
)

type provider struct {
	seq uint64
	fn  any // value func
}

type Injector struct {
	mu sync.RWMutex

	values    map[reflect.Type]reflect.Value
	providers map[reflect.Type]*provider
	parent    *Injector

	maxProviderSeq uint64
	sfg            singleflight.Group
}

func New() *Injector {
	inj := &Injector{
		values:         map[reflect.Type]reflect.Value{},
		providers:      map[reflect.Type]*provider{},
		maxProviderSeq: 0,
	}
	inj.Provide(func() *Injector { return inj })
	return inj
}

func (inj *Injector) SetParent(parent *Injector) error {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if inj.parent != nil {
		return ErrParentAlreadySet
	}
	inj.parent = parent
	return nil
}

var typeError = reflect.TypeOf((*error)(nil)).Elem()

const invalidProvider = "Provide only accepts a function that returns at least one type except error"

func (inj *Injector) provide(f any) (err error) {
	rv := reflect.ValueOf(f)
	rt := rv.Type()
	if rt.Kind() != reflect.Func {
		panic(invalidProvider)
	}

	inj.mu.Lock()
	defer inj.mu.Unlock()

	setted := []reflect.Type{}
	defer func() {
		if err != nil {
			for _, t := range setted {
				delete(inj.providers, t)
			}
		}
	}()

	numOut := rt.NumOut()
	if numOut == 0 {
		panic(invalidProvider)
	}

	provider := &provider{
		seq: inj.maxProviderSeq + 1,
		fn:  f,
	}
	for i := 0; i < numOut; i++ {
		outType := rt.Out(i)

		// skip error type if it is the last return value
		if i == numOut-1 && outType == typeError {
			continue
		}

		if outType == typeContext {
			return fmt.Errorf("%w: %s", ErrContextNotAllowed, outType.String())
		}

		if _, ok := inj.values[outType]; ok {
			return fmt.Errorf("%w: %s", ErrTypeAlreadyProvided, outType.String())
		}

		if _, ok := inj.providers[outType]; ok {
			return fmt.Errorf("%w: %s", ErrTypeAlreadyProvided, outType.String())
		}

		inj.providers[outType] = provider
		setted = append(setted, outType)
	}
	if len(setted) == 0 {
		panic(invalidProvider)
	}
	inj.maxProviderSeq++

	return nil
}

type Context context.Context

var typeContext = reflect.TypeOf((*Context)(nil)).Elem()

func (inj *Injector) invoke(ctx context.Context, f any) ([]reflect.Value, error) {
	rt := reflect.TypeOf(f)
	if rt.Kind() != reflect.Func {
		panic("Invoke only accepts a function")
	}

	numIn := rt.NumIn()
	in := make([]reflect.Value, numIn)
	for i := 0; i < numIn; i++ {
		argType := rt.In(i)
		if argType == typeContext {
			in[i] = reflect.ValueOf(Context(ctx))
			continue
		}
		argValue, err := inj.resolve(ctx, argType)
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

	return outs, nil
}

func (inj *Injector) resolve(ctx context.Context, rt reflect.Type) (reflect.Value, error) {
	inj.mu.RLock()
	rv := inj.values[rt]
	if rv.IsValid() {
		inj.mu.RUnlock()
		return rv, nil
	}
	provider, ok := inj.providers[rt]
	parent := inj.parent
	inj.mu.RUnlock()

	if ok {
		// ensure that the provider is only executed once same time
		_, err, _ := inj.sfg.Do(fmt.Sprintf("%d", provider.seq), func() (any, error) {
			// must recheck the provider, because it may be deleted by prev inj.sfg.Do
			inj.mu.RLock()
			_, ok := inj.providers[rt]
			inj.mu.RUnlock()
			if !ok {
				return nil, nil
			}

			results, err := inj.invoke(ctx, provider.fn)
			if err != nil {
				return nil, err
			}

			inj.mu.Lock()
			for _, result := range results {
				resultType := result.Type()
				inj.values[resultType] = result
				delete(inj.providers, resultType)
			}
			inj.mu.Unlock()

			return nil, nil
		})
		if err != nil {
			return rv, err
		}
		return inj.resolve(ctx, rt)
	}

	if parent != nil {
		return parent.resolve(ctx, rt)
	}

	return rv, fmt.Errorf("%w: %s", ErrTypeNotProvided, rt.String())
}

func unwrapPtr(rv reflect.Value) reflect.Value {
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	return rv
}

func (inj *Injector) Apply(val any) error {
	return inj.ApplyWithContext(context.Background(), val)
}

func (inj *Injector) ApplyWithContext(ctx Context, val any) error {
	rv := unwrapPtr(reflect.ValueOf(val))
	if rv.Kind() != reflect.Struct {
		panic("Apply only accepts a struct")
	}
	return inj.applyStruct(ctx, rv)
}

const tagOptional = "optional"

func (inj *Injector) applyStruct(ctx context.Context, rv reflect.Value) error {
	rt := rv.Type()

	for i := 0; i < rv.NumField(); i++ {
		field := rv.Field(i)
		structField := rt.Field(i)
		if tag, ok := structField.Tag.Lookup("inject"); ok {
			if !field.CanSet() {
				// If the field is unexported, we need to create a new field that is settable.
				field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
			}
			dep, err := inj.resolve(ctx, field.Type())
			if err != nil {
				if errors.Is(err, ErrTypeNotProvided) && strings.TrimSpace(tag) == tagOptional {
					continue
				}
				return err
			}
			field.Set(dep)
		}
	}

	return nil
}

func (inj *Injector) Provide(fs ...any) error {
	for _, f := range fs {
		if err := inj.provide(f); err != nil {
			return err
		}
	}
	return nil
}

func (inj *Injector) Invoke(f any) ([]any, error) {
	return inj.InvokeWithContext(context.Background(), f)
}

func (inj *Injector) InvokeWithContext(ctx Context, f any) ([]any, error) {
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
	return inj.ResolveWithContext(context.Background(), refs...)
}

func (inj *Injector) ResolveWithContext(ctx Context, refs ...any) error {
	for _, ref := range refs {
		rv, err := inj.resolve(ctx, reflect.TypeOf(ref).Elem())
		if err != nil {
			return err
		}
		reflect.ValueOf(ref).Elem().Set(rv)
	}
	return nil
}
