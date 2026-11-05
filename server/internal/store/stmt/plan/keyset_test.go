package plan_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Source facts and the exhaustive reference do not call SQL or the candidate's
// cursor/predicate code. This is the large tied-group/NULL continuation control
// required by V, not a full browse/authority oracle.
func TestPlanTwoStreamKeysetsAgainstSourceFacts(t *testing.T) {
	conn, _, f, _ := fixture(t)
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "DELETE FROM items"); err != nil {
		t.Fatal(err)
	}
	type fact struct {
		id, library int64
		key         sql.NullInt64
	}
	facts := []fact{}
	for id := int64(1); id <= 1035; id++ {
		key := sql.NullInt64{Int64: 1080, Valid: true}
		if id <= 5 {
			key.Valid = false
		} else if id > 1029 {
			key.Int64 = 2160
		}
		library := int64(1)
		if id == 1028 {
			library = 2
		}
		facts = append(facts, fact{id: id, library: library, key: key})
		var value any
		if key.Valid {
			value = key.Int64
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO items VALUES(?,?,1,?)", id, library, value); err != nil {
			t.Fatal(err)
		}
	}
	hash := sha256.New()
	fmt.Fprint(hash, f.SHA256)
	for _, source := range facts {
		fmt.Fprintf(hash, "|%d/%d/%t/%d", source.id, source.library, source.key.Valid, source.key.Int64)
	}
	f.SHA256 = hex.EncodeToString(hash.Sum(nil))
	for _, direction := range []string{"asc", "desc"} {
		t.Run(direction, func(t *testing.T) {
			// Closed harness-only variants. Product callers register their own compiled
			// statements; this test never provides a SQL-builder API to a serving path.
			order, compare := "ASC", ">"
			startID := int64(0)
			if direction == "desc" {
				order, compare = "DESC", "<"
				startID = 1 << 62
			}
			base := "SELECT id,key FROM items INDEXED BY item_sort WHERE library_id=? AND kind=? AND " + predicate
			orderBy := " ORDER BY key " + order + ",id " + order + " LIMIT ?"
			defs := []stmt.Definition{}
			add := func(id, suffix string, extra []stmt.Param, bindings []any) {
				params := []stmt.Param{{Name: "library", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}}
				params = append(params, extra...)
				params = append(params, stmt.Param{Name: "limit", Type: stmt.Integer})
				defs = append(defs, stmt.Definition{ID: stmt.ID(direction + "." + id), SQL: base + suffix, Owner: "keyset-control", CallSites: []string{"keyset_test.go"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "large-tie", Bindings: bindings}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"item_sort"}}})
			}
			add("first", " AND key IS NOT NULL"+orderBy, nil, []any{int64(1), int64(1), int64(7)})
			add("null", " AND key IS NULL AND id"+compare+"?"+orderBy, []stmt.Param{{Name: "after", Type: stmt.Integer}}, []any{int64(1), int64(1), startID, int64(7)})
			add("tail", " AND key=? AND id"+compare+"?"+orderBy, []stmt.Param{{Name: "key", Type: stmt.Integer}, {Name: "after", Type: stmt.Integer}}, []any{int64(1), int64(1), int64(1080), int64(500), int64(7)})
			add("next", " AND key"+compare+"?"+orderBy, []stmt.Param{{Name: "key", Type: stmt.Integer}}, []any{int64(1), int64(1), int64(1080), int64(7)})
			registry, err := stmt.New(defs)
			if err != nil {
				t.Fatal(err)
			}
			// All four physical shapes in each direction must satisfy the plan gate.
			golden, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", "keyset-"+direction+".json")
			if *record {
				data, err := golden.JSON()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
					t.Fatal(err)
				}
			}
			expected, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := golden.Compare(expected); err != nil {
				t.Fatal(err)
			}
			read := func(id string, args ...any) []fact {
				s, err := registry.Lookup(stmt.ID(direction + "." + id))
				if err != nil {
					t.Fatal(err)
				}
				bindings := []any{int64(1), int64(1)}
				bindings = append(bindings, args...)
				bound, err := s.Bind(bindings...)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := bound.QueryContext(ctx, conn)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				result := []fact{}
				for rows.Next() {
					var f fact
					f.library = 1
					if err := rows.Scan(&f.id, &f.key); err != nil {
						t.Fatal(err)
					}
					result = append(result, f)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				return result
			}
			nulls := []fact{}
			after := startID
			for {
				page := read("null", after, int64(7))
				if len(page) == 0 {
					break
				}
				nulls = append(nulls, page...)
				after = page[len(page)-1].id
			}
			nonnull := read("first", int64(7))
			for len(nonnull) > 0 {
				last := nonnull[len(nonnull)-1]
				page := read("tail", last.key.Int64, last.id, int64(7))
				if len(page) < 7 {
					page = append(page, read("next", last.key.Int64, int64(7-len(page)))...)
				}
				if len(page) == 0 {
					break
				}
				nonnull = append(nonnull, page...)
				if len(nonnull) > len(facts) {
					t.Fatal("cursor loop or duplicate rows")
				}
			}
			got := append(slices.Clone(nulls), nonnull...)
			if direction == "desc" {
				got = append(slices.Clone(nonnull), nulls...)
			}
			want := []fact{}
			for _, f := range facts {
				if f.library == 1 {
					want = append(want, f)
				}
			}
			sort.Slice(want, func(i, j int) bool {
				a, b := want[i], want[j]
				cmp := 0
				if a.key.Valid != b.key.Valid {
					if a.key.Valid {
						cmp = 1
					} else {
						cmp = -1
					}
				} else if a.key.Valid && a.key.Int64 != b.key.Int64 {
					if a.key.Int64 < b.key.Int64 {
						cmp = -1
					} else {
						cmp = 1
					}
				} else if a.id < b.id {
					cmp = -1
				} else {
					cmp = 1
				}
				if direction == "desc" {
					return cmp > 0
				}
				return cmp < 0
			})
			ids := func(rows []fact) []int64 {
				r := []int64{}
				for _, f := range rows {
					r = append(r, f.id)
				}
				return r
			}
			if !reflect.DeepEqual(ids(got), ids(want)) {
				t.Fatal(fmt.Sprintf("%s keyset differs from source order", direction))
			}
		})
	}
}
