// Package watermark hides a few bytes in an image, invisibly, and finds
// them again in a copy that was re-encoded, resized, cropped, turned,
// photographed off a screen and so on, without the original.
//
// The mark is a tile of 64 x 64 chips repeated over the luminance, each
// chip a few pixels wide, its values a mix of three signals derived from a
// key: a reference of 16 sinusoids at known frequencies and phases, whose
// peaks in the spectrum of a copy tell the tile's size, angle and shear; a
// random pattern of signs (sync), which tells where the tiles start; and
// the payload, its bits coded with a convolutional code and spread over
// the chips by the key. The tile is added where the picture hides it best
// (more in busy areas, little in flat ones). To read it, the copy's
// spectrum is flattened, the reference's peaks are searched for over
// sizes and angles, every copy of the tile is folded onto one (which adds
// up the mark and averages the picture away), the sync pattern places it,
// region by region for a photograph's perspective, and the payload is
// decoded and checked against a CRC of the key and the bytes.
package watermark

import (
	"encoding/binary"

	"github.com/whhhh1500/goimgtools/internal/sha256"
)

// Key is the hash of a user's key that every pattern is derived from.
type Key [32]byte

// NewKey derives the patterns' key from the user's.
func NewKey(key string) Key {
	return sha256.Sum256([]byte("goimgtools watermark/" + key))
}

// rng is SplitMix64 seeded from the key and a label: the same numbers on
// every platform and Go version.
type rng struct{ s uint64 }

func newRNG(k Key, label string) *rng {
	h := sha256.Sum256(append(k[:], "/"+label...))
	return &rng{binary.LittleEndian.Uint64(h[:8])}
}

func (r *rng) next() uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// intn is in [0, n); the bias of the modulus is far below anything that
// matters here.
func (r *rng) intn(n int) int { return int(r.next() % uint64(n)) }

func (r *rng) sign() float64 {
	if r.next()>>63 == 0 {
		return 1
	}
	return -1
}
