package compactcatalog_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestZZStepCensus(t *testing.T) {
	if os.Getenv("ZZ_STEPS") == "" {
		t.Skip()
	}
	for _, pressure := range []bool{false, true} {
		c := catalogtest.Open(t)
		movies := c.Library("m", "Movies", "movie", "/m")
		for i := 0; i < 130; i++ {
			c.Movie(movies, fmt.Sprintf("/m/%03d.mkv", i), fmt.Sprintf("Film %03d", i), 2000)
		}
		if pressure {
			dbwork.RegisterForegroundProbe(t.Name(), func() bool { return true })
		}
		w := compactcatalog.NewWorker(c.DB)
		steps, rows, took := map[int]int{}, map[int]int{}, map[int]time.Duration{}
		start := time.Now()
		total := 0
		for i := 0; i < 100000; i++ {
			s := time.Now()
			n, err := w.Step(context.Background(), compactcatalog.BatchMax)
			if err != nil {
				t.Fatal(err)
			}
			d, _, _ := w.LastBatch()
			if n == 0 {
				break
			}
			total++
			steps[d]++
			rows[d] += n
			took[d] += time.Since(s)
		}
		dbwork.RegisterForegroundProbe(t.Name(), nil)
		keys := []int{}
		for k := range steps {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(a, b int) bool { return steps[keys[a]] > steps[keys[b]] })
		t.Logf("pressure=%v steps=%d work=%s", pressure, total, time.Since(start))
		for _, k := range keys[:min(len(keys), 15)] {
			t.Logf("  domain %3d steps %5d rows %5d time %s", k, steps[k], rows[k], took[k].Round(time.Millisecond))
		}
	}
}
