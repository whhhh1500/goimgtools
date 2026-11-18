package watermark

import (
	"hash/crc32"
	"math"
)

// MaxPayload is the most bytes a mark carries.
const MaxPayload = 32

// The payload goes out with a CRC-32 of the key and the payload (so that a
// mark read with another key, or noise, is not taken for one), coded with
// the convolutional code of constraint length 7 and rate 1/3 (generators
// 133, 171 and 165 octal), ended by six zeros.
const (
	constraint = 7
	states     = 1 << (constraint - 1)
)

var generators = [3]uint32{0o133, 0o171, 0o165}

// frameBits is the payload and its check, as bits.
func frameBits(payload []byte, k Key) []uint8 {
	crc := checksum(payload, k)
	data := append(append([]byte{}, payload...), byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	bits := make([]uint8, 0, 8*len(data))
	for _, b := range data {
		for i := 7; i >= 0; i-- {
			bits = append(bits, b>>i&1)
		}
	}
	return bits
}

func checksum(payload []byte, k Key) uint32 {
	h := crc32.NewIEEE()
	h.Write(k[:])
	h.Write(payload)
	return h.Sum32()
}

// codedLen is the number of coded bits of a payload of n bytes.
func codedLen(n int) int { return 3 * (8*(n+4) + constraint - 1) }

func parity(v uint32) uint8 {
	v ^= v >> 16
	v ^= v >> 8
	v ^= v >> 4
	v ^= v >> 2
	v ^= v >> 1
	return uint8(v & 1)
}

// encode is the convolutional code of bits, with the tail.
func encode(bits []uint8) []uint8 {
	out := make([]uint8, 0, 3*(len(bits)+constraint-1))
	var state uint32
	for i := 0; i < len(bits)+constraint-1; i++ {
		var b uint32
		if i < len(bits) {
			b = uint32(bits[i])
		}
		state = (state<<1 | b) & (1<<constraint - 1)
		for _, g := range generators {
			out = append(out, parity(state&g))
		}
	}
	return out
}

// outputs[s][b] are the three coded bits, as +1 for 0 and -1 for 1, of
// input b from state s (the last six bits).
var outputs = func() (o [states][2][3]float64) {
	for s := range states {
		for b := range 2 {
			full := uint32(s<<1|b) & (1<<constraint - 1)
			for i, g := range generators {
				o[s][b][i] = float64(1 - 2*int(parity(full&g)))
			}
		}
	}
	return
}()

// viterbi decodes nbits bits from soft values of the coded bits (positive
// for 0), the tail known to be zeros.
func viterbi(soft []float64, nbits int) []uint8 {
	steps := nbits + constraint - 1
	metric := make([]float64, states)
	next := make([]float64, states)
	for i := range metric {
		metric[i] = math.Inf(-1)
	}
	metric[0] = 0
	from := make([][states]uint8, steps)
	bit := make([][states]uint8, steps)
	for t := range steps {
		s3 := soft[3*t : 3*t+3]
		for i := range next {
			next[i] = math.Inf(-1)
		}
		for s := range states {
			if math.IsInf(metric[s], -1) {
				continue
			}
			for b := range 2 {
				if t >= nbits && b == 1 {
					continue
				}
				o := &outputs[s][b]
				m := metric[s] + float64(o[0]*s3[0]) + float64(o[1]*s3[1]) + float64(o[2]*s3[2])
				d := (s<<1 | b) & (states - 1)
				if m > next[d] {
					next[d], from[t][d], bit[t][d] = m, uint8(s), uint8(b)
				}
			}
		}
		metric, next = next, metric
	}
	bits := make([]uint8, steps)
	s := 0
	for t := steps - 1; t >= 0; t-- {
		bits[t] = bit[t][s]
		s = int(from[t][s])
	}
	return bits[:nbits]
}

// listSize is how many of the likeliest bit sequences listViterbi
// returns: when the likeliest fails its check, the next ones often pass.
const listSize = 4

// listViterbi decodes nbits bits from soft values of the coded bits
// (positive for 0), the tail known to be zeros: the listSize likeliest
// sequences, likeliest first (the parallel list Viterbi algorithm: each
// state keeps its listSize best paths).
func listViterbi(soft []float64, nbits int) [][]uint8 {
	const L = listSize
	type entry struct {
		metric float64
		from   uint8 // previous state
		rank   uint8 // rank of the path there
		bit    uint8
	}
	steps := nbits + constraint - 1
	var cur, nxt [states][L]float64
	var count, nextCount [states]int
	cur[0][0], count[0] = 0, 1
	back := make([][states][L]entry, steps)
	for t := range steps {
		s3 := soft[3*t : 3*t+3]
		nextCount = [states]int{}
		for s := range states {
			for b := range 2 {
				if t >= nbits && b == 1 {
					continue
				}
				o := &outputs[s][b]
				branch := float64(o[0]*s3[0]) + float64(o[1]*s3[1]) + float64(o[2]*s3[2])
				d := (s<<1 | b) & (states - 1)
				for r := range count[s] {
					m := cur[s][r] + branch
					// Insert into d's list, kept sorted, best first.
					n := nextCount[d]
					if n == L && m <= nxt[d][L-1] {
						continue
					}
					i := min(n, L-1)
					for i > 0 && nxt[d][i-1] < m {
						if i < L {
							nxt[d][i] = nxt[d][i-1]
							back[t][d][i] = back[t][d][i-1]
						}
						i--
					}
					nxt[d][i] = m
					back[t][d][i] = entry{metric: m, from: uint8(s), rank: uint8(r), bit: uint8(b)}
					if n < L {
						nextCount[d]++
					}
				}
			}
		}
		cur, count = nxt, nextCount
	}
	var out [][]uint8
	for r := range count[0] {
		bits := make([]uint8, steps)
		s, rk := 0, r
		for t := steps - 1; t >= 0; t-- {
			e := back[t][s][rk]
			bits[t] = e.bit
			s, rk = int(e.from), int(e.rank)
		}
		out = append(out, bits[:nbits])
	}
	return out
}

// unframe checks decoded bits: the payload, or false.
func unframe(bits []uint8, k Key) ([]byte, bool) {
	data := make([]byte, len(bits)/8)
	for i := range data {
		for j := range 8 {
			data[i] = data[i]<<1 | bits[8*i+j]
		}
	}
	n := len(data) - 4
	payload := data[:n]
	crc := uint32(data[n])<<24 | uint32(data[n+1])<<16 | uint32(data[n+2])<<8 | uint32(data[n+3])
	if checksum(payload, k) != crc {
		return nil, false
	}
	return payload, true
}
