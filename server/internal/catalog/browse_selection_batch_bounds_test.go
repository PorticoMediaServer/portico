package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestSelectionBatchCumulativeSourceBytes(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(lib, "/m/a.mkv", "A", 2020)
	values := []string{}
	for i := 0; i < 100; i++ {
		values = append(values, fmt.Sprintf("%03d", i)+strings.Repeat("x", 128))
	}
	c.Attributes(item.ID, "tag", values...)
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	node, err := ParseBrowseQuery(json.RawMessage(`{"variant":"predicate","field":"tag","operator":"equals","value":"missing"}`), "query")
	if err != nil {
		t.Fatal(err)
	}
	state, oracle, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "m", Pivot: "movies", Query: node, Limit: 1}, HomeActionAuthority{})
	const quantum = 512
	hit := false
	total := 0
	for !state.Complete() {
		next, work, err := s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{Bytes: quantum})
		if err != nil {
			t.Fatal(err)
		}
		if work.ExaminedBytes != work.Rows*selectionBatchProbeBytes+work.DecodedBytes {
			t.Fatal("source accounting", work)
		}
		if work.ExaminedBytes > quantum+work.TransientScalarBytes+selectionBatchProbeBytes {
			t.Fatal("more than one scalar overrun", work)
		}
		if work.DecodedBytes > work.TransientScalarBytes && work.ExaminedBytes >= quantum {
			hit = true
		}
		if work.Bytes < next.Bytes() || work.Bytes > SelectionBatchMaximumBytes {
			t.Fatal("source bytes confused with retained capacity", work)
		}
		total += work.DecodedBytes
		state = next
	}
	if !hit || total < 100*128 {
		t.Fatalf("did not exercise cumulative small scalars hit=%v bytes=%d", hit, total)
	}
	if got := finishSelectionBatch(t, s, state, witness, HomeActionAuthority{}, 1); !reflect.DeepEqual(got, oracle) {
		t.Fatal(got, oracle)
	}
}

func TestSelectionBatchExceptionalScalarYieldsBeforeNextUnit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(lib, "/m/a.mkv", "A", 2020)
	value := strings.Repeat("x", SelectionBatchMaximumBytes+1)
	c.Attributes(item.ID, "tag", value, "z")
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	node, err := ParseBrowseQuery(json.RawMessage(`{"variant":"predicate","field":"tag","operator":"equals","value":"missing"}`), "query")
	if err != nil {
		t.Fatal(err)
	}
	state, _, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "m", Pivot: "movies", Query: node, Limit: 1}, HomeActionAuthority{})
	for !state.Complete() {
		next, one, err := s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{Rows: 1})
		if err != nil {
			t.Fatal(err)
		}
		if one.TransientScalarBytes > SelectionBatchMaximumBytes {
			actual, work, err := s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{})
			if err != nil {
				t.Fatal(err)
			}
			if work.Rows != 1 || work.DecodedBytes != one.DecodedBytes || !reflect.DeepEqual(actual, next) {
				t.Fatalf("advanced past exceptional scalar: %+v", work)
			}
			return
		}
		state = next
	}
	t.Fatal("oversize scalar not examined")
}

func TestSelectionBatchPreviousMemberTombstonesRawProgressAndPlan(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(lib, "/m/a.mkv", "A", 2020)
	c.Drain()
	c.Exec(`UPDATE catalog_generations SET retaining=1`)
	for i := 1; i <= 64; i++ {
		c.Exec(`INSERT INTO catalog_browse_memberships_previous(generation,present,entity_id,item_id,source) SELECT generation,0,?,?,0 FROM catalog_generations`, i, item.ID+1000)
	}
	c.Exec(`INSERT INTO catalog_browse_memberships_previous(generation,present,entity_id,item_id,source) SELECT generation,1,1000,?,0 FROM catalog_generations`, item.ID)
	s := New(c.DB)
	if err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		test := selectionBatchTest{owners: 1}
		cursor := selectionBatchRelationCursor{phase: 1, rawOwner: item.ID}
		for i := 1; i <= 64; i++ {
			done, yes, err := read.selectionBatchPreviousMember(test, &cursor, new(int))
			if err != nil {
				return err
			}
			if done || yes || cursor.previousAfter[0] != int64(i) || cursor.phase != 1 || cursor.pending != nil {
				t.Fatalf("tombstone run skipped at raw limit1: %d %+v", i, cursor)
			}
		}
		if _, _, err := read.selectionBatchPreviousMember(test, &cursor, new(int)); err != nil {
			return err
		}
		if cursor.previousAfter[0] != 1000 || cursor.phase != 2 || !reflect.DeepEqual(cursor.pending, []any{int64(1000), int64(0)}) {
			t.Fatalf("no real match after charged history: %+v", cursor)
		}
		rows, err := dbwork.Snapshot(ctx).QueryContext(ctx, "EXPLAIN QUERY PLAN "+selectionBatchPreviousMembershipRawSQL, 0, 0, 0)
		if err != nil {
			return err
		}
		defer rows.Close()
		plan := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				return err
			}
			plan += detail + "\n"
		}
		if err = rows.Err(); err != nil {
			return err
		}
		t.Log("production raw previous plan:\n" + plan)
		if strings.Contains(plan, "TEMP B-TREE") || !strings.Contains(plan, "SEARCH catalog_browse_memberships_previous USING PRIMARY KEY") {
			t.Fatal("unbounded raw source plan", plan)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSelectionBatchSearchQualification(t *testing.T) {
	for _, text := range []string{"日本", strings.Repeat("long", 20)} {
		got, err := selectionBatchCompileTest(BrowseNode{Field: "search", Operator: "contains", Value: text}, "p", true)
		if err != nil || got.search == nil {
			t.Fatalf("local grouped fallback %q: %+v %v", text, got, err)
		}
	}

	for _, text := range []string{"a", "日本", strings.Repeat("long", 20), "alpha"} {
		got, err := selectionBatchCompileTest(BrowseNode{Field: "search", Operator: "contains", Value: text}, "p", false)
		if err != nil || got.search == nil {
			t.Fatalf("local bare search %q: %+v %v", text, got, err)
		}
	}
	_, s, names := browseFixture(t)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	node, err := ParseBrowseQuery(json.RawMessage(fmt.Sprintf(`{"variant":"group","all":[{"variant":"predicate","field":"memberOf","operator":"equals","value":%q},{"variant":"predicate","field":"search","operator":"contains","value":"alpha"}]}`, names["m1"].Public)), "query")
	if err != nil {
		t.Fatal(err)
	}
	state, oracle, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "a", Pivot: "movies", Query: node, Limit: 1}, HomeActionAuthority{})
	got := finishSelectionBatch(t, s, state, witness, HomeActionAuthority{}, 1)
	if !reflect.DeepEqual(got, oracle) {
		t.Fatal(got, oracle)
	}
}

func TestSelectionBatchRestrictionChargesSourceAndTermKey(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(lib, "/m/a.mkv", "A", 2020)
	value := strings.Repeat("x", 200)
	c.Attributes(item.ID, "label", value)
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"private"}}}
	state, _, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "m", Pivot: "movies", Limit: 1}, HomeActionAuthority{})
	found := false
	for !state.Complete() {
		next, work, err := s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{Rows: 1})
		if err != nil {
			t.Fatal(err)
		}
		if work.DecodedBytes >= 2*len(value) {
			found = true
		}
		state = next
	}
	if !found {
		t.Fatal("restriction comparison omitted source_value or term.value_key bytes")
	}
}

func TestSelectionBatchPreviousTombstonesOneRowLimitOracle(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(lib, "/m/a.mkv", "A", 2020)
	for i := 0; i < 64; i++ {
		c.Collection(lib, fmt.Sprintf("Empty %02d", i))
	}
	target := c.Collection(lib, "Target", item)
	c.Drain()
	c.Exec(`UPDATE catalog_generations SET retaining=1`)
	for id := int64(2); id < target.ID; id++ {
		c.Exec(`INSERT INTO catalog_browse_memberships_previous(generation,present,entity_id,item_id,source) SELECT generation,0,?,?,1 FROM catalog_generations`, id, item.ID)
	}
	c.Exec(`INSERT INTO catalog_browse_memberships_previous(generation,present,entity_id,item_id,source) SELECT g.generation,1,m.entity_id,m.item_id,m.source FROM catalog_generations g CROSS JOIN catalog_browse_memberships m WHERE m.entity_id=? AND m.item_id=?`, target.ID, item.ID)
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	node, err := ParseBrowseQuery(json.RawMessage(fmt.Sprintf(`{"variant":"predicate","field":"memberOf","operator":"equals","value":%q}`, target.Public)), "query")
	if err != nil {
		t.Fatal(err)
	}
	state, oracle, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "m", Pivot: "movies", Query: node, Limit: 1}, HomeActionAuthority{})
	if oracle.Total != 1 {
		t.Fatalf("fixture oracle %+v", oracle)
	}
	seen := map[int64]bool{}
	for !state.Complete() {
		previous := state.candidate.relation.previousAfter[0]
		next, work, err := s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{Rows: 1})
		if err != nil {
			t.Fatal(err)
		}
		if work.Rows != 1 {
			t.Fatal("raw row budget", work)
		}
		after := next.candidate.relation.previousAfter[0]
		if after > previous {
			if after != previous+1 && previous != 0 {
				t.Fatalf("raw history skipped %d -> %d", previous, after)
			}
			seen[after] = true
		}
		state = next
	}
	if len(seen) < 64 {
		t.Fatalf("only charged %d historical keys", len(seen))
	}
	got := finishSelectionBatch(t, s, state, witness, HomeActionAuthority{}, 1)
	if !reflect.DeepEqual(got, oracle) {
		t.Fatal(got, oracle)
	}
}

// Rejected credit labels are source input too. Each guard is a separate unit,
// so two legal large labels never consume a multi-scalar budget exception.
func TestSelectionBatchCreditGuardBytesAndYield(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(lib, "/m/a.mkv", "A", 2020)
	label := strings.Repeat("界", 200)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetCreditsTx(ctx, tx, item.ID, "fixture", []compactcatalog.Credit{{PersonKey: "person", PersonName: "Person", CreditedName: "Person", Role: label, Department: label}})
	})
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	for _, field := range []string{"cast", "crew", "actor", "director", "writer"} {
		t.Run(field, func(t *testing.T) {
			node, err := ParseBrowseQuery(json.RawMessage(fmt.Sprintf(`{"variant":"predicate","field":%q,"operator":"equals","value":"missing"}`, field)), "query")
			if err != nil {
				t.Fatal(err)
			}
			state, oracle, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "m", Pivot: "movies", Query: node, Limit: 1}, HomeActionAuthority{})
			guards := 0
			for !state.Complete() {
				next, one, err := s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{Rows: 1})
				if err != nil {
					t.Fatal(err)
				}
				if one.DecodedBytes >= len(label) {
					guards++
					actual, work, err := s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{Bytes: 512})
					if err != nil {
						t.Fatal(err)
					}
					if work.Rows != 1 || work.DecodedBytes != len(label) || work.TransientScalarBytes != len(label) || !reflect.DeepEqual(next, actual) {
						t.Fatalf("guard did not yield after one lossless scalar: %+v", work)
					}
				}
				state = next
			}
			expected := 1
			if field == "director" || field == "writer" {
				expected = 2
			}
			if guards != expected {
				t.Fatalf("guard scalar units=%d want %d", guards, expected)
			}
			got := finishSelectionBatch(t, s, state, witness, HomeActionAuthority{}, 1)
			if !reflect.DeepEqual(got, oracle) {
				t.Fatalf("got %+v want %+v", got, oracle)
			}
		})
	}
}

func TestSelectionBatchCreditGuardOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	labels := [][2]string{{"Director", "Other"}, {"Other", "Directing"}, {"Writer", "Other"}, {"Other", "Writing"}, {"Actor", "Acting"}, {"Other", "Other"}}
	for i, pair := range labels {
		item := c.Movie(lib, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Film %d", i), 2020)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetCreditsTx(ctx, tx, item.ID, "fixture", []compactcatalog.Credit{{PersonKey: "same-person", PersonName: "Guard Name", CreditedName: "Guard Name", Role: pair[0], Department: pair[1]}})
		})
	}
	c.Drain()
	var token string
	if err := c.DB.QueryRow(`SELECT token FROM catalog_people LIMIT 1`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	for _, field := range []string{"cast", "crew", "actor", "director", "writer"} {
		for _, operator := range []string{"equals", "not-in"} {
			t.Run(field+"/"+operator, func(t *testing.T) {
				value := any("Guard Name")
				if field == "cast" || field == "crew" {
					value = token
				}
				if operator == "not-in" {
					value = []any{value}
				}
				raw, _ := json.Marshal(BrowseNode{Field: field, Operator: operator, Value: value})
				query, err := ParseBrowseQuery(raw, "query")
				if err != nil {
					t.Fatal(err)
				}
				state, oracle, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "m", Pivot: "movies", Query: query, Limit: 1}, HomeActionAuthority{})
				got := finishSelectionBatch(t, s, state, witness, HomeActionAuthority{}, 1)
				if !reflect.DeepEqual(got, oracle) {
					t.Fatalf("got %+v want %+v", got, oracle)
				}
			})
		}
	}
}
