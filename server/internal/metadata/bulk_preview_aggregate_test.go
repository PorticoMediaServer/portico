package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func TestBulkPreviewAggregateExactThrough64AndSaturatesAt65(t *testing.T) {
	m := bulkPreviewAggregate{}
	ctx := context.Background()
	for round := 0; round < 2; round++ {
		for i := 0; i < bulkPreviewValues; i++ {
			if err := m.add(ctx, []bulkPreviewObservation{{field: "year", value: fmt.Sprint(1900 + i), count: 1, locked: round == 1}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	schema := []RepairFieldSpec{{Field: "year"}}
	out, err := m.result(schema)
	if err != nil {
		t.Fatal(err)
	}
	f := out["year"]
	if f.State != "mixed" || f.DistinctCount != 64 || f.AtLeast != 0 || len(f.Values) != 64 || f.Locked != 64 || f.Truncated {
		t.Fatal(f)
	}
	for _, v := range f.Values {
		if v.Count != 2 {
			t.Fatal(v)
		}
	}
	if err = m.add(ctx, []bulkPreviewObservation{{field: "year", value: "new", count: 1, locked: true}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2000; i++ {
		if err = m.add(ctx, []bulkPreviewObservation{{field: "year", value: fmt.Sprint(i), count: 1, locked: true}}); err != nil {
			t.Fatal(err)
		}
	}
	out, err = m.result(schema)
	if err != nil {
		t.Fatal(err)
	}
	f = out["year"]
	if f.State != "many" || f.AtLeast != 65 || f.DistinctCount != 0 || f.Value != nil || len(f.Values) != 0 || !f.Truncated || f.Locked != 2065 {
		t.Fatal(f)
	}
	if m.fields["year"].values != nil {
		t.Fatal("saturated field retained distinct keys")
	}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["distinctCount"]; exists {
		t.Fatal(string(raw))
	}
	if _, exists := wire["value"]; exists {
		t.Fatal(string(raw))
	}
}

func TestBulkPreviewAggregateEntriesRemainExactBoundedAndDeduplicated(t *testing.T) {
	m := bulkPreviewAggregate{}
	ctx := context.Background()
	// Descending arrival continually replaces the largest display key. Counts
	// are exact because a key that is evicted can never be displayed again.
	for i := 9999; i >= 0; i-- {
		entry := fmt.Sprintf("%05d", i)
		for repeat := 0; repeat < 2; repeat++ {
			if err := m.add(ctx, []bulkPreviewObservation{{field: "tags", value: entry, count: 1, entries: []string{entry, entry, "Shared", "Shared"}}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if m.used > 256<<10 {
		t.Fatalf("entry eviction leaked byte reservations: %d", m.used)
	}
	out, err := m.result([]RepairFieldSpec{{Field: "tags"}})
	if err != nil {
		t.Fatal(err)
	}
	f := out["tags"]
	if f.State != "many" || len(f.Entries) != 64 || !f.Truncated {
		t.Fatal(f)
	}
	for i, entry := range f.Entries {
		if entry.Value != fmt.Sprintf("%05d", i) || entry.Count != 2 {
			t.Fatal(i, entry)
		}
	}
	// A small list entry stays visible and counts all entities even after its
	// scalar list-vector cardinality saturates.
	m = bulkPreviewAggregate{}
	for i := 0; i < 100; i++ {
		if err = m.add(ctx, []bulkPreviewObservation{{field: "tags", value: fmt.Sprint(i), count: 2, entries: []string{"Shared", "Shared"}, locked: true}}); err != nil {
			t.Fatal(err)
		}
	}
	out, err = m.result([]RepairFieldSpec{{Field: "tags"}})
	if err != nil {
		t.Fatal(err)
	}
	if f = out["tags"]; f.State != "many" || len(f.Entries) != 1 || f.Entries[0].Count != 200 || f.Locked != 200 {
		t.Fatal(f)
	}
}

func TestBulkPreviewAggregateSharedEmptyAndCapacity(t *testing.T) {
	m := bulkPreviewAggregate{}
	if err := m.add(context.Background(), []bulkPreviewObservation{{field: "studio", value: "", count: 3}}); err != nil {
		t.Fatal(err)
	}
	out, err := m.result([]RepairFieldSpec{{Field: "studio"}})
	if err != nil {
		t.Fatal(err)
	}
	f := out["studio"]
	if f.State != "shared" || f.DistinctCount != 1 || f.Value == nil || *f.Value != "" || f.Values[0].Count != 3 {
		t.Fatal(f)
	}
	m = bulkPreviewAggregate{reserve: func(int64) error { return readprep.ErrCapacity }}
	if err = m.add(context.Background(), []bulkPreviewObservation{{field: "year", value: "2020", count: 1}}); err != readprep.ErrCapacity || m.fields != nil {
		t.Fatal(err, m.fields)
	}
}

func TestBulkPreviewAggregateCancellationWithinOneSourceList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	m := bulkPreviewAggregate{reserve: func(int64) error {
		calls++
		if calls == 4 { // fixed field, scalar, then first retained entry
			cancel()
		}
		return nil
	}}
	err := m.add(ctx, []bulkPreviewObservation{{field: "genres", value: "list", entries: []string{"A", "B", "C"}, count: 1}})
	if !errors.Is(err, context.Canceled) || len(m.fields["genres"].entries) != 1 {
		t.Fatal(err, m.fields["genres"].entries)
	}
}
