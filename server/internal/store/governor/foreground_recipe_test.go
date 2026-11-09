package governor

import (
	"fmt"
	"testing"
	"time"
)

func TestForegroundReadRecipeFundsMaximumConcurrentWindows(t *testing.T) {
	if ForegroundReadBytes != 12_648_448 || ForegroundReadBytes != 129*65536+(4<<20) ||
		ForegroundRequestBytes != 21_037_056 || ForegroundRequestBytes-(20<<20) != 65536 {
		t.Fatal("foreground recipe drift", ForegroundReadBytes, ForegroundRequestBytes)
	}
	for _, tc := range []struct {
		memory, nonBrowse int64
		windows           int
	}{{1_600_000_000, 1140 << 20, 4}, {2_000_000_000, 1360 << 20, 8}, {4_000_000_000, 2720 << 20, 16}, {8_000_000_000, 5440 << 20, 33}} {
		t.Run(fmt.Sprint(tc.windows), func(t *testing.T) {
			now := time.Now()
			profile := &ForegroundProfile{AssignedBytes: tc.memory, EffectiveBytes: tc.memory, NonBrowseBytes: tc.nonBrowse,
				MinimumLive: tc.windows, MaxWaiters: 1, BookkeepingBytes: 4096, MaxWait: 50 * time.Millisecond}
			n, err := profile.Capacity()
			if err != nil || n != tc.windows {
				t.Fatal("funded concurrency changed", n, err)
			}
			// At maximum concurrency, every window holds its native row + 4 MiB
			// Go margin concurrently with its 8 MiB output. Metadata also funds
			// the bounded waiter. No 20 MiB-per-LIVE rounding can hide 64 KiB.
			reads := int64(n) * ForegroundReadBytes
			outputs := int64(n) * (8 << 20)
			metadata := int64(n+1) * 4096
			partition := reads + outputs + metadata
			cfg := Config{Now: func() time.Time { return now }, MaxGrantsPerClass: 3*n + 1, Foreground: profile}
			cfg.Partitions[Foreground] = Amounts{Reader: int64(n), MemoryBytes: partition, QueueBytes: metadata}
			g, a, err := newGovernor(cfg, 0) // arithmetic/control, not host capacity qualification
			if err != nil {
				t.Fatal(err)
			}
			var held []*Grant
			defer func() {
				for i := len(held) - 1; i >= 0; i-- {
					if err := a.Retired(held[i]); err != nil {
						t.Error(err)
					}
				}
				if a.Usage() != [4]Amounts{} || a.ForegroundUsage().Live != 0 {
					t.Error("concurrent recipe custody leaked", a.Usage(), a.ForegroundUsage())
				}
			}()
			for i := 0; i < n; i++ {
				key := JobKey{Domain: 91, ID: fmt.Sprint(i)}
				live := a.AdmitForeground(t.Context(), ForegroundRequest{Key: key, Owner: 1, ReadyDeadline: now.Add(time.Hour), ProtectedDeadline: now.Add(time.Hour)})
				if live.Grant == nil {
					t.Fatal("maximum LIVE concurrency refused", i, live.Refusal)
				}
				held = append(held, live.Grant)
				for _, need := range []Amounts{{Reader: 1, MemoryBytes: ForegroundReadBytes}, {MemoryBytes: 8 << 20}} {
					d := g.Try(Request{Key: key, Owner: 1, Class: Foreground, Need: need, Deadline: now.Add(time.Hour)})
					if d.Grant == nil {
						t.Fatal("maximum concurrent read/output refused", i, need, d.Refusal)
					}
					held = append(held, d.Grant)
				}
			}
			if got := a.Usage()[Foreground]; got[Reader] != int64(n) || got[MemoryBytes] != reads+outputs+int64(n)*4096 {
				t.Fatal("actual concurrent reservation differs", got, reads, outputs)
			}
			cfg.Partitions[Foreground][MemoryBytes]--
			if _, _, err := newGovernor(cfg, 0); err == nil {
				t.Fatal("underfunded maximum concurrency accepted")
			}
			t.Logf("%d windows: reads=%d outputs=%d metadata=%d partition=%d", n, reads, outputs, metadata, partition)
		})
	}
}
