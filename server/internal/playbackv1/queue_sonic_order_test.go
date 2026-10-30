package playbackv1

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func TestCapturedSonicOrderKeepsSeededWindowsInverseAndOriginalOrder(t *testing.T) {
	l := &layout{
		q:            queueRow{shuffle: true, domain: 512, first: 17, seed: 249, lap: 3},
		segments:     []segment{{count: 512, musicOnly: true, state: "ready"}},
		sonicWindows: map[int64]sonicWindowCapture{},
	}
	for window := int64(0); window < 2; window++ {
		ordinals := make([]int64, 256)
		for n := range ordinals {
			ordinals[n] = l.ordinaryOrderToSource(window*256 + int64(n))
		}
		for left, right := 1, 255; left < right; left, right = left+1, right-1 {
			ordinals[left], ordinals[right] = ordinals[right], ordinals[left]
		}
		l.sonicWindows[window] = sonicWindowCapture{ordinals: ordinals, model: &sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}}
	}
	for position := int64(0); position < 512; position++ {
		source := l.orderToSource(position)
		if got := l.sourceToOrder(source); got != position {
			t.Fatal("Back/entry mapping lost captured occurrence", position, source, got)
		}
		if original := l.ordinarySourceToOrder(source); original/256 != position/256 {
			t.Fatal("smoothing moved a song between seeded windows", position, source, original)
		}
	}
	l.q.shuffle = false
	for position := int64(0); position < 512; position++ {
		if l.orderToSource(position) != position || l.sourceToOrder(position) != position {
			t.Fatal("shuffle Off did not restore original source order", position)
		}
	}
	l.q.shuffle = true
	l.segments[0].musicOnly = false
	for position := int64(0); position < 512; position++ {
		window := l.sonicWindows[position/256]
		if l.orderToSource(position) != window.ordinals[position%256] {
			t.Fatal("later queue edits rewrote an already captured music order", position)
		}
	}
}
