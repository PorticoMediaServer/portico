package playbackv1

import (
	"math/rand/v2"
	"testing"
)

func TestSortKeyBetweenStaysOrdered(t *testing.T) {
	keys := []string{sortKeyBetween("", "")}
	r := rand.New(rand.NewPCG(3, 4))
	for n := 0; n < 3000; n++ {
		i := r.IntN(len(keys) + 1)
		lo, hi := "", ""
		if i > 0 {
			lo = keys[i-1]
		}
		if i < len(keys) {
			hi = keys[i]
		}
		k := sortKeyBetween(lo, hi)
		if !(lo < k && (hi == "" || k < hi)) || k[len(k)-1] == '0' {
			t.Fatalf("between(%q,%q)=%q", lo, hi, k)
		}
		keys = append(keys[:i], append([]string{k}, keys[i:]...)...)
	}
	// Appending at the end many times stays short.
	k := ""
	for n := 0; n < 1000; n++ {
		k = sortKeyBetween(k, "")
	}
	if len(k) > 40 {
		t.Fatalf("append keys grow too fast: %d", len(k))
	}
}
