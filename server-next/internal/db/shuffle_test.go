package db

import (
	"cmp"
	"fmt"
	"slices"
	"testing"
)

// Shuffling an album, or choosing Random twice, gives an unrelated order each time: twelve
// tracks with ids next to each other, shuffled with 2,000 seeds, come out in nearly 2,000
// different orders and almost never in track order. (The multiply-modulo-prime walk this
// replaced gave them 495 orders, 87 of them track order.)
func TestEverySeedShufflesAnAlbumAfresh(t *testing.T) {
	const tracks, seeds = 12, 2000
	orders := map[string]bool{}
	inOrder := 0
	for seed := int64(1); seed <= seeds; seed++ {
		ids := make([]int64, tracks)
		for n := range ids {
			ids[n] = int64(5000 + n)
		}
		slices.SortFunc(ids, func(a, b int64) int {
			pa, pb := shufflePlace(a, seed), shufflePlace(b, seed)
			if pa < 0 || pb < 0 {
				t.Fatalf("a negative place under seed %d", seed)
			}
			return cmp.Compare(pa, pb)
		})
		if slices.IsSorted(ids) {
			inOrder++
		}
		orders[fmt.Sprint(ids)] = true
	}
	if len(orders) < seeds*99/100 || inOrder > 1 {
		t.Fatalf("%d seeds gave %d orders, %d of them track order", seeds, len(orders), inOrder)
	}
}

// No two items share a place under a seed, so a walk that reads places after the last one
// meets each item once.
func TestAShuffleGivesEveryItemItsOwnPlace(t *testing.T) {
	for _, seed := range []int64{1, 2, 77, 2147483646} {
		seen := map[int64]int64{}
		for id := int64(1); id <= 100_000; id++ {
			p := shufflePlace(id, seed)
			if other, ok := seen[p]; ok {
				t.Fatalf("seed %d: items %d and %d share place %d", seed, other, id, p)
			}
			seen[p] = id
		}
	}
}
