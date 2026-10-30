package compactcatalog_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// O-101 parity: the batched capture window and the batched normalize sweeps must
// leave exactly the state that one-object steps leave. The same fixture is
// drained twice: with a raw budget of one (every window is a single point read)
// and with a larger budget (one set-based read per window). Capture, the
// representative choice, the durable cursor, the per-scope counters and the
// published heads must all be identical.

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
	capture, representative, build, keys, scopes, heads   []string
	values                                                map[int64]compactcatalog.ReadingValues
	lowItem, lowAsset, highAsset, secondItem, secondAsset int64
}

func runReadingBatchFixture(t *testing.T, limit int) readingBatchState {
	t.Helper()
	f := newCompleteFixture(t, nil, "batch")
	// One item, two publications in one folder. The lower asset is captured
	// last (its object id sorts after the other), so the representative must be
	// replaced downwards exactly once.
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
	// A second supported title in the same folder is what makes the counted
	// folder context valid: one title alone publishes its author but no series.
	// It leaves the two-files-one-item representative branch above untouched.
	second := f.add("Author/Series/Series 05.epub", "epub", 0)

	f.settle()
	f.seal()
	for i := 0; i < 5000; i++ {
		if scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_build WHERE phase='stage'`) > 0 {
			break
		}
		completeStep(t, f.c, limit)
	}
	if scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_build WHERE phase='stage'`) != 1 {
		t.Fatal("fixture never reached the capture/stage boundary")
	}
	state := readingBatchState{
		lowItem: low.item, lowAsset: low.asset, highAsset: high.asset, secondItem: second.item, secondAsset: second.asset,
		capture:        readingBatchRows(t, f.c.DB, `SELECT item_id,asset_id,object_id,source_revision,relative_path,item_kind,series_evidence FROM reading_path_capture ORDER BY item_id,asset_id,object_id`),
		representative: readingBatchRows(t, f.c.DB, `SELECT item_id,asset_id,series_evidence FROM reading_path_representative ORDER BY item_id`),
		build:          readingBatchRows(t, f.c.DB, `SELECT after_object,title_count,non_series_count FROM reading_path_build ORDER BY id`),
	}
	for i := 0; i < 5000; i++ {
		if scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_dirty_work`) == 0 &&
			scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_build`) == 0 &&
			scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_scope WHERE queued=1`) == 0 {
			break
		}
		completeStep(t, f.c, limit)
	}
	if scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_dirty_work`) != 0 ||
		scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_build`) != 0 ||
		scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_scope WHERE queued=1`) != 0 {
		t.Fatalf("fixture did not drain at raw budget %d", limit)
	}
	state.keys = readingBatchRows(t, f.c.DB, `SELECT object_id,scope_id,item_id,context_active,asset_id,source_revision,relative_path,eligible FROM reading_path_object_keys ORDER BY object_id`)
	// Desired is a monotone build-attempt fence, not a content count. The
	// batch size may coalesce a different number of attempts; each run must
	// still accept its own exact desired fence and publish identical facts.
	if n := scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_scope WHERE completed<>desired OR queued<>0`); n != 0 {
		t.Fatalf("%d scopes failed their exact completion fence", n)
	}
	state.scopes = readingBatchRows(t, f.c.DB, `SELECT id,library_id,source_id,directory_key,desired=completed,queued,pending,sealed FROM reading_path_scope ORDER BY id`)
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
	if len(batch.capture) != 3 {
		t.Fatalf("capture has %d rows, want two versions of one title plus the second title: %v", len(batch.capture), batch.capture)
	}
	wantRepresentative := map[int64]int64{batch.lowItem: batch.lowAsset, batch.secondItem: batch.secondAsset}
	if len(batch.representative) != len(wantRepresentative) {
		t.Fatalf("representative is %v, want one lowest asset per title", batch.representative)
	}
	for _, row := range batch.representative {
		var item, asset int64
		var evidence int
		if _, err := fmt.Sscanf(row, "%d|%d|%d", &item, &asset, &evidence); err != nil {
			t.Fatalf("representative row %q: %v", row, err)
		}
		want, ok := wantRepresentative[item]
		if !ok || asset != want || evidence != 1 {
			t.Fatalf("representative %q, want the lowest asset (%d) with series evidence for item %d", row, want, item)
		}
	}
	if len(batch.build) != 1 || !strings.HasPrefix(batch.build[0], "zzz-zzz|2|0") {
		t.Fatalf("capture cursor/counters are %v, want the full raw window consumed over two titles (zzz-zzz|2|0)", batch.build)
	}
	if len(batch.keys) != 5 {
		t.Fatalf("object keys are %v, want the scope's five raw objects", batch.keys)
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
	// Two counted titles with no non-series witness is what carries the folder
	// series; a single-title folder deliberately publishes no series.
	for _, item := range []int64{batch.lowItem, batch.secondItem} {
		if v := batch.values[item]; v.Series == nil || v.Series.Value != "Series" {
			t.Fatalf("published series for item %d is %+v, want the folder series", item, v)
		}
	}
}
