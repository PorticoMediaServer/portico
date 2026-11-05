package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

func TestRowBudgetPreflightAndLengthRestoration(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "budget.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before, err := Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		query   string
		bytes   int64
		rawRows uint32
	}{
		{"SELECT NULL AS v", 64, 1},
		{"SELECT CAST(zeroblob(12000) AS TEXT) AS v UNION ALL SELECT CAST(zeroblob(12000) AS TEXT)", 64 << 10, 2},
		{"SELECT 1 AS v UNION ALL SELECT 2", 64 << 10, 1},
	} {
		budget, err := NewRowBudget(test.rawRows, test.bytes)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := conn.QueryContext(WithRowBudget(t.Context(), budget), test.query)
		if err == nil {
			defer rows.Close() // also release the connection on assertion failure
			active, limitErr := Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, -1)
			if limitErr != nil || active >= before || active > int(test.bytes/4) {
				t.Fatal("native materialization limit not installed", active, before, limitErr)
			}
			for rows.Next() {
				var value any
				if err = rows.Scan(&value); err != nil {
					t.Fatal(err)
				}
			}
			err = errors.Join(rows.Err(), rows.Close())
		}
		if !errors.Is(err, ErrRowBudget) {
			t.Fatal("native window not enforced", test.query, err)
		}
		if test.rawRows == 2 {
			var native *Error
			if !errors.As(err, &native) || native.Code()&255 != sqlite3.SQLITE_TOOBIG {
				t.Fatal("next VM value was not limited before materialization", err)
			}
		}
		after, err := Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, -1)
		if err != nil || after != before {
			t.Fatal("native limit escaped its row lifetime", before, after, err)
		}
		var healthy int
		if err = conn.QueryRowContext(t.Context(), "SELECT 42").Scan(&healthy); err != nil || healthy != 42 {
			t.Fatal("failed row construction damaged statement ownership", healthy, err)
		}
	}
	// Without the opt-in context, normal driver materialization is unchanged.
	var blob []byte
	if err = conn.QueryRowContext(t.Context(), "SELECT zeroblob(131072)").Scan(&blob); err != nil || len(blob) != 131072 {
		t.Fatal(len(blob), err)
	}
}
