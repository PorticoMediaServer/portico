package writer

import (
	"database/sql"
	"fmt"
	"testing"
)

// Test-only canonical staged-enrich seed helpers. Values are cloned from the
// actual prepared fixture and only identifiers, sequences and immutable source
// digests are replaced.
const (
	canonicalEnrichRecipe = "canonical-enrich-materialized-image-accept-v1"
	canonicalEnrichWarmup = 8
)

func canonicalEnrichBlob(tag byte, n int) [16]byte {
	b := [16]byte{'E', 'N', 'R', tag}
	for i := 0; i < 8; i++ {
		b[8+i] = byte(uint64(n) >> (8 * (7 - i)))
	}
	return b
}

type canonicalSQLRow struct {
	cols []string
	vals []any
}

func canonicalReadRow(t *testing.T, db *sql.DB, table, where string, args ...any) canonicalSQLRow {
	t.Helper()
	cols, err := tableColumns(db, table)
	if err != nil {
		t.Fatal(err)
	}
	return canonicalReadSelected(t, db, table, cols, where, args...)
}

// canonicalReadAllRow keeps generated columns for decoders that require the
// complete native projection.
func canonicalReadAllRow(t *testing.T, db *sql.DB, table, where string, args ...any) canonicalSQLRow {
	t.Helper()
	cols, err := tableAllColumns(db, table)
	if err != nil {
		t.Fatal(err)
	}
	return canonicalReadSelected(t, db, table, cols, where, args...)
}

func canonicalReadSelected(t *testing.T, db *sql.DB, table string, cols []string, where string, args ...any) canonicalSQLRow {
	t.Helper()
	selected := ""
	for i, c := range cols {
		if i > 0 {
			selected += ","
		}
		selected += c
	}
	rows, err := db.QueryContext(t.Context(), "SELECT "+selected+" FROM "+table+" WHERE "+where, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("missing canonical template row in %s", table)
	}
	r := canonicalSQLRow{cols: cols, vals: make([]any, len(cols))}
	ptrs := make([]any, len(cols))
	for i := range r.vals {
		ptrs[i] = &r.vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatalf("ambiguous canonical template row in %s", table)
	}
	return r
}

func (r canonicalSQLRow) index(t *testing.T, column string) int {
	t.Helper()
	for i, c := range r.cols {
		if c == column {
			return i
		}
	}
	t.Fatalf("canonical template lacks %s", column)
	return -1
}

func (r canonicalSQLRow) with(t *testing.T, changes map[string]any) canonicalSQLRow {
	t.Helper()
	out := canonicalSQLRow{cols: append([]string(nil), r.cols...), vals: append([]any(nil), r.vals...)}
	for column, value := range changes {
		out.vals[r.index(t, column)] = value
	}
	return out
}

func (r canonicalSQLRow) insert(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	columns, placeholders := "", ""
	for i, c := range r.cols {
		if i > 0 {
			columns += ","
			placeholders += ","
		}
		columns += c
		placeholders += "?"
	}
	if _, err := db.ExecContext(t.Context(), fmt.Sprintf("INSERT INTO %s(%s) VALUES(%s)", table, columns, placeholders), r.vals...); err != nil {
		t.Fatal(err)
	}
}

var canonicalSealSavedColumns = []string{
	"provider_operation_id", "command_incarnation", "command_sequence", "operation_id", "request_digest", "command_kind",
	"accepted_revision", "applied_ms", "entity_id", "after_item", "after_ordinal", "publication_id", "effective_image_id",
	"effective_image_revision", "result_count", "result_digest", "seal_version", "seal_evidence_id", "seal_subject_id",
	"seal_parser_version", "seal_provider", "seal_subject_kind", "seal_provider_id", "seal_language", "seal_region",
	"seal_request_shape", "seal_observation_digest", "seal_owner_generation", "seal_build_id", "seal_build_generation",
	"seal_content_revision", "seal_algorithm", "seal_commitment", "seal_verified_rows", "seal_canonical_bytes",
	"seal_retained_bytes", "upstream_evidence_owner", "upstream_evidence_incarnation", "upstream_evidence_sequence",
	"source_dependency_owner", "source_dependency_incarnation", "source_dependency_sequence",
	"source_dependency_publication_id", "source_dependency_provider",
}

func canonicalSealSavedValues(t *testing.T, r canonicalSQLRow) []any {
	t.Helper()
	out := make([]any, len(canonicalSealSavedColumns))
	for i, name := range canonicalSealSavedColumns {
		out[i] = r.vals[r.index(t, name)]
	}
	return out
}

func (r canonicalSQLRow) without(t *testing.T, names ...string) canonicalSQLRow {
	t.Helper()
	drop := map[string]bool{}
	for _, name := range names {
		drop[name] = true
	}
	out := canonicalSQLRow{}
	for i, c := range r.cols {
		if drop[c] {
			continue
		}
		out.cols = append(out.cols, c)
		out.vals = append(out.vals, r.vals[i])
	}
	return out
}

func tableAllColumns(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query("PRAGMA table_xinfo(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk, hidden int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk, &hidden); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func tableColumns(db *sql.DB, table string) ([]string, error) {
	all, err := tableAllColumns(db, table)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query("PRAGMA table_xinfo(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hidden := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk, hid int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk, &hid); err != nil {
			return nil, err
		}
		if hid != 0 {
			hidden[name] = true
		}
	}
	var out []string
	for _, c := range all {
		if !hidden[c] {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}
