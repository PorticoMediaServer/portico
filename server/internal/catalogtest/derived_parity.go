// Complete-row parity support for derived-table owners lives in the existing
// catalogtest fixture boundary; runtime imports remain forbidden by its census.
package catalogtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// DerivedImage keeps every column and typed value, including NULL versus empty text
// and BLOB versus TEXT. Rows are sorted, with duplicates retained. No counts,
// hashes, selected prefixes or tolerance can hide a missing or extra title.
type DerivedImage map[string]DerivedTable
type DerivedTable struct {
	Columns []string
	Rows    []string
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// DerivedCapture reads the requested owner tables in one snapshot. Identifiers are
// source constants, never SQL supplied by users. No column is ignored: an
// owner with a generation-specific layout supplies its own logical projection.
func DerivedCapture(ctx context.Context, db *sql.DB, tables ...string) (DerivedImage, error) {
	tx, closeRead, err := dbwork.BeginRead(ctx, db)
	if err != nil {
		return nil, err
	}
	defer closeRead()
	out := DerivedImage{}
	for _, name := range tables {
		if !identifier.MatchString(name) {
			return nil, fmt.Errorf("invalid parity table %q", name)
		}
		rows, err := tx.QueryContext(ctx, `SELECT * FROM "`+name+`"`)
		if err != nil {
			return nil, err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		image := DerivedTable{Columns: cols, Rows: []string{}}
		for rows.Next() {
			values, targets := make([]any, len(cols)), make([]any, len(cols))
			for i := range values {
				targets[i] = &values[i]
			}
			if err = rows.Scan(targets...); err != nil {
				rows.Close()
				return nil, err
			}
			typed := make([]any, len(values))
			for i, v := range values {
				value := v
				if text, ok := v.(string); ok {
					value = []byte(text)
				}
				typed[i] = []any{fmt.Sprintf("%T", v), value}
			}
			encoded, err := json.Marshal(typed)
			if err != nil {
				rows.Close()
				return nil, err
			}
			image.Rows = append(image.Rows, string(encoded))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		sort.Strings(image.Rows)
		out[name] = image
	}
	return out, nil
}

// DerivedDifference reports the first complete row mismatch without overwhelming CI
// logs with a whole catalogue. Both the row count and full row bytes matter.
func DerivedDifference(want, got DerivedImage) error {
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(want) != len(got) {
		return fmt.Errorf("parity table set changed: %d != %d", len(want), len(got))
	}
	for _, name := range names {
		a, b := want[name], got[name]
		if !reflect.DeepEqual(a.Columns, b.Columns) {
			return fmt.Errorf("%s: columns differ: %v != %v", name, a.Columns, b.Columns)
		}
		if len(a.Rows) != len(b.Rows) {
			return fmt.Errorf("%s: row count %d != %d", name, len(a.Rows), len(b.Rows))
		}
		for i := range a.Rows {
			if a.Rows[i] != b.Rows[i] {
				return fmt.Errorf("%s: row %d differs:\nincremental %s\nrebuilt %s", name, i, a.Rows[i], b.Rows[i])
			}
		}
	}
	return nil
}

// DerivedCheck captures settled incremental state, destroys the owner projection,
// invokes its real source-rebuild command and compares the complete result.
// Reset is mandatory: merely rerunning a drain can preserve the same bad data.
// Each family supplies its own reset and rebuild, including dependencies and
// cleanup. This helper deliberately cannot infer a rebuild from dirty markers.
func DerivedCheck(t *testing.T, c *Catalog, tables []string, reset, rebuild func() error) {
	t.Helper()
	ctx := context.Background()
	c.Drain()
	want, err := DerivedCapture(ctx, c.DB, tables...)
	if err != nil {
		t.Fatal(err)
	}
	if reset == nil || rebuild == nil {
		t.Fatal("parity requires destructive reset and source rebuild")
	}
	if err = reset(); err != nil {
		t.Fatal(err)
	}
	empty, err := DerivedCapture(ctx, c.DB, tables...)
	if err != nil {
		t.Fatal(err)
	}
	for name, table := range empty {
		if len(table.Rows) != 0 {
			t.Fatalf("reset left %d rows in %s; rebuild would reuse a projection", len(table.Rows), name)
		}
	}
	if err = rebuild(); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	var remaining int
	if err = c.DB.QueryRow(`SELECT (SELECT count(*) FROM catalog_derivation_failures)+(SELECT count(*) FROM catalog_dirty)+(SELECT count(*) FROM catalog_derivations WHERE rebuilding=1)`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("rebuild has %d failed, dirty or rebuilding units: %v", remaining, err)
	}
	got, err := DerivedCapture(ctx, c.DB, tables...)
	if err != nil {
		t.Fatal(err)
	}
	if err = DerivedDifference(want, got); err != nil {
		t.Fatal(err)
	}
}
