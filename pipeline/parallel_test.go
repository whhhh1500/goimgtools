package pipeline

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestParallel: every call is made once; without helpers in order on the
// calling goroutine; with them never more helpers busy than were given,
// however many goroutines call at once; a panic comes back to the caller
// and every helper is given back.
func TestParallel(t *testing.T) {
	env := NewEnv(0, t.TempDir())
	var order []int
	env.Parallel(5, func(i int) { order = append(order, i) })
	if len(order) != 5 || order[0] != 0 || order[4] != 4 {
		t.Fatalf("without helpers: %v", order)
	}

	for _, helpers := range []int{1, 3, 7} {
		env := NewEnv(0, t.TempDir())
		env.SetHelpers(helpers)
		if env.Helpers() != helpers {
			t.Fatalf("Helpers() = %d, want %d", env.Helpers(), helpers)
		}
		const callers = 3
		var busy, worst atomic.Int32
		var counts [callers][40]atomic.Int32
		var wg sync.WaitGroup
		for c := 0; c < callers; c++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				env.Parallel(40, func(i int) {
					n := busy.Add(1)
					for w := worst.Load(); n > w && !worst.CompareAndSwap(w, n); w = worst.Load() {
					}
					time.Sleep(200 * time.Microsecond)
					counts[c][i].Add(1)
					busy.Add(-1)
				})
			}()
		}
		wg.Wait()
		for c := range counts {
			for i := range counts[c] {
				if n := counts[c][i].Load(); n != 1 {
					t.Fatalf("helpers %d: caller %d call %d made %d times", helpers, c, i, n)
				}
			}
		}
		if w := int(worst.Load()); w > callers+helpers {
			t.Errorf("helpers %d: %d calls ran at once, at most %d may", helpers, w, callers+helpers)
		}
		if len(env.helpers) != helpers {
			t.Errorf("helpers %d: %d given back", helpers, len(env.helpers))
		}
	}

	// A panic, on a helper or on the caller, comes back as a *PanicError.
	env = NewEnv(0, t.TempDir())
	env.SetHelpers(3)
	for _, at := range []int{0, 5, 19} {
		func() {
			defer func() {
				v := recover()
				var p *PanicError
				if err, ok := v.(error); !ok || !errors.As(err, &p) || p.Value != "boom" {
					t.Errorf("panic at %d came back as %v", at, v)
				}
			}()
			env.Parallel(20, func(i int) {
				time.Sleep(100 * time.Microsecond)
				if i == at {
					panic("boom")
				}
			})
			t.Errorf("panic at %d not raised again", at)
		}()
		if len(env.helpers) != 3 {
			t.Errorf("after the panic at %d: %d of 3 helpers given back", at, len(env.helpers))
		}
	}

	// Inside a call: no deadlock, every inner call made.
	var inner atomic.Int32
	done := make(chan struct{})
	go func() {
		env.Parallel(6, func(int) { env.Parallel(6, func(int) { inner.Add(1) }) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Parallel inside Parallel did not finish")
	}
	if inner.Load() != 36 {
		t.Errorf("%d inner calls, want 36", inner.Load())
	}
	env.Parallel(0, func(int) { t.Error("called for n = 0") })
}
