package evidence

import (
	"testing"
	"time"
)

func TestFamiliesNeverIncludeValues(t *testing.T) {
	for query, want := range map[string]string{
		"SELECT * FROM inventory_analysis_queue WHERE object_id=?":        "read:analysis_queue",
		"UPDATE inventory_objects SET revision=?":                         "write:inventory",
		"SELECT 'FROM secret_password' FROM catalog_assets WHERE token=?": "read:info_assets",
		"SELECT * FROM catalog_dirty":                                     "read:derived_work",
		"COMMIT":                                                          "transaction",
	} {
		if got := Family(query); got != want {
			t.Fatalf("family %q != %q", got, want)
		}
	}
}
func TestOverlappingSpansConserveWindow(t *testing.T) {
	state.Lock()
	state.on = true
	state.began = time.Now()
	state.last = state.began
	state.spans = map[string]Metric{}
	state.masks = map[uint64]int64{}
	state.Unlock()
	wait := Begin("scheduler_wait")
	q := Begin("quantum")
	sql := Begin("sql")
	sql()
	q()
	wait()
	state.Lock()
	end := time.Now()
	advance(end)
	state.on = false
	var total int64
	for _, n := range state.masks {
		total += n
	}
	elapsed := end.Sub(state.began).Nanoseconds()
	state.Unlock()
	if total != elapsed {
		t.Fatalf("partition %d != window %d", total, elapsed)
	}
	if state.spans["sql"].Count != 1 {
		t.Fatal("SQL span lost")
	}
}
func TestSQLReturnsOriginalResult(t *testing.T) {
	rc, err := SQL("SELECT 1", func() (int, error) { return 123, nil })
	if rc != 123 || err != nil {
		t.Fatal(rc, err)
	}
}
