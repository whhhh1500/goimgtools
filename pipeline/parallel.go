package pipeline

import (
	"runtime/debug"
	"sync"
	"sync/atomic"
)

// SetHelpers lets the stages of the pipeline share their work with up to
// n goroutines besides their own (see Parallel); 0, the default, keeps all
// the work of a stage on the goroutine that pulls its strips. It is a
// limit for the whole pipeline: however many stages share work, no more
// than n helpers are busy at a time. Call it before the pipeline is
// built, since stages that share work set themselves up for it then.
func (e *Env) SetHelpers(n int) {
	e.helpers = nil
	if n > 0 {
		e.helpers = make(chan struct{}, n)
		for i := 0; i < n; i++ {
			e.helpers <- struct{}{}
		}
	}
}

// Helpers is what SetHelpers was given.
func (e *Env) Helpers() int {
	if e == nil {
		return 0
	}
	return cap(e.helpers)
}

// Parallel calls fn(0), fn(1), …, fn(n-1), each once, and returns when all
// of them have returned. They run at the same time on the calling
// goroutine and on as many helpers as are free at that moment (see
// SetHelpers); without helpers they run one after another, in order, on
// the calling goroutine. So the calls must not depend on each other, and
// what they write must not overlap.
//
// A helper is taken only for as long as the calls run and never waits for
// anything, so Parallel cannot block on other stages, and it may be used
// inside a call of Parallel. A panic in any call is raised again on the
// calling goroutine, as a *PanicError, once all have returned.
func (e *Env) Parallel(n int, fn func(i int)) {
	if n <= 0 {
		return
	}
	if n == 1 || e.Helpers() == 0 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	var (
		next   atomic.Int64
		failed atomic.Pointer[PanicError]
		wg     sync.WaitGroup
	)
	work := func() {
		defer func() {
			if v := recover(); v != nil {
				p, ok := v.(*PanicError)
				if !ok {
					p = &PanicError{Value: v, Stack: debug.Stack()}
				}
				failed.CompareAndSwap(nil, p)
			}
		}()
		for {
			i := next.Add(1) - 1
			if i >= int64(n) || failed.Load() != nil {
				return
			}
			fn(int(i))
		}
	}
take:
	for h := 1; h < n; h++ {
		select {
		case <-e.helpers:
			wg.Add(1)
			go func() {
				defer func() { e.helpers <- struct{}{}; wg.Done() }()
				work()
			}()
		default:
			break take // none free: the calling goroutine does the rest
		}
	}
	work()
	wg.Wait()
	if p := failed.Load(); p != nil {
		panic(p)
	}
}

// Go runs fn on a helper of the pipeline that is free, and returns a
// function that waits for fn to end. With no helper free it runs fn at
// once, on the calling goroutine, and returns a function that does
// nothing; so Go never waits for a helper, and the pipeline stays within
// the helpers it was given. A panic of fn on a helper is raised again by
// the wait function, as a *PanicError. The wait function must be called
// exactly once, before what fn uses is reused.
//
// It is for work a stage can go on without, such as compressing one block
// while the next is being filled; Parallel is for work it waits for.
func (e *Env) Go(fn func()) (wait func()) {
	if e.Helpers() > 0 {
		select {
		case <-e.helpers:
			var failed *PanicError
			done := make(chan struct{})
			go func() {
				defer func() {
					if v := recover(); v != nil {
						p, ok := v.(*PanicError)
						if !ok {
							p = &PanicError{Value: v, Stack: debug.Stack()}
						}
						failed = p
					}
					e.helpers <- struct{}{}
					close(done)
				}()
				fn()
			}()
			return func() {
				<-done
				if failed != nil {
					panic(failed)
				}
			}
		default:
		}
	}
	fn()
	return func() {}
}
