package goimgtools

import (
	"errors"
	"math"
	"strconv"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The operations on channels, after sharp's: Recomb, Unflatten,
// ExtractChannel and RemoveAlpha. They work a few rows at a time.

// Recomb mixes the channels of every pixel by a matrix, as sharp's recomb
// (to the byte): each row of matrix makes one channel of the result, the
// sum of the red, green and blue levels (and alpha, for a matrix of 4
// rows of 4) times the numbers of the row, kept within 0 to 255 with its
// fraction dropped. A matrix of 3 rows of 3 keeps transparency; the
// fourth row of a 4 x 4 matrix makes the alpha. Sepia:
//
//	m.Recomb([][]float64{
//		{0.393, 0.769, 0.189},
//		{0.349, 0.686, 0.168},
//		{0.272, 0.534, 0.131},
//	})
func (m *Image) Recomb(matrix [][]float64) *Image {
	n := len(matrix)
	if n != 3 && n != 4 {
		return m.fail(errors.New("goimgtools: Recomb takes 3 rows of 3 numbers or 4 rows of 4"))
	}
	var o recombOp
	o.alpha = n == 4
	for i, row := range matrix {
		if len(row) != n {
			return m.fail(errors.New("goimgtools: Recomb takes 3 rows of 3 numbers or 4 rows of 4"))
		}
		for j, v := range row {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return m.fail(errors.New("goimgtools: Recomb takes finite numbers"))
			}
			o.m[i][j] = v
		}
	}
	return m.add(o)
}

// Unflatten makes the white pixels transparent, as sharp's unflatten (to
// the byte): those whose grey is 255 (libvips' greyscale, by which a few
// colours a level or two off white, such as 255,255,254, are white too).
// The other pixels keep their transparency. After Threshold, every pixel
// at or above the threshold becomes transparent.
func (m *Image) Unflatten() *Image { return m.add(channelOp{name: "unflatten"}) }

// RemoveAlpha makes every pixel opaque, showing the colour it stores, as
// sharp's removeAlpha. Unlike Flatten nothing is blended with a
// background: what transparency hid (often black) shows.
func (m *Image) RemoveAlpha() *Image { return m.add(channelOp{name: "remove-alpha"}) }

// Channel is a channel of the pixels of an image, for ExtractChannel.
type Channel int

const (
	ChannelRed Channel = iota
	ChannelGreen
	ChannelBlue
	// ChannelAlpha is the opacity: 0 transparent, 255 opaque.
	ChannelAlpha
)

// ExtractChannel makes a greyscale image of one channel, as sharp's
// extractChannel: its levels become the grey levels (the alpha of an
// image without transparency is white throughout). The result has no
// transparency.
func (m *Image) ExtractChannel(c Channel) *Image {
	if c < ChannelRed || c > ChannelAlpha {
		return m.fail(errors.New("goimgtools: unknown Channel"))
	}
	return m.add(channelOp{name: "extract-channel", ch: c})
}

// ---- the operations ----

type recombOp struct {
	m     [4][4]float64
	alpha bool
}

func (o recombOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Recomb(src, o.m, o.alpha), nil
}

func (o recombOp) words() []string {
	n := 3
	if o.alpha {
		n = 4
	}
	var w []string
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			w = append(w, strconv.FormatFloat(o.m[i][j], 'g', -1, 64))
		}
	}
	return w
}

// channelOp is Unflatten, RemoveAlpha or ExtractChannel.
type channelOp struct {
	name string
	ch   Channel
}

func (o channelOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	switch o.name {
	case "unflatten":
		return ops.Unflatten(src), nil
	case "remove-alpha":
		return ops.RemoveAlpha(src), nil
	}
	return ops.Channel(src, int(o.ch)), nil
}

func (o channelOp) words() []string {
	if o.name == "extract-channel" {
		return []string{channelNames[o.ch]}
	}
	return nil
}

var channelNames = map[Channel]string{ChannelRed: "red", ChannelGreen: "green", ChannelBlue: "blue", ChannelAlpha: "alpha"}

// channelOps are the named operations of this file (see builtInOps).
var channelOps = map[string]OperationDef{
	"recomb": {Name: "recomb", Usage: "M11,M12,M13,M21,...", Help: "Recomb: mix the channels by a matrix of 3 rows of 3 numbers (or 4 of 4, with alpha), row by row",
		builtIn: func(m *Image, a []string) error {
			if len(a) != 9 && len(a) != 16 {
				return errors.New("want 9 or 16 numbers")
			}
			v, err := argFloats(a)
			if err != nil {
				return err
			}
			n := 3
			if len(v) == 16 {
				n = 4
			}
			rows := make([][]float64, n)
			for i := range rows {
				rows[i] = v[i*n : i*n+n]
			}
			m.Recomb(rows)
			return nil
		}},
	"unflatten": {Name: "unflatten", Help: "Unflatten: white pixels become transparent",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 0, 0); err != nil {
				return err
			}
			m.Unflatten()
			return nil
		}},
	"remove-alpha": {Name: "remove-alpha", Help: "RemoveAlpha: every pixel opaque, showing the colour it stores",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 0, 0); err != nil {
				return err
			}
			m.RemoveAlpha()
			return nil
		}},
	"extract-channel": {Name: "extract-channel", Usage: "CHANNEL", Help: "ExtractChannel: a greyscale image of CHANNEL: red, green, blue or alpha",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 1, 1); err != nil {
				return err
			}
			c, err := pick(channelNames, a[0], "channel")
			if err != nil {
				return err
			}
			m.ExtractChannel(c)
			return nil
		}},
}

func init() {
	for n, d := range channelOps {
		builtInOps[n] = d
	}
}
