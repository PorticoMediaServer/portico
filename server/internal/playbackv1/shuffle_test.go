package playbackv1

import (
	"math/rand/v2"
	"testing"
)

type pair struct{ i, at uint64 }

// Vectors from client-core's ShufflePermutation: the server and every client agree on the order.
func TestShuffleMatchesClientCore(t *testing.T) {
	for _, c := range []struct {
		n         uint64
		seed, lap uint32
		want      []pair
	}{
		{1, 7, 0, []pair{{0, 0}}},
		{2, 1, 0, []pair{{0, 1}, {1, 0}}},
		{10, 42, 0, []pair{{0, 6}, {1, 5}, {5, 7}, {9, 8}}},
		{10, 42, 1, []pair{{0, 9}, {1, 7}, {5, 1}, {9, 6}}},
		{1000, 123456789, 3, []pair{{0, 370}, {1, 706}, {500, 87}, {999, 738}}},
		{1048576, 4294967295, 0, []pair{{0, 591298}, {1, 365}, {524288, 173097}, {1048575, 633812}}},
		{750000, 99, 2, []pair{{0, 725493}, {1, 366814}, {375000, 737670}, {749999, 676911}}},
	} {
		s, err := NewShuffle(c.n, c.seed, c.lap)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range c.want {
			if got, _ := s.At(p.i); got != p.at {
				t.Fatalf("n=%d seed=%d lap=%d: At(%d)=%d, client says %d", c.n, c.seed, c.lap, p.i, got, p.at)
			}
		}
	}
}

// Invariant 7: a bijection with a computable inverse for N in [1, 2^20].
func TestShuffleBijectionAndInverse(t *testing.T) {
	sizes := []uint64{1, 2, 3, 4, 5, 15, 16, 17, 255, 256, 257, 1000, 4097, 65535, 1 << 20}
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 20; i++ {
		sizes = append(sizes, 1+r.Uint64N(1<<20))
	}
	for _, n := range sizes {
		s, _ := NewShuffle(n, r.Uint32(), r.Uint32N(4))
		if n <= 1<<17 {
			seen := make([]bool, n)
			for i := uint64(0); i < n; i++ {
				p, err := s.At(i)
				if err != nil || seen[p] {
					t.Fatalf("n=%d: At(%d)=%d repeats (%v)", n, i, p, err)
				}
				seen[p] = true
				if back, _ := s.IndexOf(p); back != i {
					t.Fatalf("n=%d: IndexOf(At(%d))=%d", n, i, back)
				}
			}
			continue
		}
		for k := 0; k < 4096; k++ {
			i := r.Uint64N(n)
			p, _ := s.At(i)
			if back, _ := s.IndexOf(p); back != i || p >= n {
				t.Fatalf("n=%d: inverse failed at %d", n, i)
			}
		}
	}
	if _, err := NewShuffle(0, 1, 0); err == nil {
		t.Fatal("n=0 accepted")
	}
}
