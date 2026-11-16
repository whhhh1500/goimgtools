package inflate

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/iotest"
)

// countingReader counts what is read from it, as image/png's IDAT reader
// would see it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// result is what a reader makes of a stream: the data, whether it failed,
// and how much of the input it read.
type result struct {
	data   []byte
	failed bool
	read   int
}

func stdlib(stream []byte, wrap func(io.Reader) io.Reader) result {
	c := &countingReader{r: bytes.NewReader(stream)}
	var r io.Reader = c
	if wrap != nil {
		r = wrap(c)
	}
	zr, err := zlib.NewReader(r)
	if err != nil {
		return result{failed: true, read: c.n}
	}
	data, err := io.ReadAll(zr)
	return result{data, err != nil, c.n}
}

func ours(stream []byte, wrap func(io.Reader) io.Reader) result {
	c := &countingReader{r: bytes.NewReader(stream)}
	var r io.Reader = c
	if wrap != nil {
		r = wrap(c)
	}
	zr, err := NewReader(r)
	if err != nil {
		return result{failed: true, read: c.n}
	}
	data, err := io.ReadAll(zr)
	return result{data, err != nil, c.n}
}

func same(t *testing.T, what string, stream []byte) {
	t.Helper()
	for _, w := range []struct {
		name string
		wrap func(io.Reader) io.Reader
	}{{"", nil}, {"one byte", iotest.OneByteReader}, {"half", iotest.HalfReader}} {
		want, got := stdlib(stream, w.wrap), ours(stream, w.wrap)
		if want.failed != got.failed || !bytes.Equal(want.data, got.data) && !want.failed {
			t.Fatalf("%s %s: failed %v, %d bytes; want failed %v, %d bytes", what, w.name, got.failed, len(got.data), want.failed, len(want.data))
		}
		if !want.failed && want.read != got.read {
			t.Fatalf("%s %s: read %d bytes of input, compress/zlib %d", what, w.name, got.read, want.read)
		}
	}
}

func compress(data []byte, level int) []byte {
	var b bytes.Buffer
	w, _ := zlib.NewWriterLevel(&b, level)
	w.Write(data)
	w.Close()
	return b.Bytes()
}

// samples are data of the kinds a PNG holds and some that test the corners:
// noise, smooth gradients, runs of every period from 1 to 9, long and short
// matches, and an empty one.
func samples() map[string][]byte {
	r := rand.New(rand.NewSource(1))
	m := map[string][]byte{"empty": nil, "one": {7}}
	noise := make([]byte, 300000)
	r.Read(noise)
	m["noise"] = noise
	smooth := make([]byte, 400000)
	for i := range smooth {
		smooth[i] = byte(i/97 + i%13 + r.Intn(3))
	}
	m["smooth"] = smooth
	for p := 1; p <= 9; p++ {
		run := make([]byte, 100000+p)
		for i := range run {
			run[i] = byte(i%p*31 + 5)
			if i%5000 == 4999 {
				run[i] ^= 0x55
			}
		}
		m["period"+string(rune('0'+p))] = run
	}
	text := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog; "), 9000)
	for i := 0; i < len(text); i += 777 {
		text[i] = byte(r.Intn(256))
	}
	m["text"] = text
	return m
}

// TestSameAsZlib: every sample at every level (stored, fixed and dynamic
// blocks, long and short) decompresses to the same bytes as compress/zlib
// gives, reading the same input, through readers that return all, half or
// one byte at a time.
func TestSameAsZlib(t *testing.T) {
	for name, data := range samples() {
		for _, level := range []int{0, 1, 2, 5, 6, 9, -2} {
			same(t, name, compress(data, level))
		}
	}
}

// TestPNGData decompresses the image data of the PNG files in Go's test
// corpus, and that of PNG files written by image/png.
func TestPNGData(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(runtime.GOROOT(), "src", "image", "png", "testdata", "pngsuite", "*.png"))
	if len(files) == 0 {
		t.Skip("no PNG test data in GOROOT")
	}
	n := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if idat := idatOf(data); idat != nil {
			same(t, filepath.Base(f), idat)
			n++
		}
	}
	m := image.NewNRGBA(image.Rect(0, 0, 700, 500))
	for i := range m.Pix {
		m.Pix[i] = byte(i/2800 + i%4*40)
	}
	for _, lv := range []png.CompressionLevel{png.DefaultCompression, png.BestSpeed, png.BestCompression, png.NoCompression} {
		var b bytes.Buffer
		(&png.Encoder{CompressionLevel: lv}).Encode(&b, m)
		same(t, "encoded", idatOf(b.Bytes()))
		n++
	}
	t.Logf("%d streams", n)
}

// idatOf returns the data of the IDAT chunks of a PNG file.
func idatOf(data []byte) []byte {
	var out []byte
	for i := 8; i+8 <= len(data); {
		n := int(uint32(data[i])<<24 | uint32(data[i+1])<<16 | uint32(data[i+2])<<8 | uint32(data[i+3]))
		if i+12+n > len(data) {
			break
		}
		if string(data[i+4:i+8]) == "IDAT" {
			out = append(out, data[i+8:i+8+n]...)
		}
		i += 12 + n
	}
	return out
}

// TestCorrupt: with bytes changed or cut off, a stream fails where
// compress/zlib fails, and gives the same data and reads the same input
// where it does not; trailing bytes after a stream are left as
// compress/zlib leaves them.
func TestCorrupt(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	all := samples()
	for _, name := range []string{"smooth", "text", "period3", "noise", "one"} {
		for _, level := range []int{0, 1, 6, 9, -2} {
			data := all[name]
			s := compress(data[:min(len(data), 40000)], level)
			for k := 0; k < 300; k++ {
				bad := append([]byte(nil), s...)
				switch k % 3 {
				case 0:
					bad[r.Intn(len(bad))] ^= byte(1 << r.Intn(8))
				case 1:
					bad[r.Intn(len(bad))] = byte(r.Intn(256))
				default:
					bad = bad[:r.Intn(len(bad))]
				}
				same(t, name, bad)
			}
			// Trailing data, a little and more than a fill.
			same(t, fmt.Sprint(name, " level ", level, " trailing"), append(append([]byte(nil), s...), 1, 2, 3))
			same(t, fmt.Sprint(name, " level ", level, " trailing 9000"), append(append([]byte(nil), s...), make([]byte, 9000)...))
		}
	}
}

// TestHeader: the header errors compress/zlib gives.
func TestHeader(t *testing.T) {
	for _, c := range []struct {
		in   []byte
		want error
	}{
		{nil, io.ErrUnexpectedEOF},
		{[]byte{0x78}, io.ErrUnexpectedEOF},
		{[]byte{0x79, 0x9c}, zlib.ErrHeader},
		{[]byte{0x78, 0x9d}, zlib.ErrHeader},
		{[]byte{0x88, 0x98}, zlib.ErrHeader},
		{[]byte{0x78, 0xbb, 0, 0, 0, 2}, zlib.ErrDictionary},
	} {
		_, werr := zlib.NewReader(bytes.NewReader(c.in))
		_, err := NewReader(bytes.NewReader(c.in))
		if !errors.Is(err, c.want) || !errors.Is(werr, c.want) {
			t.Errorf("% x: %v (compress/zlib %v), want %v", c.in, err, werr, c.want)
		}
	}
	// A checksum that differs.
	s := compress([]byte("hello, hello, hello"), 6)
	s[len(s)-1] ^= 1
	if _, err := io.ReadAll(must(NewReader(bytes.NewReader(s)))); err != zlib.ErrChecksum {
		t.Errorf("bad checksum: %v", err)
	}
}

func must(r *Reader, err error) *Reader {
	if err != nil {
		panic(err)
	}
	return r
}

// TestBuild: the decode table of any complete code (random binary trees of
// 2 to 288 leaves, 2 to 32 for distances, no deeper than 15) fits, and
// gives every symbol for its code.
func TestBuild(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	var lt [litlenEnough]uint32
	var dt [distEnough]uint32
	var sorted [288]uint16
	for k := 0; k < 4000; k++ {
		for _, c := range []struct {
			t    []uint32
			n    int
			root int
			sym  []uint32
		}{{lt[:], 288, litlenBits, litlenSym[:]}, {dt[:], 32, distBits, distSym[:]}} {
			leaves := 2 + r.Intn(c.n-1)
			depth := []int{1, 1}
			for len(depth) < leaves {
				// Deep trees: split the deepest leaves more often.
				i := r.Intn(len(depth))
				if j := r.Intn(len(depth)); depth[j] > depth[i] {
					i = j
				}
				if depth[i] < 15 {
					depth[i]++
					depth = append(depth, depth[i])
				}
			}
			lens := make([]uint8, c.n)
			for i, s := range r.Perm(c.n)[:leaves] {
				lens[s] = uint8(depth[i])
			}
			if !build(c.t, lens, c.root, c.sym, sorted[:]) {
				t.Fatalf("lengths %v: rejected", lens)
			}
			// The canonical codes, and the entry each finds.
			code, next := 0, [17]int{}
			for l := 1; l <= 15; l++ {
				next[l] = code
				for _, x := range lens {
					if int(x) == l {
						code++
					}
				}
				code <<= 1
			}
			for s, l := range lens {
				if l == 0 {
					continue
				}
				v := next[l]
				next[l]++
				var bits uint32
				for b := 0; b < int(l); b++ {
					bits |= uint32(v>>(int(l)-1-b)&1) << b
				}
				e := c.t[bits&(1<<c.root-1)]
				sl := uint32(l)
				if int(l) > c.root {
					if e&fSubtable == 0 {
						t.Fatalf("lengths %v: symbol %d not in a subtable", lens, s)
					}
					e = c.t[e>>16+bits>>c.root&(1<<(e>>8&15)-1)]
					sl -= uint32(c.root)
				}
				if want := c.sym[s] + sl<<8 + sl; e != want {
					t.Fatalf("lengths %v: symbol %d: entry %x, want %x", lens, s, e, want)
				}
			}
		}
	}
}

// FuzzInflate compares with compress/zlib on any input.
func FuzzInflate(f *testing.F) {
	for _, data := range samples() {
		if len(data) < 5000 {
			f.Add(compress(data, 6))
		} else {
			f.Add(compress(data[:5000], 6))
		}
	}
	f.Add(compress([]byte("abcabcabcabd"), 0))
	f.Add(compress([]byte("abcabcabcabd"), -2))
	f.Fuzz(func(t *testing.T, s []byte) {
		want, got := stdlib(s, nil), ours(s, nil)
		if want.failed != got.failed || !want.failed && (!bytes.Equal(want.data, got.data) || want.read != got.read) {
			t.Fatalf("failed %v, %d bytes, read %d; want failed %v, %d bytes, read %d", got.failed, len(got.data), got.read, want.failed, len(want.data), want.read)
		}
	})
}

// TestEndWithData: the end of the stream (io.EOF, a checksum that differs, a
// trailer cut short) comes with the last data as compress/zlib gives it, so
// that a reader stopping at the length it expects (as x/image/tiff does with
// io.LimitReader) sees the same: with it, unless the length is a multiple
// of 32KB.
func TestEndWithData(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	sizes := []int{1, 1000, 70000, 200000}
	for k := 1; k <= 7; k++ {
		for _, d := range []int{-258, -1, 0, 1, 2, 3, 100, 257} {
			sizes = append(sizes, k*32768+d)
		}
	}
	for _, n := range sizes {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(r.Intn(4) + i/300)
		}
		for _, level := range []int{0, 1, 2, 6, 9, -2} {
			good := compress(data, level)
			bad := append([]byte(nil), good...)
			bad[len(bad)-1] ^= 1
			short := good[:len(good)-2]
			for _, s := range [][]byte{good, bad, short} {
				zr, _ := zlib.NewReader(bytes.NewReader(s))
				want, werr := io.ReadAll(io.LimitReader(zr, int64(n)))
				z, err := NewReader(bytes.NewReader(s))
				if err != nil {
					t.Fatal(err)
				}
				got, gerr := io.ReadAll(io.LimitReader(z, int64(n)))
				if !bytes.Equal(got, want) || (werr == nil) != (gerr == nil) {
					t.Errorf("%d bytes, level %d: error %v, compress/zlib %v", n, level, gerr, werr)
				}
			}
		}
	}
}
