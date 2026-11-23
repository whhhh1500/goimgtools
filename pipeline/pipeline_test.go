package pipeline_test

import (
	"bytes"
	"errors"
	"image"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

func tempFiles(t *testing.T, dir string) int {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	return len(names)
}

func TestBudget(t *testing.T) {
	b := pipeline.NewBudget(1000)
	if err := b.Reserve(600, "a"); err != nil {
		t.Fatal(err)
	}
	if err := b.Reserve(500, "b"); !errors.Is(err, pipeline.ErrMemoryLimit) {
		t.Fatalf("over the limit: err = %v, want ErrMemoryLimit", err)
	}
	// TryReserve only succeeds while what is reserved stays within an
	// eighth of the limit.
	if b.TryReserve(1) {
		t.Fatal("TryReserve succeeded with 600 of 1000 in use")
	}
	b.Release(600)
	if b.TryReserve(126) {
		t.Fatal("TryReserve took more than an eighth of the limit")
	}
	if !b.TryReserve(100) || b.TryReserve(26) || !b.TryReserve(25) {
		t.Fatal("TryReserve does not allow exactly an eighth of the limit")
	}
	b.Release(125)
	if b.Used() != 0 || b.Peak() != 600 {
		t.Fatalf("used %d peak %d, want 0 and 600", b.Used(), b.Peak())
	}
	if pipeline.NewBudget(0).Limit() != pipeline.DefaultMemoryLimit {
		t.Fatal("zero limit is not the default")
	}
}

func TestSpill(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for _, limit := range []int64{0, 3 << 20} { // all in memory; forced to disk part way
		dir := t.TempDir()
		env := pipeline.NewEnv(limit, dir)
		s := pipeline.NewSpill(env)
		var want []byte
		for len(want) < 5<<20 {
			chunk := make([]byte, 1+r.Intn(200000))
			r.Read(chunk)
			if _, err := s.Write(chunk); err != nil {
				t.Fatal(err)
			}
			want = append(want, chunk...)
			// Reads may be interleaved with writes.
			if len(want) > 100 {
				got := make([]byte, 100)
				off := int64(r.Intn(len(want) - 100))
				if _, err := s.ReadAt(got, off); err != nil || !bytes.Equal(got, want[off:off+100]) {
					t.Fatalf("limit %d: interleaved read at %d wrong (err %v)", limit, off, err)
				}
			}
		}
		if s.Size() != int64(len(want)) {
			t.Fatalf("size %d, want %d", s.Size(), len(want))
		}
		if onDisk := s.OnDisk(); onDisk != (limit != 0) {
			t.Fatalf("limit %d: onDisk = %v", limit, onDisk)
		}
		if limit != 0 && env.Budget.Peak() > limit {
			t.Fatalf("peak %d above the limit %d", env.Budget.Peak(), limit)
		}
		for i := 0; i < 200; i++ {
			off := int64(r.Intn(len(want)))
			n := 1 + r.Intn(300000)
			got := make([]byte, n)
			k, err := s.ReadAt(got, off)
			end := min(int(off)+n, len(want))
			if !bytes.Equal(got[:k], want[off:end]) || k != end-int(off) {
				t.Fatalf("ReadAt(%d, %d) returned %d wrong bytes", off, n, k)
			}
			if (k < n) != (err == io.EOF) {
				t.Fatalf("ReadAt(%d, %d) = %d, %v", off, n, k, err)
			}
		}
		// Sequential readers over two regions, with small and large reads.
		half := int64(len(want) / 2)
		for _, reg := range [][2]int64{{0, half}, {half, int64(len(want))}} {
			q := pipeline.NewSeqReader(s, reg[0], reg[1], 4096)
			pos := reg[0]
			for pos < reg[1] {
				n := int64(1 + r.Intn(20000))
				n = min(n, reg[1]-pos)
				got := make([]byte, n)
				if err := q.ReadFull(got); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want[pos:pos+n]) {
					t.Fatalf("SeqReader wrong at %d", pos)
				}
				pos += n
			}
			if err := q.ReadFull(make([]byte, 1)); err != io.ErrUnexpectedEOF {
				t.Fatalf("read past the region: %v", err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if n := tempFiles(t, dir); n != 0 || env.Budget.Used() != 0 {
			t.Fatalf("after Close: %d temp files, %d bytes reserved", n, env.Budget.Used())
		}
	}
}

func TestScratch(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	for _, limit := range []int64{0, 1 << 20} {
		dir := t.TempDir()
		env := pipeline.NewEnv(limit, dir)
		const size = 3 << 20
		s, err := pipeline.NewScratch(env, size)
		if err != nil {
			t.Fatal(err)
		}
		if s.OnDisk() != (limit != 0) {
			t.Fatalf("limit %d: onDisk = %v", limit, s.OnDisk())
		}
		want := make([]byte, size) // starts all zero
		for i := 0; i < 300; i++ {
			off := r.Intn(size - 70000)
			n := 1 + r.Intn(70000)
			if i%2 == 0 {
				p := make([]byte, n)
				r.Read(p)
				if _, err := s.WriteAt(p, int64(off)); err != nil {
					t.Fatal(err)
				}
				copy(want[off:], p)
			} else {
				got := make([]byte, n)
				if _, err := s.ReadAt(got, int64(off)); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want[off:off+n]) {
					t.Fatalf("limit %d: read at %d differs", limit, off)
				}
			}
		}
		if _, err := s.ReadAt(make([]byte, 10), size-5); err == nil {
			t.Fatal("read past the end succeeded")
		}
		if _, err := s.WriteAt(make([]byte, 10), -1); err == nil {
			t.Fatal("write at a negative offset succeeded")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if n := tempFiles(t, dir); n != 0 || env.Budget.Used() != 0 {
			t.Fatalf("after Close: %d temp files, %d bytes reserved", n, env.Budget.Used())
		}
	}
	// An unwritable temp directory is reported, not panicked on.
	env := pipeline.NewEnv(1<<10, filepath.Join(os.TempDir(), "goimgtools-no-such-dir"))
	if _, err := pipeline.NewScratch(env, 1<<20); err == nil {
		t.Fatal("missing temp dir: no error")
	}
}

func samePixels(t *testing.T, what string, want, got image.Image) {
	t.Helper()
	if want.Bounds() != got.Bounds() {
		t.Fatalf("%s: bounds %v, want %v", what, got.Bounds(), want.Bounds())
	}
	b := want.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if want.At(x, y) != got.At(x, y) {
				t.Fatalf("%s: pixel (%d,%d) = %v, want %v", what, x, y, got.At(x, y), want.At(x, y))
			}
		}
	}
}

// TestRechunkAndCollect re-cuts strips of awkward heights into fixed bands
// for every pixel type and checks nothing moves.
func TestRechunkAndCollect(t *testing.T) {
	for name, m := range testutil.Images(67, 131) {
		for _, in := range []int{1, 7, 16, 33, 500} {
			for _, n := range []int{8, 16} {
				env := pipeline.NewEnv(0, "")
				src, err := pipeline.Rechunk(testutil.ChoppedEnv(m, in, env), n)
				if err != nil {
					t.Fatal(err)
				}
				// Every band but the last has exactly n rows.
				b := m.Bounds()
				var bands []image.Rectangle
				out := pipeline.NewBand(m, b)
				for {
					s, err := src.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					bands = append(bands, s.Bounds())
					pipeline.CopyRows(out, s, s.Bounds().Min.Y, s.Bounds().Max.Y)
				}
				for i, r := range bands {
					wantMax := min(b.Min.Y+(i+1)*n, b.Max.Y)
					if r.Min.Y != b.Min.Y+i*n || r.Max.Y != wantMax || r.Min.X != b.Min.X || r.Max.X != b.Max.X {
						t.Fatalf("%s in=%d n=%d: band %d is %v", name, in, n, i, r)
					}
				}
				samePixels(t, name, m, out)
				src.Close()
				if env.Budget.Used() != 0 {
					t.Fatalf("%s: %d bytes still reserved", name, env.Budget.Used())
				}
			}
		}
		got, err := pipeline.Collect(pipeline.FromImage(m, nil))
		if err != nil {
			t.Fatal(err)
		}
		samePixels(t, name+" FromImage", m, got)
	}
}

func TestStripRows(t *testing.T) {
	for _, c := range []struct {
		rowBytes int64
		height   int
		want     int
	}{
		{4 * 1152, 1536, 48}, // about 256KB, a multiple of 16
		{4 * 100000, 500, 1}, // a very wide row still gets one row
		{10, 1000, 256},      // capped
		{10, 5, 5},           // never more than the image
		{0, 0, 1},
	} {
		if got := pipeline.StripRows(c.rowBytes, c.height); got != c.want {
			t.Errorf("StripRows(%d, %d) = %d, want %d", c.rowBytes, c.height, got, c.want)
		}
	}
}

// TestSpillExpect: a spill told its final size goes to disk at once when
// that size cannot fit, without filling memory first.
func TestSpillExpect(t *testing.T) {
	dir := t.TempDir()
	env := pipeline.NewEnv(4<<20, dir)
	s := pipeline.NewSpill(env)
	s.Expect(10 << 20)
	if !s.OnDisk() {
		t.Fatal("a 10MB spill under a 4MB limit stayed in memory")
	}
	if _, err := s.Write(make([]byte, 2<<20)); err != nil {
		t.Fatal(err)
	}
	if env.Budget.Peak() > 1<<20 {
		t.Fatalf("peak %d: memory was filled before going to disk", env.Budget.Peak())
	}
	s.Close()
	// Within the eighth of the limit that may be staged in memory (a spill
	// takes memory a megabyte at a time).
	roomy := pipeline.NewEnv(16<<20, dir)
	small := pipeline.NewSpill(roomy)
	small.Expect(1 << 20)
	if small.OnDisk() {
		small.Close()
		t.Fatal("a 1MB spill under a 16MB limit went to disk")
	}
	small.Close()
	if n := tempFiles(t, dir); n != 0 || env.Budget.Used() != 0 {
		t.Fatalf("after Close: %d temp files, %d bytes reserved", n, env.Budget.Used())
	}
}

// TestSpillSmall: a spill told it will hold a few kilobytes takes about
// that much memory, not a whole chunk of a megabyte (a tile's JPEG is
// staged a thousand times in a pyramid), and still holds more than it was
// told, read back the same.
func TestSpillSmall(t *testing.T) {
	env := pipeline.NewEnv(64<<20, t.TempDir())
	data := make([]byte, 3000)
	for i := range data {
		data[i] = byte(i * 7)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 100 {
		s := pipeline.NewSpill(env)
		s.Expect(int64(len(data)))
		if _, err := s.Write(data); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	runtime.ReadMemStats(&after)
	if per := (after.TotalAlloc - before.TotalAlloc) / 100; per > 64<<10 {
		t.Errorf("a 3000-byte spill allocated %d bytes", per)
	}
	// More than expected: past the first chunk's size and past a megabyte.
	s := pipeline.NewSpill(env)
	defer s.Close()
	s.Expect(100)
	big := make([]byte, 3<<20+5)
	for i := range big {
		big[i] = byte(i * 13)
	}
	for p := big; len(p) > 0; {
		k := min(len(p), 70000)
		if _, err := s.Write(p[:k]); err != nil {
			t.Fatal(err)
		}
		p = p[k:]
	}
	got := make([]byte, len(big))
	if _, err := s.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, big) {
		t.Error("read back differs")
	}
}

// failing yields the strips of src and then err instead of io.EOF after n
// strips.
type failing struct {
	pipeline.Source
	n   int
	err error
}

func (f *failing) Next() (image.Image, error) {
	if f.n == 0 {
		return nil, f.err
	}
	f.n--
	return f.Source.Next()
}

// TestAsync: a source moved to its own goroutine yields the same strips and
// errors, gives its memory back, and stops when closed early.
func TestAsync(t *testing.T) {
	for name, m := range testutil.Images(67, 131) {
		for _, in := range []int{1, 7, 16, 33, 500} {
			env := pipeline.NewEnv(0, "")
			src := pipeline.Async(testutil.ChoppedEnv(m, in, env))
			b := m.Bounds()
			out := pipeline.NewBand(m, b)
			y := b.Min.Y
			for {
				s, err := src.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if r := s.Bounds(); r.Min.Y != y || r.Dy() != min(in, b.Max.Y-y) || r.Min.X != b.Min.X || r.Max.X != b.Max.X {
					t.Fatalf("%s in=%d: strip %v at row %d", name, in, r, y)
				}
				pipeline.CopyRows(out, s, s.Bounds().Min.Y, s.Bounds().Max.Y)
				y = s.Bounds().Max.Y
			}
			if _, err := src.Next(); err != io.EOF {
				t.Fatalf("%s: second end: %v", name, err)
			}
			samePixels(t, name, m, out)
			src.Close()
			if env.Budget.Used() != 0 {
				t.Fatalf("%s: %d bytes still reserved", name, env.Budget.Used())
			}
		}
	}
	m := testutil.Images(40, 100)["nrgba"]
	// An error arrives after exactly the strips before it.
	env := pipeline.NewEnv(0, "")
	boom := errors.New("boom")
	src := pipeline.Async(&failing{Source: testutil.ChoppedEnv(m, 10, env), n: 4, err: boom})
	for i := 0; i < 4; i++ {
		if _, err := src.Next(); err != nil {
			t.Fatalf("strip %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := src.Next(); err != boom {
			t.Fatalf("after 4 strips: %v, want boom", err)
		}
	}
	src.Close()
	// Closed early, with the producer blocked on a full queue; and never read.
	early := pipeline.Async(testutil.ChoppedEnv(m, 1, env))
	early.Next()
	early.Close()
	if _, err := early.Next(); err == nil || err == io.EOF {
		t.Fatalf("Next after Close: %v", err)
	}
	pipeline.Async(testutil.ChoppedEnv(m, 1, env)).Close()
	// A limit too small for the strip buffers is reported.
	tiny := pipeline.NewEnv(1000, "")
	small := pipeline.Async(testutil.ChoppedEnv(m, 50, tiny))
	if _, err := small.Next(); !errors.Is(err, pipeline.ErrMemoryLimit) {
		t.Fatalf("tiny budget: %v", err)
	}
	small.Close()
	if env.Budget.Used() != 0 || tiny.Budget.Used() != 0 {
		t.Fatalf("%d / %d bytes still reserved", env.Budget.Used(), tiny.Budget.Used())
	}
}
