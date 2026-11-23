package vasest

import "errors"

func testErrorMatchInto[T any](source error, target *T) bool {
	return errors.As(source, target)
}

func testErrorExtractor[T any](source error, predicate func(T) bool) bool {
	var value T
	return errors.As(source, &value) && predicate(value)
}
