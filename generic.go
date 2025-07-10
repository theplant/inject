package inject

// Resolve resolves a dependency from the injector.
func Resolve[T any](inj interface{ Resolve(...any) error }) (T, error) {
	var t T
	if err := inj.Resolve(&t); err != nil { //nolint:wrapcheck
		return t, err
	}
	return t, nil
}

// MustResolve resolves a dependency from the injector.
func MustResolve[T any](inj interface{ Resolve(...any) error }) T {
	t, err := Resolve[T](inj)
	if err != nil {
		panic(err)
	}
	return t
}

func ResolveContext[T any](
	ctx Context,
	inj interface{ ResolveContext(Context, ...any) error },
) (T, error) {
	var t T
	if err := inj.ResolveContext(Context(ctx), &t); err != nil {
		return t, err
	}
	return t, nil
}

func MustResolveContext[T any](
	ctx Context,
	inj interface{ ResolveContext(Context, ...any) error },
) T {
	t, err := ResolveContext[T](ctx, inj)
	if err != nil {
		panic(err)
	}
	return t
}
