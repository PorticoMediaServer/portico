package remotesources

import (
	"math"
	"testing"
)

func TestVolumeGrowthPreservesOtherReservationsFloor(t *testing.T) {
	volume := t.Name()
	first := reserveVolumeGrowth(volume, 100, 20, func(floor, growth int64) bool {
		return floor == 100 && growth == 20
	})
	if first == nil {
		t.Fatal("first reservation refused")
	}
	defer first.settle(20)
	second := reserveVolumeGrowth(volume, 10, 5, func(floor, growth int64) bool {
		return floor == 100 && growth == 25
	})
	if second == nil {
		t.Fatal("second reservation ignored the first reservation's floor or growth")
	}
	defer second.settle(5)
	first.settle(10)
	if !second.available(func(floor, growth int64) bool { return floor == 100 && growth == 15 }) {
		t.Fatal("partial persistence released the original floor")
	}
	first.settle(10)
	if !second.available(func(floor, growth int64) bool { return floor == 10 && growth == 5 }) {
		t.Fatal("settled reservation still charged growth or its floor")
	}
	called := false
	tooLarge := reserveVolumeGrowth(volume, 0, math.MaxInt64, func(int64, int64) bool {
		called = true
		return true
	})
	if tooLarge != nil || called {
		t.Fatal("overflow reached the filesystem check or acquired a reservation")
	}
}
