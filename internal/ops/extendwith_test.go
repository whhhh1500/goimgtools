package ops

import (
	"image"
	"io"
	"os"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestExtendWithRows: every pixel of the result is the source pixel its
// place shows (the edge copied, the image repeated, or mirrored), for
// borders of nothing, one row, the image's height less one, its height,
// one more and several times it, above and below; a grey picture and one
// with colours and transparency, in strips of other sizes; with room to
// keep rows in memory and with so little that the store is a temporary
// file. Where only the rows of the border are needed again (a mirrored
// border no taller than the image, the image repeated below it) the store
// holds those and no more. Nothing stays reserved, and no file is left.
func TestExtendWithRows(t *testing.T) {
	const sw, sh = 37, 23
	// The pictures are parts of larger ones: their rows do not start at 0.
	all := testutil.Images(sw+7, sh+9)
	var onDisk, inMemory int
	defer func() {
		if onDisk == 0 || inMemory == 0 {
			t.Errorf("the store was a file %d times and in memory %d times: both were to be tried", onDisk, inMemory)
		}
	}()
	for _, name := range []string{"gray", "nrgba"} {
		m := all[name].(interface {
			SubImage(image.Rectangle) image.Image
		}).SubImage(image.Rect(3, 5, 3+sw, 5+sh))
		// The picture as the rows the stage works on: grey levels, or NRGBA.
		bpp := 4
		if name == "gray" {
			bpp = 1
		}
		ref, err := pipeline.Collect(pipeline.Map(pipeline.FromImage(m, nil), func(*image.NRGBA) error { return nil }))
		if err != nil {
			t.Fatal(err)
		}
		whole := ref.(*image.NRGBA)
		if whole.Rect != image.Rect(3, 5, 3+sw, 5+sh) {
			t.Fatalf("the picture is at %v", whole.Rect)
		}
		heights := []int{0, 1, 5, sh - 1, sh, sh + 1, 3*sh + 2}
		for how := ExtendCopy; how <= ExtendMirror; how++ {
			for _, top := range heights {
				for _, bottom := range heights {
					for _, limit := range []int64{0, 256 << 10} {
						dir := t.TempDir()
						env := pipeline.NewEnv(limit, dir)
						src, err := pipeline.Rechunk(pipeline.FromImage(m, env), 1+(top+2*bottom)%7)
						if err != nil {
							t.Fatal(err)
						}
						left, right := (top+bottom)%5, 2*sw+1-bottom%3
						out, err := ExtendWith(src, left, top, right, bottom, how)
						if err != nil {
							t.Fatal(err)
						}
						e := out.(*extendFrom)
						if e.bpp != bpp {
							t.Fatalf("%s: %d bytes a pixel", name, e.bpp)
						}
						w, h := sw+left+right, sh+top+bottom
						y := 0
						for {
							s, err := out.Next()
							if err == io.EOF {
								break
							}
							if err != nil {
								t.Fatalf("%s, way %d, %d above, %d below, limit %d: %v", name, how, top, bottom, limit, err)
							}
							sp, stride, sbpp, r, _ := pipeline.Pix(s)
							if r.Min.Y != y || r.Dx() != w || sbpp != bpp {
								t.Fatalf("strip %v of %d bytes a pixel at row %d of a picture %d wide", r, sbpp, y, w)
							}
							for ; y < r.Max.Y; y++ {
								sy := edgeIndex(y-top, sh, how)
								for x := 0; x < w; x++ {
									sx := edgeIndex(x-left, sw, how)
									got, want := sp[(y-r.Min.Y)*stride+x*bpp:][:bpp], whole.Pix[whole.PixOffset(3+sx, 5+sy):][:4]
									if bpp == 1 && got[0] != want[0] || bpp == 4 && string(got) != string(want) {
										t.Fatalf("%s, way %d, %d above, %d below, limit %d: at %d,%d %v, the source's %d,%d is %v", name, how, top, bottom, limit, x, y, got, sx, sy, want)
									}
								}
							}
						}
						if y != h {
							t.Fatalf("%d rows of %d", y, h)
						}
						// What was kept: nothing where the rows come in order, the
						// rows of the border where they are enough, else the image.
						whole := int64(sw * sh * bpp)
						kept, held := int64(-1), false
						switch {
						case how == ExtendCopy || top == 0 && bottom == 0:
						case how == ExtendMirror && top <= sh && bottom <= sh:
							kept = int64(min(top+bottom, sh) * sw * bpp)
						case how == ExtendRepeat && top == 0 && bottom <= sh:
							kept = int64(bottom * sw * bpp)
						default:
							kept, held = whole, true
						}
						// In a file (never with the default limit, which has room
						// for the image), the store of the rows kept is asked for
						// as one too large for memory; only those rows are written.
						share := env.Budget.Limit() / pipeline.StagingShare
						size := kept
						if kept >= 0 && e.sc != nil && e.sc.OnDisk() && !e.held {
							size = max(kept, share+1)
						}
						switch {
						case kept < 0 && e.sc != nil:
							t.Errorf("way %d, %d above, %d below: a store of %d bytes for rows that come in order", how, top, bottom, e.sc.Size())
						case kept >= 0 && e.sc == nil:
							t.Errorf("%s, way %d, %d above, %d below: no store, want one of %d bytes", name, how, top, bottom, kept)
						case kept >= 0 && e.sc.Size() != size:
							t.Errorf("%s, way %d, %d above, %d below, limit %d: a store of %d bytes, want one of %d (the image is %d)", name, how, top, bottom, limit, e.sc.Size(), size, whole)
						case kept >= 0 && e.sc.OnDisk() && limit == 0, kept > share && !e.sc.OnDisk():
							t.Errorf("%s, way %d, %d above, %d below, limit %d: %d bytes kept, in a file %v", name, how, top, bottom, limit, kept, e.sc.OnDisk())
						case e.held != held:
							t.Errorf("%s, way %d, %d above, %d below: the whole image held: %v", name, how, top, bottom, e.held)
						}
						if kept >= 0 && e.sc != nil && !e.held {
							if e.sc.OnDisk() {
								onDisk++
							} else {
								inMemory++
							}
						}
						if err := out.Close(); err != nil {
							t.Fatal(err)
						}
						if env.Budget.Used() != 0 {
							t.Errorf("way %d, %d above, %d below: %d bytes left reserved", how, top, bottom, env.Budget.Used())
						}
						if left, _ := os.ReadDir(dir); len(left) != 0 {
							t.Errorf("way %d, %d above, %d below: %d files left", how, top, bottom, len(left))
						}
					}
				}
			}
		}
	}
}

// TestExtendWithStoreWhere: the rows kept go where the whole image would
// have gone, so that keeping less never takes more memory: an image too
// large to be staged in memory has its few rows in a file, as large as
// what is kept and no larger written; with a limit that has room for the
// image they are in memory, only they.
func TestExtendWithStoreWhere(t *testing.T) {
	const sw, sh, top, bottom = 1500, 1400, 3, 5
	m := image.NewNRGBA(image.Rect(0, 0, sw, sh)) // 8.4MB: over an eighth of the default limit
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			p := m.Pix[m.PixOffset(x, y):][:4]
			p[0], p[1], p[2], p[3] = uint8(x), uint8(y), uint8(x/256+16*(y/256)), 255
		}
	}
	rows := int64(top+bottom) * sw * 4
	for _, limit := range []int64{0, 256 << 20} {
		dir := t.TempDir()
		env := pipeline.NewEnv(limit, dir)
		out, err := ExtendWith(pipeline.FromImage(m, env), 0, top, 0, bottom, ExtendMirror)
		if err != nil {
			t.Fatal(err)
		}
		e := out.(*extendFrom)
		y := 0
		for {
			s, err := out.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			sp, stride, _, r, _ := pipeline.Pix(s)
			for ; y < r.Max.Y; y++ {
				sy := edgeIndex(y-top, sh, ExtendMirror)
				if got, want := sp[(y-r.Min.Y)*stride:][:4*sw], m.Pix[sy*m.Stride:][:4*sw]; string(got) != string(want) {
					t.Fatalf("limit %d: row %d is not the source's row %d", limit, y, sy)
				}
			}
			if y == r.Max.Y && r.Min.Y == 0 {
				// While it works: where the store is, and how large.
				share := env.Budget.Limit() / pipeline.StagingShare
				switch file, size := e.sc.OnDisk(), e.sc.Size(); {
				case limit == 0 && (!file || size != share+1):
					t.Errorf("the default limit: the store in a file %v, of %d bytes; want a file asked for as %d", file, size, share+1)
				case limit != 0 && (file || size != rows):
					t.Errorf("limit %d: the store in a file %v, of %d bytes; want %d in memory", limit, file, size, rows)
				}
				if files, _ := os.ReadDir(dir); limit == 0 && len(files) != 1 {
					t.Errorf("the default limit: %d temporary files while it works", len(files))
				}
			}
		}
		if y != sh+top+bottom {
			t.Fatalf("%d rows", y)
		}
		// The staged data stayed within what the image itself would have
		// been allowed: nothing near the image's size was ever reserved.
		if peak := env.Budget.Peak(); limit == 0 && peak > 4<<20 {
			t.Errorf("the default limit: %d bytes reserved at the most", peak)
		}
		out.Close()
		if env.Budget.Used() != 0 {
			t.Errorf("limit %d: %d bytes left reserved", limit, env.Budget.Used())
		}
		if files, _ := os.ReadDir(dir); len(files) != 0 {
			t.Errorf("limit %d: %d files left", limit, len(files))
		}
	}
}

// failingRows is a source that fails at a row.
type failingRows struct {
	pipeline.Source
	at  int
	err error
}

func (f *failingRows) Next() (image.Image, error) {
	s, err := f.Source.Next()
	if err == nil && s.Bounds().Max.Y > f.at {
		return nil, f.err
	}
	return s, err
}

// TestExtendWithError: an error of the source is the stage's at the strip
// that needs the row, and again when it is asked once more (not a strip of
// what was left in the row's buffer).
func TestExtendWithError(t *testing.T) {
	m := testutil.Images(40, 30)["nrgba"]
	for how := ExtendCopy; how <= ExtendMirror; how++ {
		for _, at := range []int{0, 3, 17, 29} {
			src := &failingRows{Source: testutil.Chopped(m, 1), at: at, err: io.ErrClosedPipe}
			out, err := ExtendWith(src, 2, 4, 3, 5, how)
			if err != nil {
				t.Fatal(err)
			}
			var failed error
			for failed == nil {
				_, failed = out.Next()
			}
			if failed != io.ErrClosedPipe {
				t.Errorf("way %d, the source failing at row %d: %v", how, at, failed)
			}
			if _, again := out.Next(); again != io.ErrClosedPipe {
				t.Errorf("way %d, the source failing at row %d, asked again: %v", how, at, again)
			}
			out.Close()
		}
	}
}
