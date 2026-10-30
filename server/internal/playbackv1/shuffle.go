package playbackv1

import "errors"

// Shuffle is the seeded keyed permutation over queue positions (ARCH-MEDIA-04): a 4-round Feistel
// network on the smallest even-bit domain ≥ n, cycle-walked back into [0, n). O(1) per position,
// never materialised, with a computable inverse, and identical to client-core's
// ShufflePermutation for the same (n, seed, lap) so every device computes the same order.
type Shuffle struct {
	n        uint64
	halfBits uint
	mask     uint64
	keys     [4]uint32
}

// MaxShuffleDomain bounds a shuffled segment set (the 10 M-key segment bound, with room).
const MaxShuffleDomain = 1 << 40

var errShuffleRange = errors.New("shuffle position out of range")

func mix(x, key uint32) uint32 {
	h := x ^ key
	h = (h ^ h>>16) * 0x7feb352d
	h = (h ^ h>>15) * 0x846ca68b
	return h ^ h>>16
}

// NewShuffle returns the permutation for n positions (1 ≤ n ≤ 2^40).
func NewShuffle(n uint64, seed, lap uint32) (*Shuffle, error) {
	if n < 1 || n > MaxShuffleDomain {
		return nil, errShuffleRange
	}
	bits := uint(2)
	for uint64(1)<<bits < n {
		bits += 2
	}
	s := &Shuffle{n: n, halfBits: bits / 2, mask: uint64(1)<<(bits/2) - 1}
	base := mix(seed, 0x9e3779b9^lap)
	for i := range s.keys {
		s.keys[i] = mix(base, uint32(uint64(0x85ebca6b)*uint64(i+1)))
	}
	return s, nil
}

func (s *Shuffle) f(r uint64, k uint32) uint64 { return uint64(mix(uint32(r), k)) & s.mask }

func (s *Shuffle) forward(x uint64) uint64 {
	l, r := x>>s.halfBits, x&s.mask
	for _, k := range s.keys {
		l, r = r, (l^s.f(r, k))&s.mask
	}
	return l<<s.halfBits | r
}

func (s *Shuffle) backward(x uint64) uint64 {
	l, r := x>>s.halfBits, x&s.mask
	for i := len(s.keys) - 1; i >= 0; i-- {
		l, r = (r^s.f(l, s.keys[i]))&s.mask, l
	}
	return l<<s.halfBits | r
}

// At is the source position shown at shuffled position i.
func (s *Shuffle) At(i uint64) (uint64, error) {
	if i >= s.n {
		return 0, errShuffleRange
	}
	x := s.forward(i)
	for x >= s.n {
		x = s.forward(x)
	}
	return x, nil
}

// IndexOf is the shuffled position of source position p (the inverse of At).
func (s *Shuffle) IndexOf(p uint64) (uint64, error) {
	if p >= s.n {
		return 0, errShuffleRange
	}
	x := s.backward(p)
	for x >= s.n {
		x = s.backward(x)
	}
	return x, nil
}
