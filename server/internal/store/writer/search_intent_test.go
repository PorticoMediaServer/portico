package writer

import (
	"bytes"
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestSearchInputChangeAdmission(t *testing.T) {
	entity := newOrdinaryEntitySource{SubjectID: 1, LibraryID: 1, Kind: 1, EntityRevision: 1}
	if err := validateNewSearchInput(&searchInputChange{NewEntities: []newOrdinaryEntitySource{entity}, AcceptedAtMS: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := validateNewSearchInput(&searchInputChange{}); err != nil {
		t.Fatal("no actual source changes must remain a no-op")
	}
	for _, edit := range []func(*newOrdinaryEntitySource){
		func(e *newOrdinaryEntitySource) { e.SubjectID = 0 },
		func(e *newOrdinaryEntitySource) { e.LibraryID = 0 },
		func(e *newOrdinaryEntitySource) { e.Kind = 2 },
		func(e *newOrdinaryEntitySource) { e.EntityRevision = 2 },
	} {
		bad := entity
		edit(&bad)
		if err := validateNewSearchInput(&searchInputChange{NewEntities: []newOrdinaryEntitySource{bad}}); !errors.Is(err, ErrInvalid) {
			t.Fatal("unsupported source variant admitted")
		}
	}
	for _, bad := range []*searchInputChange{nil, {AcceptedAtMS: -1}} {
		if err := validateNewSearchInput(bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid intent envelope admitted")
		}
	}
	if err := validateNewSearchInput(&searchInputChange{NewEntities: []newOrdinaryEntitySource{entity, entity}}); !errors.Is(err, ErrCap) {
		t.Fatal("unqualified larger source batch admitted")
	}
}

func TestSearchInputCounterBoundaries(t *testing.T) {
	for _, tt := range []struct {
		expected, delta, want int64
	}{
		{0, 1, 1}, {1, 1, 2}, {math.MaxInt64 - 1, 1, math.MaxInt64},
		{math.MaxInt64 - 2, 2, math.MaxInt64}, {0, math.MaxInt64, math.MaxInt64},
	} {
		if got, err := nextSearchInputCounter(tt.expected, tt.delta); err != nil || got != tt.want {
			t.Fatalf("exact expected->next counter: got %d, %v, want %d", got, err, tt.want)
		}
	}
	for _, tt := range []struct{ expected, delta int64 }{
		{-1, 1}, {0, 0}, {1, -1}, {0, math.MinInt64},
		{math.MaxInt64, 1}, {math.MaxInt64 - 1, 2}, {1, math.MaxInt64},
	} {
		if got, err := nextSearchInputCounter(tt.expected, tt.delta); !errors.Is(err, ErrInvalid) || got != 0 {
			t.Fatal("empty, invalid or overflowing source-clock increment admitted")
		}
	}
}

// These tests exercise the literal SQL through the merged private frame. They
// are statement/atomicity controls, not InventoryAccept or helper integration:
// W's commandTurn/common receipt implementation is not on main yet.
func searchInputFixture(t *testing.T) (*executor, *stmt.Registry) {
	t.Helper()
	w, _, _ := testExecutor(t)
	for _, sql := range []string{
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(1,'fixture','/fixture')`,
		`INSERT INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(1,zeroblob(16),1,1)`,
		`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,metadata_revision)
VALUES(1,zeroblob(16),1,1,'A','a','A','a','A',0,X'09000000000000000000000000000000',zeroblob(16),7,11)`,
		`INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms)
VALUES(1,'v1','A-CJK-uni-bi-v1','engine','unicode','active',1000)`,
		`UPDATE search_revision SET active_generation_id=1 WHERE singleton=1`,
	} {
		if _, err := w.db.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	r, err := stmt.New(searchInputIntentDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, r
}

func searchInputBound(t *testing.T, r *stmt.Registry, id stmt.ID, args ...any) stmt.Bound {
	t.Helper()
	s, err := r.Lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Bind(args...)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Fixture forecasts and virtual frame clock prove protocol sequencing only.
// They are not production recipe constants or physical HOLD/WAL qualification.
func searchInputFixtureStep(t *testing.T, tx *transaction) {
	t.Helper()
	if err := tx.before(Work{Rows: 4, VMOps: 100, Pages: 4, WALBytes: 4096, Hold: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
}

func TestSearchInputNativeStatements(t *testing.T) {
	w, registry := searchInputFixture(t)
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	searchInputFixtureStep(t, tx)
	var revision, sequence int64
	var active, building sql.NullInt64
	if err := searchInputBound(t, registry, searchInputHead).QueryRowContext(tx.ctx, tx.conn).Scan(&revision, &active, &building, &sequence); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || active.Int64 != 1 || !active.Valid || building.Valid || sequence != 0 {
		t.Fatal("wrong source-input head")
	}
	searchInputFixtureStep(t, tx)
	var bankKey, recipe, engine, unicode, state string
	var pending, bankRevision int64
	if err := searchInputBound(t, registry, searchInputBank, active.Int64).QueryRowContext(tx.ctx, tx.conn).
		Scan(&bankKey, &recipe, &engine, &unicode, &state, &pending, &bankRevision); err != nil {
		t.Fatal(err)
	}
	if bankKey != "v1" || recipe != "A-CJK-uni-bi-v1" || engine != "engine" || unicode != "unicode" || state != "active" || pending != 0 || bankRevision != 1 {
		t.Fatal("bank lookup must preserve the complete stored identity and pending CAS witness")
	}
	searchInputFixtureStep(t, tx)
	var incarnation []byte
	var sourceRevision int64
	if err := searchInputBound(t, registry, searchInputEntity, int64(1), int64(1), int64(1)).QueryRowContext(tx.ctx, tx.conn).Scan(&incarnation, &sourceRevision); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(incarnation, []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}) || sourceRevision != 1 {
		t.Fatal("intent must use exact metadata incarnation/entity revision, not other counters")
	}
	searchInputFixtureStep(t, tx)
	var subject int64
	if err := searchInputBound(t, registry, searchInputSubject, int64(1), int64(1), int64(1)).QueryRowContext(tx.ctx, tx.conn).Scan(&subject); err != nil || subject != 1 {
		t.Fatal("retained subject must independently match the same source library/kind")
	}
	searchInputFixtureStep(t, tx)
	var existingJob int64
	if err := searchInputBound(t, registry, searchInputExisting, int64(1)).QueryRowContext(tx.ctx, tx.conn).Scan(&existingJob); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("new subject must have no earlier publisher")
	}
	for _, step := range []struct {
		id   stmt.ID
		args []any
	}{
		{searchInputInsert, []any{int64(1), int64(1), int64(1), incarnation, sourceRevision, "A-CJK-uni-bi-v1", "capture", int64(1000)}},
		{searchInputPending, []any{int64(1), int64(2), int64(1), "active", "A-CJK-uni-bi-v1", "engine", "unicode", int64(0), int64(1)}},
		{searchInputSequence, []any{int64(1), int64(1), int64(1), int64(0)}},
	} {
		searchInputFixtureStep(t, tx)
		r, err := searchInputBound(t, registry, step.id, step.args...).ExecContext(tx.ctx, tx.conn)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 1 {
			t.Fatal("fresh source effect must change exactly one row")
		}
	}
	result, err := tx.commit()
	if err != nil || result.state != committed {
		t.Fatalf("statement frame: %v %v", result, err)
	}
	var exact bool
	if err := w.db.QueryRowContext(t.Context(), `SELECT entity_id=1 AND person_id IS NULL AND search_generation_id=1
AND input_generation=1 AND source_incarnation=X'09000000000000000000000000000000' AND source_revision=1
AND recipe_version='A-CJK-uni-bi-v1' AND phase='capture' AND published_generation=0 AND published_document_id IS NULL
AND document_id IS NULL AND after_field=0 AND after_ordinal=0 AND after_fragment_id=0
AND expected_fragments=0 AND staged_fragments=0 AND staged_bytes=0 AND content_digest=''
AND job_revision=1 AND job_lease_owner=0 AND job_lease_generation=0 AND job_lease_expires_ms=0
AND job_attempts=0 AND job_next_attempt_ms=1000 AND job_due_ms=1000 AND length(command_incarnation)=16
AND last_command_sequence=0 AND acknowledged_command_sequence=0 FROM search_publish_jobs`).Scan(&exact); err != nil || !exact {
		t.Fatal("incompatible publisher intent defaults")
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision=1 AND input_sequence=1 AND active_generation_id=1 AND building_generation_id IS NULL FROM search_revision`).Scan(&exact); err != nil || !exact {
		t.Fatal("input clock must not publish or change accepted head/revision")
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT pending_subjects=1 AND job_revision=2 FROM search_generations WHERE id=1`).Scan(&exact); err != nil || !exact {
		t.Fatal("bank pending/cursor revision did not advance together")
	}
	var nativeRows int
	if err := w.db.QueryRowContext(t.Context(), `SELECT
(SELECT count(*) FROM search_own_prefix_docsize)
+(SELECT count(*) FROM search_fragment_prefix_docsize)
+(SELECT count(*) FROM search_fragment_lexical_docsize)
+(SELECT count(*) FROM search_fragment_grams_docsize)
+(SELECT count(*) FROM catalog_people_names_docsize)`).Scan(&nativeRows); err != nil || nativeRows != 0 {
		t.Fatal("source intent must not write native FTS")
	}
}

func TestSearchInputClockCAS(t *testing.T) {
	w, registry := searchInputFixture(t)
	for _, args := range [][]any{
		{int64(1), int64(2), int64(1), int64(0)},
		{int64(1), int64(1), int64(2), int64(0)},
		{int64(1), int64(1), int64(1), int64(1)},
	} {
		tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.rollback(nil)
		searchInputFixtureStep(t, tx)
		r, err := searchInputBound(t, registry, searchInputSequence, args...).ExecContext(tx.ctx, tx.conn)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := r.RowsAffected(); n != 0 {
			t.Fatal("stale bank/head/input-clock CAS was accepted")
		}
		if _, err := tx.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_revision SET input_sequence=?`, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	searchInputFixtureStep(t, tx)
	r, err := searchInputBound(t, registry, searchInputSequence, int64(1), int64(1), int64(1), int64(math.MaxInt64)).ExecContext(tx.ctx, tx.conn)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := r.RowsAffected(); n != 0 {
		t.Fatal("input sequence overflow admitted")
	}
}

func TestSearchInputBuildingClockCAS(t *testing.T) {
	w, registry := searchInputFixture(t)
	// Metadata-only clock-CAS fixture: no second native bank/DDL or compatible
	// generation is asserted. The actual helper must reject unsupported banks.
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms)
VALUES(2,'clock_fixture_only','fixture_recipe','fixture_engine','fixture_unicode','building',1000)`); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		building bool
		id       stmt.ID
		args     []any
		want     int64
	}{
		{false, searchInputBuilding, []any{int64(1), int64(1), int64(1), int64(2), int64(0)}, 0},
		{true, searchInputSequence, []any{int64(1), int64(1), int64(1), int64(0)}, 0},
		{true, searchInputBuilding, []any{int64(1), int64(1), int64(1), int64(3), int64(0)}, 0},
		{true, searchInputBuilding, []any{int64(1), int64(2), int64(1), int64(2), int64(0)}, 0},
		{true, searchInputBuilding, []any{int64(1), int64(1), int64(3), int64(2), int64(0)}, 0},
		{true, searchInputBuilding, []any{int64(1), int64(1), int64(1), int64(2), int64(1)}, 0},
		{true, searchInputBuilding, []any{int64(1), int64(1), int64(1), int64(2), int64(0)}, 1},
	} {
		var building any
		if tt.building {
			building = int64(2)
		}
		if _, err := w.db.ExecContext(t.Context(), `UPDATE search_revision SET building_generation_id=? WHERE singleton=1`, building); err != nil {
			t.Fatal(err)
		}
		tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.rollback(nil)
		searchInputFixtureStep(t, tx)
		r, err := searchInputBound(t, registry, tt.id, tt.args...).ExecContext(tx.ctx, tx.conn)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != tt.want {
			t.Fatal("active-only/building variant failed exact head CAS")
		}
		var exact bool
		if err := tx.conn.QueryRowContext(tx.ctx, `SELECT revision=1 AND active_generation_id=1 AND input_sequence=? FROM search_revision WHERE singleton=1`, tt.want).Scan(&exact); err != nil || !exact {
			t.Fatal("clock variant changed serving revision or incremented more than once")
		}
		if _, err := tx.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_revision SET input_sequence=? WHERE singleton=1`, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	searchInputFixtureStep(t, tx)
	r, err := searchInputBound(t, registry, searchInputBuilding, int64(1), int64(1), int64(1), int64(2), int64(math.MaxInt64)).ExecContext(tx.ctx, tx.conn)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.RowsAffected(); err != nil || n != 0 {
		t.Fatal("building clock sequence overflow admitted")
	}
}

func TestSearchInputPendingCAS(t *testing.T) {
	w, registry := searchInputFixture(t)
	good := []any{int64(1), int64(2), int64(1), "active", "A-CJK-uni-bi-v1", "engine", "unicode", int64(0), int64(1)}
	for _, tt := range []struct {
		at    int
		value any
	}{
		{2, int64(2)}, {3, "building"}, {4, "wrong-recipe"}, {5, "wrong-engine"},
		{6, "wrong-unicode"}, {7, int64(1)}, {8, int64(2)},
	} {
		args := append([]any(nil), good...)
		args[tt.at] = tt.value
		tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.rollback(nil)
		searchInputFixtureStep(t, tx)
		r, err := searchInputBound(t, registry, searchInputPending, args...).ExecContext(tx.ctx, tx.conn)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := r.RowsAffected(); n != 0 {
			t.Fatal("stale generation/recipe/engine/Unicode/pending CAS accepted")
		}
		if _, err := tx.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{"pending_subjects", "job_revision"} {
		// Closed fixture alternatives, not runtime SQL construction.
		query := `UPDATE search_generations SET pending_subjects=? WHERE id=1`
		args := append([]any(nil), good...)
		args[7] = int64(math.MaxInt64)
		if field == "job_revision" {
			query = `UPDATE search_generations SET pending_subjects=0,job_revision=? WHERE id=1`
			args[7], args[8] = int64(0), int64(math.MaxInt64)
		}
		if _, err := w.db.ExecContext(t.Context(), query, int64(math.MaxInt64)); err != nil {
			t.Fatal(err)
		}
		tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.rollback(nil)
		searchInputFixtureStep(t, tx)
		r, err := searchInputBound(t, registry, searchInputPending, args...).ExecContext(tx.ctx, tx.conn)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := r.RowsAffected(); n != 0 {
			t.Fatal("generation counter overflow admitted")
		}
		if _, err := tx.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchInputEffectsRollbackTogether(t *testing.T) {
	w, registry := searchInputFixture(t)
	steps := []struct {
		id   stmt.ID
		args []any
	}{
		{searchInputInsert, []any{int64(1), int64(1), int64(1), []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), "A-CJK-uni-bi-v1", "capture", int64(1000)}},
		{searchInputPending, []any{int64(1), int64(2), int64(1), "active", "A-CJK-uni-bi-v1", "engine", "unicode", int64(0), int64(1)}},
		{searchInputSequence, []any{int64(1), int64(1), int64(1), int64(0)}},
	}
	for stop := 0; stop <= len(steps); stop++ {
		tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.rollback(nil)
		searchInputFixtureStep(t, tx)
		// Synthetic caller effect, not a registered InventoryAccept implementation.
		if _, err := tx.conn.ExecContext(tx.ctx, `UPDATE catalog_entities SET title='proposed' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		for _, step := range steps[:stop] {
			searchInputFixtureStep(t, tx)
			if _, err := searchInputBound(t, registry, step.id, step.args...).ExecContext(tx.ctx, tx.conn); err != nil {
				t.Fatal(err)
			}
		}
		cause := errors.New("cohort refused")
		if result, err := tx.rollback(cause); !errors.Is(err, cause) || result.state != rolledBack {
			t.Fatal("cohort refusal did not roll back")
		}
		var intact bool
		if err := w.db.QueryRowContext(t.Context(), `SELECT
(SELECT title='A' FROM catalog_entities WHERE id=1)
AND (SELECT count(*)=0 FROM search_publish_jobs)
AND (SELECT pending_subjects=0 AND job_revision=1 FROM search_generations WHERE id=1)
AND (SELECT revision=1 AND input_sequence=0 FROM search_revision WHERE singleton=1)`).Scan(&intact); err != nil || !intact {
			t.Fatal("source/intent/pending/clock escaped whole-cohort rollback")
		}
	}
}

func TestSearchInputNativeSourceBoundary(t *testing.T) {
	w, registry := searchInputFixture(t)
	for _, args := range [][]any{
		{int64(2), int64(1), int64(1)},
		{int64(1), int64(2), int64(1)},
		{int64(1), int64(1), int64(2)},
	} {
		var incarnation []byte
		var revision int64
		if err := searchInputBound(t, registry, searchInputEntity, args...).QueryRowContext(t.Context(), w.db).Scan(&incarnation, &revision); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("foreign subject/library/kind passed native source lookup")
		}
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_entities SET retired=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	var incarnation []byte
	var revision int64
	if err := searchInputBound(t, registry, searchInputEntity, int64(1), int64(1), int64(1)).QueryRowContext(t.Context(), w.db).Scan(&incarnation, &revision); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("retired source passed native lookup")
	}
}

func TestSearchInputNativeSubjectBoundary(t *testing.T) {
	w, registry := searchInputFixture(t)
	for _, args := range [][]any{
		{int64(2), int64(1), int64(1)},
		{int64(1), int64(2), int64(1)},
		{int64(1), int64(1), int64(2)},
	} {
		var subject int64
		if err := searchInputBound(t, registry, searchInputSubject, args...).QueryRowContext(t.Context(), w.db).Scan(&subject); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("foreign or absent retained subject passed its independent point proof")
		}
	}
}

func TestSearchInputExistingIntentBoundary(t *testing.T) {
	w, registry := searchInputFixture(t)
	if _, err := searchInputBound(t, registry, searchInputInsert, int64(1), int64(1), int64(1),
		[]byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), "A-CJK-uni-bi-v1", "capture", int64(1000)).ExecContext(t.Context(), w.db); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		subject int64
		want    bool
	}{{1, true}, {2, false}} {
		var existingJob int64
		err := searchInputBound(t, registry, searchInputExisting, tt.subject).QueryRowContext(t.Context(), w.db).Scan(&existingJob)
		if tt.want && (err != nil || existingJob <= 0) || !tt.want && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("an existing publisher owner must not be mistaken for a new source")
		}
	}
}

func TestSearchInputNativeAccessPlans(t *testing.T) {
	w, registry := searchInputFixture(t)
	for _, entry := range registry.Manifest() {
		for _, cohort := range entry.Cohorts {
			rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+entry.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			var plans []string
			for rows.Next() {
				var id, parent, unused int64
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plans = append(plans, detail)
				if strings.Contains(detail, "USE TEMP B-TREE") || strings.Contains(detail, "SCAN ") {
					t.Errorf("%s/%s unbounded access: %s", entry.ID, cohort.Name, detail)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if entry.ID == searchInputExisting && !strings.Contains(strings.Join(plans, "\n"), "SEARCH search_publish_jobs USING COVERING INDEX sqlite_autoindex_search_publish_jobs_1 (entity_id=?)") {
				t.Fatal("intent existence must use subject-leading unique index")
			}
		}
	}
}
