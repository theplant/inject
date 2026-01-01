package inject

// Void is a marker type for constructors that don't return any value.
// When a constructor has no return type (or only returns error), it is
// automatically treated as returning *Element[*Void].
//
// This allows "side-effect only" constructors (e.g., modifying config)
// to be registered and executed through the injection system.
//
// Usage:
//
//	// Void constructor - automatically returns *Element[*Void]
//	inj.Provide(func(cfg *Config) {
//	    cfg.Debug = true  // Modify config as side effect
//	})
//
//	// Execute all void constructors via Build
//	inj.Build()
//
//	// Or resolve explicitly
//	var voids Slice[*Void]
//	inj.Resolve(&voids)
type Void struct{}

// NewVoidElement creates a new *Element[*Void].
func NewVoidElement() *Element[*Void] {
	return NewElement[*Void](nil)
}
