package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"slices"
	"testing"
)

// The fixture was generated outside this native package by the existing
// catalog.ParseBrowseQuery -> CompileBrowseSelection path. Native tests replay
// its actual SQL over source years without importing any legacy initialization.
func TestRBScalarFilterCompiledNumericParity(t *testing.T) {
	data, err := os.ReadFile("testdata/rb-scalar-numeric-parity.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Query json.RawMessage
		Where string
		Args  []any
	}
	if err = json.Unmarshal(plain, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1424 {
		t.Fatal("incomplete frozen public compiler matrix", len(vectors))
	}
	years := []int64{-1e9, -5, -1, 0, 1, 1799, 1800, 1809, 1989, 1990, 1999, 2000, 2009, 2190, 2199, 2200, 1e9}
	f := newBrowseSource(t)
	f.exec(t, "DELETE FROM catalog_entities WHERE id>?", int64(len(years)))
	for i, year := range years {
		f.exec(t, "UPDATE catalog_entities SET year=? WHERE id=?", year, int64(i+1))
	}
	for _, vector := range vectors {
		filter, err := compileRBScalarFilter(vector.Query)
		if err != nil {
			t.Fatal(string(vector.Query), err)
		}
		var got []int64
		for i, year := range years {
			if filter.matches(rbFilterFacts{year: year}) {
				got = append(got, int64(i+1))
			}
		}
		rows, err := f.conn.QueryContext(t.Context(), "SELECT id FROM catalog_entities AS e WHERE "+vector.Where+" ORDER BY id", vector.Args...)
		if err != nil {
			t.Fatal(string(vector.Query), vector.Where, err)
		}
		var want []int64
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			want = append(want, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || !slices.Equal(got, want) {
			t.Fatal(string(vector.Query), vector.Where, vector.Args, "native", got, "public", want, err)
		}
	}
}
