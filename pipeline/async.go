package pipeline

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"runtime/debug"
)

// asyncBuffers is how many strips an Async stage holds: one being read by
// the consumer, one waiting, one being filled.
const asyncBuffers = 3

type asyncBuf struct {
	band  image.Image // the allocation, rows starting at 0
	typ   string      // the strip type it was made for
	bytes int64
}

type asyncItem struct {
	buf  *asyncBuf
	view image.Image
	err  error
}

type async struct {
	src      Source
	env      *Env
	opaque   bool
	out      chan asyncItem
	free     chan *asyncBuf
	stop     chan struct{}
	exited   chan struct{}
	started  bool
	closed   bool
	cur      *asyncBuf
	err      error
	reserved int64 // owned by the goroutine while it runs
}

// Async runs src in its own goroutine, so the stages before it and the
// stages after it use one CPU core each. Strips are copied into a small
// fixed set of buffers (three strips), which is all the extra memory it
// needs; the producer stops and waits whenever the consumer falls behind.
// Strips, errors and their order are exactly those of src.
//
// The goroutine starts with the first Next and ends at Close.
func Async(src Source) Source {
	a := &async{
		src:    src,
		env:    EnvOf(src),
		opaque: IsOpaque(src),
		out:    make(chan asyncItem, asyncBuffers-2),
		free:   make(chan *asyncBuf, asyncBuffers),
		stop:   make(chan struct{}),
		exited: make(chan struct{}),
	}
	for i := 0; i < asyncBuffers; i++ {
		a.free <- &asyncBuf{}
	}
	return a
}

func (a *async) Env() *Env               { return a.env }
func (a *async) Bounds() image.Rectangle { return a.src.Bounds() }
func (a *async) ColorModel() color.Model { return a.src.ColorModel() }
func (a *async) Opaque() bool            { return a.opaque }

func (a *async) send(it asyncItem) bool {
	select {
	case a.out <- it:
		return true
	case <-a.stop:
		return false
	}
}

func (a *async) run() {
	defer close(a.exited)
	// A panic here would end the whole program: no caller can recover what
	// happens in a goroutine it did not start. It becomes the error of the
	// stage instead.
	defer func() {
		if v := recover(); v != nil {
			p, ok := v.(*PanicError) // raised again by Parallel
			if !ok {
				p = &PanicError{Value: v, Stack: debug.Stack()}
			}
			a.send(asyncItem{err: p})
		}
	}()
	for {
		m, err := a.src.Next()
		if err != nil {
			a.send(asyncItem{err: err})
			return
		}
		var buf *asyncBuf
		select {
		case buf = <-a.free:
		case <-a.stop:
			return
		}
		r := m.Bounds()
		typ := fmt.Sprintf("%T", m)
		if buf.band == nil || buf.typ != typ || buf.band.Bounds().Dy() < r.Dy() ||
			buf.band.Bounds().Min.X != r.Min.X || buf.band.Bounds().Max.X != r.Max.X {
			a.env.Budget.Release(buf.bytes)
			a.reserved -= buf.bytes
			buf.band, buf.bytes = nil, 0
			full := image.Rect(r.Min.X, 0, r.Max.X, r.Dy())
			need := BandBytes(m, full)
			if err := a.env.Budget.Reserve(need, "strip buffer between threads"); err != nil {
				a.send(asyncItem{err: err})
				return
			}
			a.reserved += need
			buf.band, buf.typ, buf.bytes = NewBand(m, full), typ, need
		}
		view := Relabel(buf.band, r)
		CopyRows(view, m, r.Min.Y, r.Max.Y)
		if !a.send(asyncItem{buf: buf, view: view}) {
			return
		}
	}
}

func (a *async) Next() (image.Image, error) {
	if a.err != nil {
		return nil, a.err
	}
	if !a.started {
		a.started = true
		go a.run()
	}
	if a.cur != nil {
		a.free <- a.cur // never blocks: there are asyncBuffers slots
		a.cur = nil
	}
	it := <-a.out
	if it.err != nil {
		a.err = it.err
		return nil, it.err
	}
	a.cur = it.buf
	return it.view, nil
}

func (a *async) Close() error {
	if !a.closed {
		a.closed = true
		close(a.stop)
		if a.started {
			<-a.exited
		}
		a.env.Budget.Release(a.reserved)
		a.reserved = 0
	}
	if a.err == nil {
		a.err = errors.New("goimgtools: source closed")
	}
	return a.src.Close()
}

// PanicError is the error a panic inside a run is turned into: a bug in a
// decoder, an operation or an encoder (the library's or one supplied to
// it) that an input brought out. A run never panics; it returns this, so
// that a program processing files it does not control keeps running, and
// working storage is cleaned up as after any other error.
type PanicError struct {
	Value any    // what was passed to panic
	Stack []byte // the stack of the goroutine that panicked
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("goimgtools: internal error: panic: %v", e.Value)
}
