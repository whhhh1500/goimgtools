package ops

import (
	"bytes"
	"image"
	"io"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/internal/watermark"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestWatermarkKept: what each goroutine that shares the rows keeps of the
// window's rows (watermark.NewRowKept) is counted in the limit while the
// stage works and given back when it is closed; under a limit without room
// for it nothing is kept, and the marked picture is the same either way.
func TestWatermarkKept(t *testing.T) {
	const w, h = 640, 480
	photo := testutil.Natural(w, h, 9)
	key, o := watermark.NewKey("k"), watermark.Options{Density: 1} // a window of 17 rows
	m, err := watermark.NewMarker([]byte("mark"), key, o, w, h)
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	for _, c := range []struct {
		limit   int64
		helpers int
		kept    int64 // the goroutines that keep rows
	}{{0, 0, 1}, {0, 3, 4}, {1 << 20, 3, 0}, {1 << 20, 0, 0}} {
		env := pipeline.NewEnv(c.limit, t.TempDir())
		env.SetHelpers(c.helpers)
		out, err := Watermark(testutil.ChoppedEnv(photo, 16, env), []byte("mark"), key, o)
		if err != nil {
			t.Fatal(err)
		}
		ws := out.(*windowStage)
		rows := ws.reserved // the stage's own rows, before any row is made
		var got []byte
		for {
			s, err := out.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("limit %d, %d helpers: %v", c.limit, c.helpers, err)
			}
			n := s.(*image.NRGBA)
			got = append(got, n.Pix[:n.Rect.Dy()*n.Stride]...)
		}
		if want == nil {
			want = got
		}
		if !bytes.Equal(got, want) {
			t.Errorf("limit %d, %d helpers: not the picture marked without them", c.limit, c.helpers)
		}
		if len(ws.rows) != c.helpers+1 {
			t.Fatalf("%d helpers: %d goroutines shared the rows", c.helpers, len(ws.rows))
		}
		if kept := ws.reserved - rows; kept != c.kept*m.KeptBytes() {
			t.Errorf("limit %d, %d helpers: %d bytes reserved for kept rows, want %d times %d", c.limit, c.helpers, kept, c.kept, m.KeptBytes())
		}
		if used := env.Budget.Used(); used < ws.reserved {
			t.Errorf("limit %d, %d helpers: %d bytes in the budget, the stage says %d", c.limit, c.helpers, used, ws.reserved)
		}
		out.Close()
		if used := env.Budget.Used(); used != 0 {
			t.Errorf("limit %d, %d helpers: %d bytes left reserved", c.limit, c.helpers, used)
		}
	}
}
