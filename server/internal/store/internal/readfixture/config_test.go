package readfixture

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestForegroundReadRecipeMatchesCanonicalReader(t *testing.T) {
	limits := ReaderConfig().ReadLimits
	if limits.Columns != 129 || limits.LengthBytes != 65536 ||
		ReadRequest().Need[governor.MemoryBytes] != governor.ForegroundReadBytes ||
		governor.ForegroundReadBytes != int64(limits.Columns)*int64(limits.LengthBytes)+(4<<20) {
		t.Fatal("foreground recipe and canonical Reader diverged", limits, ReadRequest().Need)
	}
	cfg := LiveConfig(time.Now)
	capacity, err := cfg.Foreground.Capacity()
	if err != nil || capacity != 4 {
		t.Fatal("fixture LIVE profile changed", capacity, err)
	}
	// Fund all four LIVE read+output recipes, even though this fixed Reader
	// pool has only one simultaneous native window. Include the bounded waiter.
	want := 4*governor.ForegroundReadBytes + 4*(8<<20) + 5*4096
	if cfg.Partitions[governor.Foreground][governor.Reader] != 1 || cfg.Partitions[governor.Foreground][governor.MemoryBytes] != want || want != 84_168_704 {
		t.Fatal("fixture partition does not cover maximum LIVE custody", cfg.Partitions, want)
	}
}
