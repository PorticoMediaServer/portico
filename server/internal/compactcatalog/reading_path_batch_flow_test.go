package compactcatalog_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Batched per-file hydration and one-object steps must publish the same
// accepted heads and leave the same durable witnesses, including ineligible
// and missing publication rows.

func readingBatchRows(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(values))
		for i, value := range values {
			switch v := value.(type) {
			case nil:
				parts[i] = "null"
			case []byte:
				parts[i] = string(v)
			default:
				parts[i] = fmt.Sprint(v)
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type readingBatchState struct {
	keys, heads                                           []string
	values                                                map[int64]compactcatalog.ReadingValues
	lowItem, lowAsset, highAsset, secondItem, secondAsset int64
}

func runReadingBatchFixture(t *testing.T, limit int) readingBatchState {
	t.Helper()
	f := newCompleteFixture(t, nil, "batch")
	// One item, two publications in one folder. Reverse raw-object order
	// must not change canonical version selection.
	low := f.add("Author/Series/Series 01.epub", "epub", 0)
	high := f.add("Author/Series/Series 02.pdf", "pdf", low.item)
	f.c.Exec(`UPDATE inventory_objects SET id='zzz-low' WHERE id=?`, low.object)
	// An unsupported row and a publication-less row: hydrated but ineligible.
	// The publication-less object also takes the highest raw id, so the window's
	// last member is a rejected object and the durable cursor must still advance
	// past it. (An active canonical duplicate is not representable: the baseline
	// keeps inventory_objects(source_id,relative_path) unique while retired=0.)
	unsupported := f.add("Author/Series/Series 03.epub", "epub", 0)
	f.c.Exec(`UPDATE inventory_objects SET unsupported_reason='reject' WHERE id=?`, unsupported.object)
	bare := f.add("Author/Series/Series 04.epub", "epub", 0)
	f.c.Exec(`DELETE FROM reading_publications WHERE item_id=? AND asset_id=?`, bare.item, bare.asset)
	f.c.Exec(`UPDATE inventory_objects SET id='zzz-zzz' WHERE id=?`, bare.object)
	// A separate title gets the same layout-derived labels independently of
	// the other title and its two publication versions.
	second := f.add("Author/Series/Series 05.epub", "epub", 0)

	f.settle()
	f.seal()
	state := readingBatchState{lowItem: low.item, lowAsset: low.asset, highAsset: high.asset, secondItem: second.item, secondAsset: second.asset}
	for i := 0; i < 5000; i++ {
		if scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_dirty_work`) == 0 {
			break
		}
		completeStep(t, f.c, limit)
	}
	if scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_dirty_work`) != 0 {
		t.Fatal("feed did not drain")
	}
	state.keys = readingBatchRows(t, f.c.DB, `SELECT object_id,item_id,context_active,asset_id,source_revision,relative_path,eligible,pending FROM reading_path_object_keys ORDER BY object_id`)
	state.heads = readingBatchRows(t, f.c.DB, `SELECT item_id,asset_id,object_id,source_revision,author,author_source,issue_date,series,series_source,series_number FROM reading_path_head ORDER BY item_id,asset_id`)
	state.values = map[int64]compactcatalog.ReadingValues{
		low.item:    completeValues(t, f.c, low.item),
		second.item: completeValues(t, f.c, second.item),
	}
	return state
}

func TestReadingCompleteBatchHydrationMatchesSingleObjectSteps(t *testing.T) {
	point := runReadingBatchFixture(t, 1)
	batch := runReadingBatchFixture(t, 7)
	if !reflect.DeepEqual(point, batch) {
		t.Fatalf("batched Reading steps diverged from one-object steps\npoint %+v\nbatch %+v", point, batch)
	}
	// Independent expectations on the batched run, so the oracle cannot pass by
	// both paths making the same mistake.
	if len(batch.keys) != 5 {
		t.Fatalf("object keys are %v, want five raw object witnesses", batch.keys)
	}
	if len(batch.heads) != 3 {
		t.Fatalf("heads are %v, want one head per publication", batch.heads)
	}
	published := map[string]bool{}
	for _, row := range batch.heads {
		if fields := strings.SplitN(row, "|", 3); len(fields) == 3 {
			published[fields[0]+"|"+fields[1]] = true
		}
	}
	for _, pair := range [][2]int64{{batch.lowItem, batch.lowAsset}, {batch.lowItem, batch.highAsset}, {batch.secondItem, batch.secondAsset}} {
		if !published[fmt.Sprintf("%d|%d", pair[0], pair[1])] {
			t.Fatalf("heads are %v, want a head for item %d asset %d", batch.heads, pair[0], pair[1])
		}
	}
	// Each accepted file derives the folder series independently of sibling
	// counts or version order.
	for _, item := range []int64{batch.lowItem, batch.secondItem} {
		if v := batch.values[item]; v.Series == nil || v.Series.Value != "Series" {
			t.Fatalf("published series for item %d is %+v, want the folder series", item, v)
		}
	}
}
