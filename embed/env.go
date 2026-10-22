package embed

import "sync"

// onceInit runs an initialization function at most once and remembers its
// result, so concurrent callers observe the same outcome instead of racing to
// initialize a process-global resource.
type onceInit struct {
	once sync.Once
	err  error
}

// do runs fn on the first call and returns the result of that call on every
// later call. A failure stays failed: the resource it guards is global, so
// repeating a broken setup only repeats the same error.
func (o *onceInit) do(fn func() error) error {
	o.once.Do(func() { o.err = fn() })
	return o.err
}
