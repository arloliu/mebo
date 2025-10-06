package options

// Option represents a functional option for configuring any type T.
// This is a generic interface that can be used with any type.
type Option[T any] interface {
	apply(T) error
}

// Func is a generic functional option that wraps a function.
// It implements the Option interface for any type T.
type Func[T any] struct {
	applyFunc func(T) error
}

// apply implements the Option interface.
func (f *Func[T]) apply(target T) error {
	return f.applyFunc(target)
}

// New creates a new functional option from a function.
// This is the generic factory function for creating options.
func New[T any](fn func(T) error) *Func[T] {
	return &Func[T]{applyFunc: fn}
}

// Apply applies multiple options to a target object.
// This is a utility function that applies a slice of options in order.
func Apply[T any](target T, opts ...Option[T]) error {
	for _, opt := range opts {
		if err := opt.apply(target); err != nil {
			return err
		}
	}

	return nil
}

// NoError creates a functional option from a function that doesn't return an error.
// This is a convenience function for options that can't fail.
func NoError[T any](fn func(T)) *Func[T] {
	return &Func[T]{
		applyFunc: func(target T) error {
			fn(target)
			return nil
		},
	}
}
