package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type fixtureFacetInput struct {
	members           []readingFacetMember
	rejected          map[int64]bool
	at, work, visited int
	readBytes         int // optional transient scalar/read size
}

func (in *fixtureFacetInput) next(_ *Service, b *readingFacetBudget) (readingFacetMember, bool, bool, bool, error) {
	// Three nested history rows per fixture member; deliberately resumable even
	// when a quantum has room for less than one complete title.
	for b.available() {
		b.charge(max(32, in.readBytes))
		in.visited++
		if in.at == len(in.members) {
			return readingFacetMember{}, false, false, true, nil
		}
		in.work++
		if in.work < 4 {
			continue
		}
		in.work = 0
		m := in.members[in.at]
		in.at++
		return m, !in.rejected[m.Item], true, false, nil
	}
	return readingFacetMember{}, false, false, false, nil
}
func (in *fixtureFacetInput) reset()           { in.at, in.work = 0, 0 }
func (in *fixtureFacetInput) scalarBytes() int { return 0 }
func (in *fixtureFacetInput) bytes() int       { return 64 } // fixture corpus is source data, not reducer state
func fixtureFacetMembers(labels []string, counts []int) []readingFacetMember {
	var rows []readingFacetMember
	for i, label := range labels {
		for j := 0; j < counts[i]; j++ {
			rows = append(rows, readingFacetMember{Key: compactcatalog.ReadingValueKey(label), Label: label, Source: "folder", Item: int64(len(rows) + 1)})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.Item < b.Item
	})
	return rows
}
func runFixtureFacet(t *testing.T, request FacetRequest, in *fixtureFacetInput) *readingFacetReduction {
	t.Helper()
	r, err := newReadingFacetReduction(request, "full-witness", in, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for turns := 0; turns < 100000; turns++ {
		b := newReadingFacetBudget(7, 512, time.Second)
		before := in.visited
		done, err := r.step(nil, "full-witness", b)
		if err != nil {
			t.Fatal(err)
		}
		if in.visited-before > 7 {
			t.Fatal("raw work budget exceeded")
		}
		if len(r.page) > r.request.Limit {
			t.Fatal("unbounded page")
		}
		if done {
			return r
		}
	}
	t.Fatal("no progress")
	return nil
}
func TestReadingFacetReductionCountOffsetsAndRawProgress(t *testing.T) {
	labels := []string{"A", "a", "B", "C", "c", "D", "E", "F", "G", "H", "I", "J"}
	counts := []int{2, 3, 7, 1, 2, 6, 1, 4, 7, 2, 3, 5}
	rows := fixtureFacetMembers(labels, counts)
	// Expected title/count ordering from the old SQL oracle's value counts.
	want := []readingFacetChoice{{"a", "a", 5}, {"b", "B", 7}, {"c", "c", 3}, {"d", "D", 6}, {"e", "E", 1}, {"f", "F", 4}, {"g", "G", 7}, {"h", "H", 2}, {"i", "I", 3}, {"j", "J", 5}}
	for _, order := range []string{"title", "count"} {
		expected := append([]readingFacetChoice{}, want...)
		if order == "count" {
			sort.Slice(expected, func(i, j int) bool {
				if expected[i].count != expected[j].count {
					return expected[i].count > expected[j].count
				}
				return expected[i].key < expected[j].key
			})
		}
		for offset := 0; offset < 15; offset++ {
			for limit := 1; limit < 5; limit++ {
				r := runFixtureFacet(t, FacetRequest{Library: "reading", Field: "author", Sort: order, Offset: offset, Limit: limit}, &fixtureFacetInput{members: rows})
				page, ok := r.result()
				if !ok || page.Total != len(expected) {
					t.Fatal(page, ok)
				}
				lo := min(offset, len(expected))
				hi := min(lo+limit, len(expected))
				if len(page.Values) != hi-lo {
					t.Fatalf("%s offset %d: %+v", order, offset, page)
				}
				for i, v := range page.Values {
					e := expected[lo+i]
					if v.Value != e.label || v.Count != e.count {
						t.Fatalf("%s offset %d got %+v want %+v", order, offset, v, e)
					}
				}
				if r.passes > bits.Len(uint(len(rows)))+3 {
					t.Fatalf("offset-dependent passes: %d", r.passes)
				}
			}
		}
	}
}
func TestReadingFacetReductionSpellingUnicodeAndRejectedOwners(t *testing.T) {
	rows := fixtureFacetMembers([]string{"Café  Author", "CAFÉ AUTHOR", "café author", "Cafe Author", "Space\tName", "SPACE NAME"}, []int{2, 2, 8, 3, 1, 1})
	rejected := map[int64]bool{}
	for i := range rows {
		if rows[i].Label == "CAFÉ AUTHOR" {
			rows[i].Source = "owner"
		}
		if rows[i].Label == "Café  Author" {
			rows[i].Source = "owner"
			rejected[rows[i].Item] = true
		}
	}
	r := runFixtureFacet(t, FacetRequest{Library: "reading", Field: "series", Q: "É", Limit: 20}, &fixtureFacetInput{members: rows, rejected: rejected})
	page, _ := r.result()
	if page.Total != 1 || len(page.Values) != 1 || page.Values[0].Value != "CAFÉ AUTHOR" || page.Values[0].Count != 10 || string(page.Values[0].SeriesID) != compactcatalog.ReadingSeriesID("reading", "CAFÉ AUTHOR") {
		t.Fatal(page)
	}
	// Owner frequency wins; total automatic frequency cannot outvote an owner.
	for i := range rows {
		if rows[i].Label == "Café  Author" {
			delete(rejected, rows[i].Item)
		}
	}
	r = runFixtureFacet(t, FacetRequest{Library: "reading", Field: "author", Q: "é", Limit: 20}, &fixtureFacetInput{members: rows, rejected: rejected})
	page, _ = r.result()
	if page.Values[0].Value != "CAFÉ AUTHOR" || page.Values[0].Count != 12 {
		t.Fatal(page)
	} // owner tie: binary capitals first
}
func TestReadingFacetDeepCountOffsetRetainsOnlyPage(t *testing.T) {
	labels := make([]string, 1000)
	counts := make([]int, 1000)
	expected := make([]readingFacetChoice, 1000)
	for i := range labels {
		labels[i] = fmt.Sprintf("Author-%04d", i)
		counts[i] = i%13 + 1
		expected[i] = readingFacetChoice{compactcatalog.ReadingValueKey(labels[i]), labels[i], counts[i]}
	}
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].count != expected[j].count {
			return expected[i].count > expected[j].count
		}
		return expected[i].key < expected[j].key
	})
	r := runFixtureFacet(t, FacetRequest{Field: "author", Sort: "count", Offset: 995, Limit: 3}, &fixtureFacetInput{members: fixtureFacetMembers(labels, counts)})
	page, _ := r.result()
	if page.Total != 1000 || len(page.Values) != 3 || cap(r.page) > 4 || r.passes > bits.Len(13)+3 {
		t.Fatal("deep offset grew memory or offset-dependent passes", page.Total, cap(r.page), r.passes)
	}
	for i, v := range page.Values {
		if v.Value != expected[995+i].label || v.Count != expected[995+i].count {
			t.Fatal("deep rank window differs", page.Values)
		}
	}
	tied := fixtureFacetMembers([]string{"Space\tName", "SPACE NAME"}, []int{1, 1})
	tie := runFixtureFacet(t, FacetRequest{Field: "author", Q: "s", Limit: 1}, &fixtureFacetInput{members: tied})
	p, _ := tie.result()
	if p.Values[0].Value != "SPACE NAME" {
		t.Fatal("automatic binary tie changed", p)
	}
}

func TestReadingFacetReductionWitnessAndLosslessScalar(t *testing.T) {
	rows := fixtureFacetMembers([]string{"Label"}, []int{2})
	r, err := newReadingFacetReduction(FacetRequest{Field: "author"}, "w", &fixtureFacetInput{members: rows}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.result(); ok {
		t.Fatal("partial exposed")
	}
	if _, err = r.step(nil, "changed", newReadingFacetBudget(4, 4096, time.Second)); !errors.Is(err, ErrReadingProjectionChanged) {
		t.Fatal(err)
	}
	if _, ok := r.result(); ok {
		t.Fatal("stale result exposed")
	}
	b := newReadingFacetBudget(4096, 1024, time.Second)
	b.charge(100000)
	if b.Bytes != -98976 || b.available() || !b.oversized {
		t.Fatal("scalar truncated or not charged", b)
	}
	huge := strings.Repeat("é", 20000)
	in := &fixtureFacetInput{members: fixtureFacetMembers([]string{huge}, []int{1})}
	r, err = newReadingFacetReduction(FacetRequest{Field: "author", Limit: 1}, "w", in, 4096)
	if err != nil {
		t.Fatal(err)
	}
	failure := error(nil)
	for i := 0; i < 20; i++ {
		_, failure = r.step(nil, "w", newReadingFacetBudget(4, 4096, time.Second))
		if failure != nil {
			break
		}
	}
	if !errors.Is(failure, errReadingFacetCapacity) || r.input != nil || len(r.page) != 0 || r.bytes() > 4096 {
		t.Fatal("oversize payload retained in preparation state", failure, r.bytes())
	}
	if _, ok := r.result(); ok {
		t.Fatal("capacity failure became a partial result")
	}

}

func TestReadingFacetSQLInputRequiresProductionOrderedIndex(t *testing.T) {
	c, s, viewer, _ := readingBrowseFixture(t, "book")
	c.Drain()
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		_, err := s.WithContext(ctx).newReadingFacetSQLInput(viewer, FacetRequest{Library: "reading", Field: "author"}, time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func runSQLFacet(t *testing.T, s *Service, viewer Viewer, request FacetRequest) (FacetPage, *readingFacetReduction) {
	t.Helper()
	var in *readingFacetSQLInput
	var witness string
	err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var err error
		ss := s.WithContext(ctx)
		witness, err = ss.browseExactWitness(request.Library, viewer.Profile, viewer.EffectiveRestrictions())
		if err != nil {
			return err
		}
		in, err = ss.newReadingFacetSQLInput(viewer, request, time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newReadingFacetReduction(request, witness, in, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for turns := 0; turns < 10000; turns++ {
		done := false
		err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
			var err error
			b := newReadingFacetBudget(2, 4096, time.Second)
			ss := s.WithContext(ctx)
			current, e := ss.browseExactWitness(request.Library, viewer.Profile, viewer.EffectiveRestrictions())
			if e != nil {
				return e
			}
			done, err = r.step(ss, current, b)
			if b.Raw < 0 {
				t.Fatal("nested raw work exceeded")
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if done {
			page, ok := r.result()
			if !ok {
				t.Fatal("result absent")
			}
			return page, r
		}
	}
	t.Fatal("SQL source made no progress")
	return FacetPage{}, nil
}
func TestReadingFacetSQLInputOldOracleParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"book", "comic"} {
		t.Run(kind, func(t *testing.T) {
			c, s, viewer, items := readingBrowseFixture(t, kind)
			c.Exec(`INSERT INTO catalog_item_details(entity_id,added_text) VALUES(?,'2026-10-27T12:00:00Z') ON CONFLICT(entity_id) DO UPDATE SET added_text=excluded.added_text`, items[0].ID)
			// An unsupported version precedes a valid publication; unavailable remains
			// eligible. Retiring one title must reject its raw attribution member.
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: fmt.Sprintf("/extra-%s.epub", kind), Container: "epub"})
				if err != nil {
					return err
				}
				if err = compactcatalog.LinkAssetTx(ctx, tx, items[0].ID, asset, compactcatalog.Link{}); err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'epub')`, items[0].ID, asset)
				return err
			})
			c.Drain()
			queries := []*BrowseNode{nil, {Field: "author", Operator: "contains", Value: "café author"}, {Not: &BrowseNode{Field: "series", Operator: "equals", Value: "missing"}}, {All: []BrowseNode{{Field: "title", Operator: "contains", Value: "title"}, {Field: "year", Operator: "is-missing"}}}, {Field: "dateAdded", Operator: "within-last-days", Value: float64(2)}}
			s.Clock = func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }
			for _, field := range []string{"author", "series"} {
				for _, order := range []string{"title", "count"} {
					for _, query := range queries {
						for _, needle := range []string{"", "é", "a"} {
							for offset := 0; offset < 3; offset++ {
								request := FacetRequest{Library: "reading", Field: field, Sort: order, Query: query, Q: needle, Offset: offset, Limit: 1}
								want, err := s.Facets(viewer, request)
								if err != nil {
									t.Fatal(err)
								}
								got, _ := runSQLFacet(t, s, viewer, request)
								got.Revision = want.Revision
								if !reflect.DeepEqual(got, want) {
									t.Fatalf("%s %s %q offset %d query %+v\ngot %+v\nwant %+v", field, order, needle, offset, query, got, want)
								}
							}
						}
					}
				}
			}
			age := 10
			viewer.Restrictions = identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true}
			// Current membership/attribute semantics, including no-membership admits.
			request := FacetRequest{Library: "reading", Field: "author", Limit: 20}
			want, err := s.Facets(viewer, request)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := runSQLFacet(t, s, viewer, request)
			got.Revision = want.Revision
			if !reflect.DeepEqual(got, want) {
				t.Fatal(got, want)
			}
		})
	}
}

func TestReadingFacetScalarCompilerOldOracle(t *testing.T) {
	c, s, _, items := readingBrowseFixture(t, "book")
	c.Drain()
	nodes := []BrowseNode{}
	for _, field := range []string{"author", "series"} {
		for _, op := range []string{"equals", "contains", "not-equals", "starts-with"} {
			nodes = append(nodes, BrowseNode{Field: field, Operator: op, Value: "café author"})
		}
		for _, op := range []string{"in", "not-in", "contains-any", "contains-all"} {
			nodes = append(nodes, BrowseNode{Field: field, Operator: op, Value: []any{"café author", "CAFÉ\tAUTHOR"}})
		}
		for _, op := range []string{"is-present", "is-missing"} {
			nodes = append(nodes, BrowseNode{Field: field, Operator: op})
		}
	}
	nodes = append(nodes, BrowseNode{Field: "title", Operator: "contains", Value: "title"}, BrowseNode{Field: "year", Operator: "is-missing"}, BrowseNode{Field: "dateAdded", Operator: "not-equals", Value: "2026-10-27"}, BrowseNode{Not: &BrowseNode{Any: []BrowseNode{{Field: "author", Operator: "is-missing"}, {Field: "title", Operator: "starts-with", Value: "Title 1"}}}})
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		ss := s.WithContext(ctx)
		scope, err := ss.readingBrowseScope("reading")
		if err != nil {
			return err
		}
		for _, node := range nodes {
			now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
			old := &browseCompiler{reading: scope, now: now}
			oracle, err := old.compile(&node)
			if err != nil {
				return err
			}
			direct := &browseCompiler{reading: scope, now: now}
			leaves := 0
			scalar, err := compileReadingFacetNode(direct, &node, 0, &leaves)
			if err != nil {
				return err
			}
			for _, item := range items {
				var want, got bool
				err = ss.read().QueryRow(`SELECT COALESCE((`+oracle+`),0) FROM catalog_browse_rows e WHERE e.entity_id=?`, append(append([]any{}, old.args...), item.ID)...).Scan(&want)
				if err != nil {
					return err
				}
				err = ss.read().QueryRow(`SELECT COALESCE((`+scalar+`),0) FROM catalog_browse_rows e JOIN reading_attribution_members cm ON cm.item_id=e.entity_id AND cm.library_id=e.library_id AND cm.generation=? WHERE e.entity_id=?`, append(append([]any{}, direct.args...), scope.generation.Ready, item.ID)...).Scan(&got)
				if err != nil {
					return err
				}
				if got != want {
					t.Fatalf("node %+v item %d direct %v old %v", node, item.ID, got, want)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadingFacetNestedMembershipAttributesResume(t *testing.T) {
	c, s, _, items := readingBrowseFixture(t, "book")
	values := make([]string, 31)
	for i := range values {
		values[i] = fmt.Sprintf("label-%02d", i)
	}
	c.Attributes(items[0].ID, "label", values...)
	c.Attributes(items[0].ID, "contentRating", "PG")
	c.Drain()
	age := 12
	policies := []identity.ContentRestrictions{{MaximumAge: &age, BlockUnrated: true}, {BlockedLabels: []string{"label-30"}}, {MemberDeniedLabels: []string{"LABEL-30"}}, {BlockUnrated: true}, {}}
	for _, policy := range policies {
		var in *readingFacetSQLInput
		want := false
		err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
			ss := s.WithContext(ctx)
			scope, err := ss.readingBrowseScope("reading")
			if err != nil {
				return err
			}
			clause, args := EntityRestrictionSQL("e.entity_id", policy)
			if err = ss.read().QueryRow(`SELECT `+clause+` FROM catalog_browse_rows e WHERE e.entity_id=?`, append(args, items[0].ID)...).Scan(&want); err != nil {
				return err
			}
			// Starts after raw-member admission: no fixture index, and this test makes
			// no claim about ordered member seeking until the production slot lands.
			in = &readingFacetSQLInput{scope: *scope, library: "reading", field: "author", clause: "1", restrictions: policy, current: readingFacetMember{Item: items[0].ID}, phase: "scalar"}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		finished := false
		turns := 0
		for ; turns < 1000; turns++ {
			err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
				b := newReadingFacetBudget(1, readingFacetByteLimit, time.Second)
				_, got, present, eof, err := in.next(s.WithContext(ctx), b)
				if b.Raw < 0 {
					t.Fatal("nested cursor exceeded single raw unit")
				}
				if eof {
					t.Fatal("member disappeared")
				}
				if present {
					finished = true
					if got != want {
						t.Fatalf("policy %+v got %v want %v", policy, got, want)
					}
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if finished {
				break
			}
		}
		if !finished {
			t.Fatal("nested cursor failed to progress")
		}
		if policy.Active() && turns < 10 {
			t.Fatal("attribute history hidden as one raw unit", turns)
		}
	}
}

func TestReadingFacetProductionIndexSeekPlans(t *testing.T) {
	c, s, viewer, _ := readingBrowseFixture(t, "book")
	c.Drain()
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		ss := s.WithContext(ctx)
		for _, field := range []string{"author", "series"} {
			in, err := ss.newReadingFacetSQLInput(viewer, FacetRequest{Library: "reading", Field: field}, time.Now())
			if err != nil {
				return err
			}
			for _, after := range []readingFacetMember{{}, {Key: "café author", Label: "Café Author", Item: 1}} {
				in.after = after
				query, args := in.memberQuery()
				rows, err := ss.read().Query("EXPLAIN QUERY PLAN "+query, args...)
				if err != nil {
					return err
				}
				indexed := false
				for rows.Next() {
					var id, parent, unused int
					var plan string
					if err = rows.Scan(&id, &parent, &unused, &plan); err != nil {
						rows.Close()
						return err
					}
					if strings.Contains(plan, "TEMP B-TREE") || strings.Contains(plan, "SCAN ") {
						t.Fatalf("unbounded member plan: %s", plan)
					}
					indexed = indexed || strings.Contains(plan, "reading_attribution_"+field+"_facet")
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				if !indexed {
					t.Fatal("facet cursor did not seek the production base index")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadingFacetArchiveObjectCursorChargesRejectedRows(t *testing.T) {
	c, s, _, items := readingBrowseFixture(t, "comic")
	var asset int64
	var token, source, incarnation string
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT a.id,a.token,a.size,a.modified_ns FROM reading_publications p JOIN catalog_assets a ON a.id=p.asset_id WHERE p.item_id=?`, items[0].ID).Scan(&asset, &token, &size, &modified); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id='reading'`).Scan(&source, &incarnation); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES(?,?,?,'old-root',?,'r','{}',?,?)`, fmt.Sprintf("archive-%02d", i), source, token, fmt.Sprintf("archive/%02d.cb7", i), size, modified)
	}
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('archive-99',?,?,?,'archive/99.cb7','r','{}',?,?,'missing')`, source, token, incarnation, size, modified)
	c.Drain()
	// Exercise the local archive witness stage directly, independent of build
	// format tags. The stage must retain unavailable current local objects and
	// charge every rejected object instead of LIMIT after its witness filter.
	var in *readingFacetSQLInput
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		scope, err := s.WithContext(ctx).readingBrowseScope("reading")
		if err != nil {
			return err
		}
		in = &readingFacetSQLInput{scope: *scope, library: "reading", field: "author", clause: "1", current: readingFacetMember{Item: items[0].ID}, asset: asset, token: token, phase: "object"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	for turn := 0; turn < 40; turn++ {
		err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
			b := newReadingFacetBudget(1, 4096, time.Second)
			_, eligible, present, _, err := in.next(s.WithContext(ctx), b)
			if b.Raw < 0 {
				t.Fatal("object raw bound exceeded")
			}
			if present {
				finished = true
				if !eligible || turn < 30 {
					t.Fatal("local witness lost or raw rejects not charged", turn)
				}
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if finished {
			break
		}
	}
	if !finished {
		t.Fatal("object cursor did not reach valid unavailable witness")
	}
}

func TestReadingFacetFullWitnessRejectsLivePredicateDrift(t *testing.T) {
	c, s, viewer, items := readingBrowseFixture(t, "book")
	c.Drain()
	var r *readingFacetReduction
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		ss := s.WithContext(ctx)
		witness, err := ss.browseExactWitness("reading", viewer.Profile, viewer.EffectiveRestrictions())
		if err != nil {
			return err
		}
		request := FacetRequest{Library: "reading", Field: "author"}
		in, err := ss.newReadingFacetSQLInput(viewer, request, time.Now())
		if err != nil {
			return err
		}
		r, err = newReadingFacetReduction(request, witness, in, 1<<20)
		if err != nil {
			return err
		}
		done, err := r.step(ss, witness, newReadingFacetBudget(1, 4096, time.Second))
		if done {
			t.Fatal("unprocessed source exposed")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Attributes(items[0].ID, "label", "new-live-policy-input")
	err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		ss := s.WithContext(ctx)
		current, err := ss.browseExactWitness("reading", viewer.Profile, viewer.EffectiveRestrictions())
		if err != nil {
			return err
		}
		_, err = r.step(ss, current, newReadingFacetBudget(2, 4096, time.Second))
		return err
	})
	if !errors.Is(err, ErrReadingProjectionChanged) {
		t.Fatalf("live epoch change reused partial reduction: %v", err)
	}
	if _, ok := r.result(); ok {
		t.Fatal("stale result exposed")
	}
}

func TestReadingFacetPublicationCursorChargesUnsupportedVersions(t *testing.T) {
	c, s, _, items := readingBrowseFixture(t, "book")
	c.Exec(`UPDATE reading_publications SET format='cbz' WHERE item_id=?`, items[0].ID)
	// Add unsupported historical versions before the last admitted one. The FK
	// requires a link for each, exactly the premise of the existence proof.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < 20; i++ {
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: fmt.Sprintf("/history-%d.pdf", i), Container: "pdf"})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, items[0].ID, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			format := "cbz"
			if i == 19 {
				format = "pdf"
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,?)`, items[0].ID, asset, format); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	var in *readingFacetSQLInput
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		ss := s.WithContext(ctx)
		scope, err := ss.readingBrowseScope("reading")
		if err != nil {
			return err
		}
		var eligible bool
		if err = ss.read().QueryRow(`SELECT ` + scope.eligibleSQL(fmt.Sprint(items[0].ID))).Scan(&eligible); err != nil {
			return err
		}
		if !eligible {
			t.Fatal("old primary existence rejected an admitted version")
		}
		in = &readingFacetSQLInput{scope: *scope, library: "reading", field: "author", clause: "1", current: readingFacetMember{Item: items[0].ID}, phase: "publication"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	for turn := 0; turn < 30; turn++ {
		err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
			b := newReadingFacetBudget(1, 4096, time.Second)
			_, eligible, present, _, err := in.next(s.WithContext(ctx), b)
			if b.Raw < 0 {
				t.Fatal("publication bound exceeded")
			}
			if present {
				finished = true
				if !eligible || turn < 20 {
					t.Fatal("publication history was not charged", turn)
				}
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if finished {
			break
		}
	}
	if !finished {
		t.Fatal("publication cursor made no progress")
	}
}

func TestReadingFacetRetainedMembershipCursorUsesIndexedRuns(t *testing.T) {
	c, s, _, items := readingBrowseFixture(t, "book")
	c.Drain()
	c.Exec(`UPDATE catalog_generations SET retaining=1 WHERE singleton=1`)
	c.Exec(`DELETE FROM catalog_browse_memberships WHERE entity_id=?`, items[0].ID)
	query := `SELECT item_id,source FROM catalog_browse_memberships WHERE entity_id=? AND (item_id,source)>(?,?) ORDER BY item_id,source LIMIT 1`
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		ss := s.WithContext(ctx)
		rows, err := ss.read().Query("EXPLAIN QUERY PLAN "+query, items[0].ID, 0, -1)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, parent, unused int
			var plan string
			if err = rows.Scan(&id, &parent, &unused, &plan); err != nil {
				rows.Close()
				return err
			}
			if strings.Contains(plan, "TEMP B-TREE") {
				t.Fatalf("retained membership materialized before LIMIT: %s", plan)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		var member, source int64
		if err = ss.read().QueryRow(query, items[0].ID, 0, -1).Scan(&member, &source); err != nil {
			return err
		}
		if member != items[0].ID {
			t.Fatal("serving membership lost")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (in *fixtureFacetInput) fence() string { return "" }
func (in *fixtureFacetInput) discard()      { in.members = nil; in.rejected = nil }

func TestReadingFacetPeakReservationPrecedesReads(t *testing.T) {
	in := &fixtureFacetInput{members: fixtureFacetMembers([]string{"A"}, []int{1})}
	r, err := newReadingFacetReduction(FacetRequest{Field: "author"}, "w", in, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	r.reservePeak = func(peak int) error {
		if in.visited != 0 || peak < r.bytes()+8192 {
			t.Fatal("peak was not reserved first", peak)
		}
		return errReadingFacetCapacity
	}
	if _, err = r.step(nil, "w", newReadingFacetBudget(4, 4096, time.Second)); !errors.Is(err, errReadingFacetCapacity) || in.visited != 0 {
		t.Fatal("allocation/read preceded reservation", err)
	}
}

func TestReadingFacetCompilerRejectsMalformedQueries(t *testing.T) {
	malformed := []BrowseNode{{All: []BrowseNode{}}, {All: []BrowseNode{{Field: "title", Operator: "equals", Value: "A"}}, Any: []BrowseNode{{Field: "title", Operator: "equals", Value: "B"}}}, {Field: "author", Operator: "equals", Value: true}, {Field: "author", Operator: "equals", Value: ""}, {Field: "author", Operator: "in", Value: []any{}}, {Field: "title", Operator: "equals", Value: []any{"A"}}, {Field: "year", Operator: "equals", Value: "not a number"}, {Field: "author", Operator: "is-present", Value: "non-null"}, {Field: "dateAdded", Operator: "equals", Value: "invalid-date"}}
	raw := []string{`{"all":[]}`, `{"all":[{"field":"title","operator":"equals","value":"A"}],"any":[{"field":"title","operator":"equals","value":"B"}]}`, `{"field":"author","operator":"equals","value":true}`, `{"field":"author","operator":"equals","value":""}`, `{"field":"author","operator":"in","value":[]}`, `{"field":"title","operator":"equals","value":["A"]}`, `{"field":"year","operator":"equals","value":"not a number"}`, `{"field":"author","operator":"is-present","value":"non-null"}`, `{"field":"dateAdded","operator":"equals","value":"invalid-date"}`}
	for i, node := range malformed {
		if _, err := ParseBrowseQuery(json.RawMessage(raw[i]), "query"); err == nil {
			t.Fatal("test expected public parser rejection", raw[i])
		}
		c := &browseCompiler{reading: &readingBrowseScope{}}
		leaves := 0
		if _, err := compileReadingFacetNode(c, &node, 0, &leaves); err == nil {
			t.Fatalf("malformed query accepted: %+v", node)
		}
	}
}

func TestReadingFacetRelativeRevisionBindsCapturedInstant(t *testing.T) {
	c, s, viewer, _ := readingBrowseFixture(t, "book")
	c.Drain()
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		ss := s.WithContext(ctx)
		witness, err := ss.browseExactWitness("reading", viewer.Profile, viewer.EffectiveRestrictions())
		if err != nil {
			return err
		}
		request := FacetRequest{Library: "reading", Field: "author", Query: &BrowseNode{Field: "dateAdded", Operator: "within-last-days", Value: float64(2)}}
		revisions := []string{}
		for _, at := range []time.Time{time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)} {
			in, err := ss.newReadingFacetSQLInput(viewer, request, at)
			if err != nil {
				return err
			}
			r, err := newReadingFacetReduction(request, witness, in, 1<<20)
			if err != nil {
				return err
			}
			revisions = append(revisions, r.revision)
		}
		if revisions[0] == revisions[1] {
			t.Fatal("relative instant absent from result revision")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Read quantum exhaustion is scheduling, not a result-capacity refusal. Even
// a legal transient scalar may exceed one turn while the retained result fits.
func TestReadingFacetReadQuantumDoesNotPoisonBoundedResult(t *testing.T) {
	for _, tc := range []struct {
		name          string
		read, quantum int
	}{{"ordinary reads cross byte boundary", 32, 63}, {"lossless transient scalar", 8192, 512}} {
		t.Run(tc.name, func(t *testing.T) {
			in := &fixtureFacetInput{members: fixtureFacetMembers([]string{"A", "B"}, []int{2, 1}), readBytes: tc.read}
			r, err := newReadingFacetReduction(FacetRequest{Field: "author", Limit: 2}, "w", in, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			crossed, completed := false, false
			for turn := 0; turn < 100; turn++ {
				b := newReadingFacetBudget(16, tc.quantum, time.Second)
				done, err := r.step(nil, "w", b)
				if err != nil {
					t.Fatal(err)
				}
				crossed = crossed || b.Bytes < 0
				if done {
					completed = true
					break
				}
			}
			page, ok := r.result()
			if !completed || !crossed || !ok || !r.cacheable() || page.Total != 2 || len(page.Values) != 2 || page.Values[0].Count != 2 {
				t.Fatalf("read exhaustion became result capacity: completed=%v crossed=%v cacheable=%v page=%+v", completed, crossed, r.cacheable(), page)
			}
		})
	}
}
