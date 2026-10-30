package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// O-101 byte bound: one hydrated window keeps its raw count at <=128 and its
// ordinary scalar bytes inside facetScalarBudget. These tests pin the follow-up
// properties that make the byte stop safe:
//
//  1. a window stops before the object that would exceed the budget, and a
//     legal object larger than the budget is still hydrated alone;
//  2. the returned count is a RAW PREFIX, so absent joins inside it are
//     consumed while ids past it are never reported as missing;
//  3. capture's cursor and the grouped object-work acknowledgement only use
//     that prefix, so no unhydrated raw id is skipped or deleted.
//
// The fixtures use near-longest legal relative paths (~4KB). Every object is a
// real eligible book, created by the same helpers the parity tests use.

type pathBytesLibrary struct {
	handle      int64
	source, inc string
	root        string
}

// seedPathBytesLibrary creates a book library and its catalogue handles.
func seedPathBytesLibrary(t *testing.T, tx *sql.Tx, id, root string) pathBytesLibrary {
	t.Helper()
	execPathBatch(t, tx, `INSERT INTO libraries(id,name,kind,root) VALUES(?,?,'book',?)`, id, id, root)
	lib := pathBytesLibrary{root: root}
	if err := tx.QueryRow(`SELECT cl.id,s.id,s.incarnation FROM catalog_libraries cl JOIN library_sources s ON s.library_id=cl.library_id WHERE cl.library_id=?`, id).Scan(&lib.handle, &lib.source, &lib.inc); err != nil {
		t.Fatal(err)
	}
	return lib
}

// seedPathBytesObject adds one eligible ebook object with a caller-chosen
// relative path and returns its raw id, entity and asset.
func seedPathBytesObject(t *testing.T, tx *sql.Tx, lib pathBytesLibrary, serial int, relative string) (string, int64, int64) {
	t.Helper()
	item := pathBatchEntity(t, tx, lib.handle, 20)
	token := fmt.Sprintf("tok-bytes-%04d", serial)
	asset := pathBatchAsset(t, tx, item, token, lib.root+"/"+relative, "epub")
	id := fmt.Sprintf("obj-bytes-%04d", serial)
	pathBatchObject(t, tx, id, lib.source, lib.inc, token, relative, "r1")
	return id, item, asset
}

func pathBytesCount(t *testing.T, tx *sql.Tx, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := tx.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func pathBytesStrings(t *testing.T, tx *sql.Tx, q string, args ...any) []string {
	t.Helper()
	rows, err := tx.Query(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func beginPathBytesTx(t *testing.T) (*sql.DB, *sql.Tx) {
	t.Helper()
	db := openPathBatchDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return db, tx
}

// pathBytesLongRelative is a legal relative path close to the 4096-byte
// attribution limit: the whole filename is one long scalar.
func pathBytesLongRelative(serial int, repeat string) string {
	return fmt.Sprintf("Author/Series/%04d-%s.epub", serial, strings.Repeat(repeat, 4000))
}

func TestReadPathObjectsCappedStopsAtTheOrdinaryByteBudget(t *testing.T) {
	_, tx := beginPathBytesTx(t)
	ctx := context.Background()
	lib := seedPathBytesLibrary(t, tx, "bytes", "/bytes")
	const count = 80
	ids := make([]string, 0, count)
	for i := 0; i < count; i++ {
		id, _, _ := seedPathBytesObject(t, tx, lib, i, pathBytesLongRelative(i, "a"))
		ids = append(ids, id)
	}

	// The raw prefix must follow caller order, independent of object primary
	// key order; the streaming join must not sort hydrated scalar rows.
	for left, right := 0, len(ids)-1; left < right; left, right = left+1, right-1 {
		ids[left], ids[right] = ids[right], ids[left]
	}
	objects, n, err := readPathObjectsCapped(ctx, tx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if n <= 0 || n >= count {
		t.Fatalf("capped window visited %d of %d raw ids, want a byte-stopped prefix", n, count)
	}
	if len(objects) != n {
		t.Fatalf("window visited %d ids but hydrated %d objects", n, len(objects))
	}
	bytes := 0
	for _, id := range ids[:n] {
		v, ok := objects[id]
		if !ok {
			t.Fatalf("%s: object inside the returned prefix was not hydrated", id)
		}
		bytes += v.bytes()
	}
	if bytes > pathObjectByteBudget {
		t.Fatalf("window retained %d ordinary bytes, want at most %d", bytes, pathObjectByteBudget)
	}
	for _, id := range ids[n:] {
		if _, ok := objects[id]; ok {
			t.Fatalf("%s: object past the returned prefix was hydrated", id)
		}
	}

	// Repeating the capped read on the remaining ids covers every raw id
	// exactly once: a byte-stopped window never drops or skips a suffix id.
	for visited := n; visited < count; {
		objects, m, err := readPathObjectsCapped(ctx, tx, ids[visited:])
		if err != nil {
			t.Fatal(err)
		}
		if m <= 0 {
			t.Fatalf("capped read made no progress with %d ids left", count-visited)
		}
		if visited+m > count {
			t.Fatalf("capped read consumed %d ids past the input", visited+m-count)
		}
		for _, id := range ids[visited : visited+m] {
			if _, ok := objects[id]; !ok {
				t.Fatalf("%s: object inside the returned prefix was not hydrated", id)
			}
		}
		visited += m
	}
}

func TestReadPathObjectsCappedCountsMissingRawPrefix(t *testing.T) {
	_, tx := beginPathBytesTx(t)
	ctx := context.Background()
	lib := seedPathBytesLibrary(t, tx, "missing", "/missing")
	const count = 40
	ids := []string{"missing-a", "missing-b"}
	for i := 0; i < count; i++ {
		id, _, _ := seedPathBytesObject(t, tx, lib, i, pathBytesLongRelative(i, "b"))
		ids = append(ids, id)
	}

	objects, n, err := readPathObjectsCapped(ctx, tx, ids)
	if err != nil {
		t.Fatal(err)
	}
	// The two absent raw ids are inside the prefix, not a stop: n counts raw
	// ids, and the window still stops on the byte budget before the suffix.
	if n < 3 || n >= len(ids) {
		t.Fatalf("capped window visited %d of %d raw ids, want an absent-joined prefix followed by a byte stop", n, len(ids))
	}
	for _, id := range []string{"missing-a", "missing-b"} {
		if _, ok := objects[id]; ok {
			t.Fatalf("%s: an absent join must never hydrate", id)
		}
	}
	if int(n)-2 != len(objects) {
		t.Fatalf("prefix %d raw ids with 2 absent joins hydrated %d objects", n, len(objects))
	}
	for _, id := range ids[n:] {
		if _, ok := objects[id]; ok {
			t.Fatalf("%s: object past the returned prefix was hydrated", id)
		}
	}

	// A window with no joins at all is consumed whole rather than read as EOF.
	objects, n, err = readPathObjectsCapped(ctx, tx, []string{"gone-1", "gone-2"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(objects) != 0 {
		t.Fatalf("all-absent window returned n=%d objects=%d, want n=2 and no objects", n, len(objects))
	}
}

func TestReadPathObjectsCappedHydratesOversizedObjectAlone(t *testing.T) {
	_, tx := beginPathBytesTx(t)
	ctx := context.Background()
	small := seedPathBytesLibrary(t, tx, "small", "/small")
	wide := seedPathBytesLibrary(t, tx, "wide", "/wide")
	smallID, _, _ := seedPathBytesObject(t, tx, small, 0, "Author/Series/01.epub")
	// inventory_objects.relative_path has no schema length cap, so one legal
	// hydrated path scalar can exceed the whole window budget on its own.
	oversized := "Author/Series/" + strings.Repeat("z", pathObjectByteBudget+1) + ".epub"
	wideID, _, _ := seedPathBytesObject(t, tx, wide, 1, oversized)

	objects, n, err := readPathObjectsCapped(ctx, tx, []string{smallID, wideID})
	if err != nil {
		t.Fatal(err)
	}
	// The small object fits; the oversized one is never added to a non-empty
	// window and is not reported as missing either.
	if n != 1 || len(objects) != 1 {
		t.Fatalf("window returned n=%d objects=%d, want the leading object alone", n, len(objects))
	}
	if _, ok := objects[smallID]; !ok {
		t.Fatalf("%s: leading object was not hydrated", smallID)
	}
	if _, ok := objects[wideID]; ok {
		t.Fatalf("%s: oversized object was added to a non-empty window", wideID)
	}

	// The next window starts empty, so the same oversized object is admitted
	// alone instead of stalling the walk at zero progress.
	objects, n, err = readPathObjectsCapped(ctx, tx, []string{wideID})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(objects) != 1 {
		t.Fatalf("next window returned n=%d objects=%d, want the oversized object alone", n, len(objects))
	}
	v, ok := objects[wideID]
	if !ok {
		t.Fatalf("%s: oversized object was not hydrated alone", wideID)
	}
	if v.bytes() <= pathObjectByteBudget {
		t.Fatalf("fixture object is %d ordinary bytes, want more than the %d budget", v.bytes(), pathObjectByteBudget)
	}
	if !v.Eligible {
		t.Fatalf("the oversized fixture must still be one legal eligible pathObject")
	}
}

func TestPathCaptureStepConsumesOnlyTheHydratedRawPrefix(t *testing.T) {
	_, tx := beginPathBytesTx(t)
	ctx := context.Background()
	lib := seedPathBytesLibrary(t, tx, "capture", "/capture")
	const count = 80
	var scope int64
	if err := tx.QueryRow(`INSERT INTO reading_path_scope(library_id,source_id,incarnation,root_prefix,directory_key,desired) VALUES(?,?,?,?,?,?) RETURNING id`, lib.handle, lib.source, lib.inc, rootPrefix(lib.root), "Author/Series", count).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, count)
	for i := 0; i < count; i++ {
		relative := pathBytesLongRelative(i, "c")
		id, item, asset := seedPathBytesObject(t, tx, lib, i, relative)
		ids = append(ids, id)
		execPathBatch(t, tx, `INSERT INTO reading_path_object_keys(object_id,scope_id,item_id,context_active,asset_id,source_revision,relative_path,eligible) VALUES(?,?,?,1,?,'r1',?,1)`, id, scope, item, asset, relative)
	}
	var build int64
	if err := tx.QueryRow(`INSERT INTO reading_path_build(scope_id,desired,input_revision,source_generation,root_identity,job_id,phase) VALUES(?,?,1,1,'root','job-capture','capture') RETURNING id`, scope, count).Scan(&build); err != nil {
		t.Fatal(err)
	}
	s := pathScope{ID: scope, Library: lib.handle, Source: lib.source, Incarnation: lib.inc, Root: rootPrefix(lib.root), Key: "Author/Series"}
	b := pathBuild{ID: build, Scope: scope, Desired: count, Job: "job-capture", Phase: "capture"}

	n, err := pathCaptureStep(ctx, tx, b, s, pathObjectBatchMax)
	if err != nil {
		t.Fatal(err)
	}
	if n <= 0 || n >= count {
		t.Fatalf("first capture step returned %d of %d raw ids, want a byte-stopped prefix", n, count)
	}
	cursor := pathBytesStrings(t, tx, `SELECT after_object FROM reading_path_build WHERE id=?`, build)[0]
	if cursor != ids[n-1] {
		t.Fatalf("capture cursor is %q, want the last raw-prefix id %q", cursor, ids[n-1])
	}
	if got := pathBytesCount(t, tx, `SELECT count(*) FROM reading_path_capture WHERE build_id=?`, build); got != int64(n) {
		t.Fatalf("first step captured %d objects, want exactly the %d raw-prefix ids", got, n)
	}
	if got := pathBytesStrings(t, tx, `SELECT phase FROM reading_path_build WHERE id=?`, build)[0]; got != "capture" {
		t.Fatalf("capture phase is %q after a partial window, want capture (no false EOF)", got)
	}
	for _, id := range ids[n:] {
		if got := pathBytesCount(t, tx, `SELECT count(*) FROM reading_path_capture WHERE build_id=? AND object_id=?`, build, id); got != 0 {
			t.Fatalf("%s: object past the returned prefix was captured", id)
		}
	}

	// Later steps resume at the durable cursor and visit the remaining raw
	// prefix in order, so every raw id is captured exactly once.
	for step := 0; step < 32; step++ {
		var phase, at string
		var titles, nonSeries int64
		if err = tx.QueryRow(`SELECT phase,after_object,title_count,non_series_count FROM reading_path_build WHERE id=?`, build).Scan(&phase, &at, &titles, &nonSeries); err != nil {
			t.Fatal(err)
		}
		if phase != "capture" {
			break
		}
		b.Phase, b.Object, b.Titles, b.NonSeries = phase, at, titles, nonSeries
		if _, err = pathCaptureStep(ctx, tx, b, s, pathObjectBatchMax); err != nil {
			t.Fatal(err)
		}
		var nextPhase, next string
		if err = tx.QueryRow(`SELECT phase,after_object FROM reading_path_build WHERE id=?`, build).Scan(&nextPhase, &next); err != nil {
			t.Fatal(err)
		}
		if nextPhase != "capture" {
			// An exhausted raw window is the only transition out of capture.
			if nextPhase != "stage" {
				t.Fatalf("capture phase became %q, want stage at EOF", nextPhase)
			}
			break
		}
		if next <= at {
			t.Fatalf("capture cursor %q did not advance past %q", next, at)
		}
	}
	if phase := pathBytesStrings(t, tx, `SELECT phase FROM reading_path_build WHERE id=?`, build)[0]; phase != "stage" {
		t.Fatalf("capture phase is %q after the window was consumed, want stage", phase)
	}
	captured := pathBytesStrings(t, tx, `SELECT object_id FROM reading_path_capture WHERE build_id=? ORDER BY object_id`, build)
	if !reflect.DeepEqual(captured, ids) {
		t.Fatalf("captured %d raw ids, want every one of the %d in order", len(captured), len(ids))
	}
}

func TestPathObjectWorkStepAcknowledgesOnlyTheHydratedRawPrefix(t *testing.T) {
	_, tx := beginPathBytesTx(t)
	ctx := context.Background()
	lib := seedPathBytesLibrary(t, tx, "work", "/work")
	const count = 80
	ids := make([]string, 0, count)
	for i := 0; i < count; i++ {
		// The inventory trigger queues one 'object' work row per raw object.
		id, _, _ := seedPathBytesObject(t, tx, lib, i, pathBytesLongRelative(i, "d"))
		ids = append(ids, id)
	}
	head := pathWork{Library: lib.handle, Kind: "object", Key: ids[0], Requested: 1, Captured: 1}

	n, err := pathObjectWorkStep(ctx, tx, head, pathObjectBatchMax, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n <= 0 || n >= count {
		t.Fatalf("object step acknowledged %d of %d raw ids, want a byte-stopped prefix", n, count)
	}
	if got := pathBytesCount(t, tx, `SELECT count(*) FROM reading_path_dirty_work WHERE kind='object'`); got != int64(count-n) {
		t.Fatalf("%d object work rows left, want %d: only the hydrated raw prefix may be acknowledged", got, count-n)
	}
	remaining := pathBytesStrings(t, tx, `SELECT work_key FROM reading_path_dirty_work WHERE kind='object' ORDER BY work_key`)
	if !reflect.DeepEqual(remaining, ids[n:]) {
		t.Fatalf("remaining work keys are %v, want exactly the unhydrated suffix %v", remaining, ids[n:])
	}
	if got := pathBytesCount(t, tx, `SELECT count(*) FROM reading_path_object_keys`); got != int64(n) {
		t.Fatalf("%d objects normalized, want exactly the %d hydrated raw-prefix ids", got, n)
	}

	// The unacknowledged suffix is claimed again; normalization eventually
	// reaches every raw id.
	for step := 0; step < 32; step++ {
		if pathBytesCount(t, tx, `SELECT count(*) FROM reading_path_dirty_work WHERE kind='object'`) == 0 {
			break
		}
		var w pathWork
		if err := tx.QueryRow(`SELECT library_id,work_key,requested,captured,served FROM reading_path_dirty_work WHERE kind='object' ORDER BY served,kind,work_key LIMIT 1`).Scan(&w.Library, &w.Key, &w.Requested, &w.Captured, &w.Served); err != nil {
			t.Fatal(err)
		}
		w.Kind = "object"
		if w.Captured == 0 {
			w.Captured = w.Requested
		}
		if _, err := pathObjectWorkStep(ctx, tx, w, pathObjectBatchMax, 1); err != nil {
			t.Fatal(err)
		}
	}
	if got := pathBytesCount(t, tx, `SELECT count(*) FROM reading_path_dirty_work WHERE kind='object'`); got != 0 {
		t.Fatalf("%d object work rows still queued after repeated claims", got)
	}
	if got := pathBytesCount(t, tx, `SELECT count(*) FROM reading_path_object_keys`); got != count {
		t.Fatalf("normalization visited %d of %d raw ids", got, count)
	}
}
