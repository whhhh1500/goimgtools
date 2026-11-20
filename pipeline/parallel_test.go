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

// TestGo: work handed to Go runs on a helper while one is free and on the
// caller otherwise, never on more helpers than given; its panic comes back
// from the wait function.
func TestGo(t *testing.T) {
	env := NewEnv(0, t.TempDir())
	ran := false
	env.Go(func() { ran = true })() // no helpers: at once
	if !ran {
		t.Fatal("without helpers fn did not run at once")
	}
	env.SetHelpers(2)
	var busy, worst atomic.Int32
	var waits []func()
	for i := 0; i < 20; i++ {
		waits = append(waits, env.Go(func() {
			n := busy.Add(1)
			for w := worst.Load(); n > w && !worst.CompareAndSwap(w, n); w = worst.Load() {
			}
			time.Sleep(time.Millisecond)
			busy.Add(-1)
		}))
	}
	for _, w := range waits {
		w()
	}
	// Two helpers and the caller.
	if w := worst.Load(); w > 3 {
		t.Errorf("%d at once, at most 3 may", w)
	}
	if len(env.helpers) != 2 {
		t.Errorf("%d of 2 helpers given back", len(env.helpers))
	}
	// A panic on a helper (both are free again) comes back from the wait.
	wait := env.Go(func() { time.Sleep(time.Millisecond); panic("boom") })
	func() {
		defer func() {
			v := recover()
			var p *PanicError
			if err, ok := v.(error); !ok || !errors.As(err, &p) || p.Value != "boom" {
				t.Errorf("panic came back as %v", v)
			}
		}()
		wait()
		t.Error("panic not raised by wait")
	}()
	if len(env.helpers) != 2 {
		t.Errorf("after the panic: %d of 2 helpers given back", len(env.helpers))
	}
}

// TestTryGo: TryGo runs work on a free helper and says so; with none free,
// or without helpers, it runs nothing; its panic comes back from the wait.
func TestTryGo(t *testing.T) {
	env := NewEnv(0, t.TempDir())
	if _, ok := env.TryGo(func() { t.Error("ran without helpers") }); ok {
		t.Error("ok without helpers")
	}
	env.SetHelpers(1)
	hold, started := make(chan struct{}), make(chan struct{})
	wait, ok := env.TryGo(func() { close(started); <-hold })
	if !ok {
		t.Fatal("not run with a helper free")
	}
	<-started
	if _, ok := env.TryGo(func() { t.Error("ran with no helper free") }); ok {
		t.Error("ok with no helper free")
	}
	close(hold)
	wait()
	if len(env.helpers) != 1 {
		t.Errorf("%d of 1 helper given back", len(env.helpers))
	}
	wait, ok = env.TryGo(func() { panic("boom") })
	if !ok {
		t.Fatal("not run with the helper free again")
	}
	func() {
		defer func() {
			v := recover()
			var p *PanicError
			if err, ok := v.(error); !ok || !errors.As(err, &p) || p.Value != "boom" {
				t.Errorf("panic came back as %v", v)
			}
		}()
		wait()
		t.Error("panic not raised by wait")
	}()
	if len(env.helpers) != 1 {
		t.Errorf("after the panic: %d of 1 helper given back", len(env.helpers))
	}
}

// TestParallelTakesFreedHelpers: helpers that become free while calls are
// left take some of them. Both helpers are busy when Parallel starts; its
// first call (on the caller) frees them.
func TestParallelTakesFreedHelpers(t *testing.T) {
	env := NewEnv(0, t.TempDir())
	env.SetHelpers(2)
	hold := make(chan struct{})
	w1, w2 := env.Go(func() { <-hold }), env.Go(func() { <-hold })
	var first atomic.Bool
	var busy, worst atomic.Int32
	env.Parallel(20, func(int) {
		if first.CompareAndSwap(false, true) {
			close(hold)
			w1()
			w2()
		}
		n := busy.Add(1)
		for w := worst.Load(); n > w && !worst.CompareAndSwap(w, n); w = worst.Load() {
		}
		time.Sleep(5 * time.Millisecond)
		busy.Add(-1)
	})
	if w := worst.Load(); w < 2 || w > 3 {
		t.Errorf("%d calls at once, want 2 or 3", w)
	}
	if len(env.helpers) != 2 {
		t.Errorf("%d of 2 helpers given back", len(env.helpers))
	}
}
