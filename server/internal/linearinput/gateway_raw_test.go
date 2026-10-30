package linearinput

import (
	"context"
	"io"
	"strconv"
	"testing"
	"time"
)

type rawPumpReader func([]byte) (int, error)

func (read rawPumpReader) Read(p []byte) (int, error) { return read(p) }

func TestRawPumpOwnsOnlyBytesRead(t *testing.T) {
	for _, size := range []int{1, 7, 188, 1024, 64 << 10} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			g := &Gateway{ctx: ctx, changed: make(chan struct{})}
			var scratch []byte
			reads := 0
			g.pump(rawPumpReader(func(p []byte) (int, error) {
				if reads == 4 {
					cancel()
					return 0, io.EOF
				}
				if scratch == nil {
					scratch = p
				} else if &scratch[0] != &p[0] {
					t.Fatal("pump allocated another scratch buffer")
				}
				for i := 0; i < size; i++ {
					p[i] = byte(reads + 1)
				}
				reads++
				return size, nil
			}))
			if len(g.raw) != 4 || g.total-g.base != int64(4*size) {
				t.Fatalf("retained %d blocks, %d bytes", len(g.raw), g.total-g.base)
			}
			for i, block := range g.raw {
				if len(block) != size || cap(block) != size {
					t.Fatalf("block %d: len=%d cap=%d, want %d", i, len(block), cap(block), size)
				}
				for _, b := range block {
					if b != byte(i+1) {
						t.Fatalf("block %d was overwritten by a later read", i)
					}
				}
			}
		})
	}
}

func TestRawPumpBoundsBlocksAndClearsEvictedSlots(t *testing.T) {
	for _, test := range []struct {
		name  string
		size  int
		reads int
		keep  int
	}{
		{"tiny reads", 1, maxRawBlocks + 3, maxRawBlocks},
		{"byte limit", 64 << 10, 19, 16},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// Keep a view of the backing array to check that eviction releases its
			// byte slices, rather than only advancing the visible slice header.
			blocks := make([][]byte, test.reads)
			times := make([]time.Time, test.reads)
			g := &Gateway{ctx: ctx, changed: make(chan struct{}), raw: blocks[:0], rawTimes: times[:0]}
			reads := 0
			g.pump(rawPumpReader(func(p []byte) (int, error) {
				if reads == test.reads {
					cancel()
					return 0, io.EOF
				}
				for i := 0; i < test.size; i++ {
					p[i] = byte(reads)
				}
				reads++
				return test.size, nil
			}))
			dropped := test.reads - test.keep
			if len(g.raw) != test.keep || len(g.rawTimes) != test.keep || g.base != int64(dropped*test.size) || g.total != int64(test.reads*test.size) {
				t.Fatalf("retained blocks=%d timestamps=%d base=%d total=%d", len(g.raw), len(g.rawTimes), g.base, g.total)
			}
			for i := 0; i < dropped; i++ {
				if blocks[i] != nil || !times[i].IsZero() {
					t.Fatalf("evicted slot %d still retains data", i)
				}
			}
			for i, block := range g.raw {
				if len(block) != test.size || block[0] != byte(i+dropped) {
					t.Fatalf("retained block %d is out of order", i)
				}
			}
		})
	}
}
