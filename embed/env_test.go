package embed

import (
	"errors"
	"sync"
	"testing"
)

func TestOnceInitRunsOnceUnderConcurrency(t *testing.T) {
	var o onceInit
	var mu sync.Mutex
	calls := 0
	errs := make([]error, 32)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = o.do(func() error {
				mu.Lock()
				calls++
				mu.Unlock()
				return nil
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("the initializer ran %d times, want 1", calls)
	}
}

func TestOnceInitRemembersTheFailure(t *testing.T) {
	var o onceInit
	boom := errors.New("ort init failed")
	calls := 0
	for range 3 {
		err := o.do(func() error {
			calls++
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want %v", err, boom)
		}
	}
	if calls != 1 {
		t.Fatalf("the initializer ran %d times, want 1", calls)
	}
}
