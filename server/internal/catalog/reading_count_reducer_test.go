package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

func finishReadingCount(t *testing.T, s *Service, v Viewer, r *ReadingCountReduction, b readingCountBudget) (ReadingCountQuantum, int, int64) {
	t.Helper()
	raw := 0
	var bytes int64
	for i := 0; i < 10000; i++ {
		q, err := s.stepReadingCountReduction(v, r, b)
		if err != nil {
			t.Fatal(err)
		}
		if q.Raw > b.raw || q.Bytes > b.bytes && !q.Oversized {
			t.Fatal("quantum exceeded budget", q, b)
		}
		if !q.Done && (q.Total != 0 || q.TotalIsLowerBound) {
			t.Fatal("partial count exposed", q)
		}
		raw += q.Raw
		bytes += q.Bytes
		if q.Done {
			return q, raw, bytes
		}
	}
	t.Fatal("count failed to terminate")
	return ReadingCountQuantum{}, 0, 0
}

func TestReadingCountReducerMatchesCurrentOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	previous := browseTotalThreshold
	browseTotalThreshold = 2
	defer func() { browseTotalThreshold = previous }()
	for _, kind := range []string{"book", "comic"} {
		t.Run(kind, func(t *testing.T) {
			c, s, v, items := readingBrowseFixture(t, kind)
			c.Exec(`INSERT INTO content_rating_ages VALUES('reducer-age',17)`)
			c.Attributes(items[0].ID, "contentRating", "Reducer-Age")
			c.Attributes(items[1].ID, "label", "Denied")
			c.Attributes(items[2].ID, "tag", "Member")
			c.Drain()
			// Restriction membership must exercise the actual live predicate for these
			// non-playable Reading leaves, including its empty-container convention.
			for _, item := range items {
				c.Exec(`INSERT INTO catalog_browse_memberships VALUES(?,?,8) ON CONFLICT DO NOTHING`, item.ID, item.ID)
			}
			age := 12
			policies := []identity.ContentRestrictions{{}, {MaximumAge: &age}, {BlockUnrated: true}, {BlockedLabels: []string{"DENIED"}}, {MemberDeniedLabels: []string{"Member"}}, {MaximumAge: &age, BlockUnrated: true, MemberDeniedLabels: []string{"Member"}}}
			queries := []*BrowseNode{nil,
				{Field: "title", Operator: "contains", Value: "Title"},
				{Field: "title", Operator: "not-equals", Value: "Title 1"},
				{Field: "year", Operator: "is-missing"},
				{Field: "dateAdded", Operator: "is-present"},
				{Field: "dateAdded", Operator: "within-last-days", Value: 36500},
				{Field: "author", Operator: "contains", Value: "CAFÉ  AUTHOR"},
				{Field: "author", Operator: "not-equals", Value: "café author"},
				{Field: "author", Operator: "is-missing"},
				{Field: "series", Operator: "contains-all", Value: []any{"Real Series", "other"}},
				{Any: []BrowseNode{{Field: "author", Operator: "equals", Value: "Cafe Author"}, {Not: &BrowseNode{Field: "series", Operator: "is-present"}}}},
			}
			for pi, policy := range policies {
				for qi, query := range queries {
					viewer := v
					viewer.Restrictions = policy
					request := BrowseRequest{Library: "reading", Limit: 1, Query: query}
					oracle, err := s.BrowseEntities(viewer, request)
					if err != nil {
						t.Fatalf("%d/%d oracle %v", pi, qi, err)
					}
					ctx, trace := dbwork.TraceStatements(context.Background())
					reduced, err := s.WithContext(ctx).NewReadingCountReduction(viewer, request)
					if err != nil {
						t.Fatalf("%d/%d admission %v", pi, qi, err)
					}
					if reduced.Witness() == "" || reduced.MemoryBytes() > ReadingCountMaximumBytes {
						t.Fatal("unaccounted state")
					}
					got, _, _ := finishReadingCount(t, s.WithContext(ctx), viewer, reduced, readingCountBudget{32, ReadingCountMaximumBytes, ReadingCountYieldAfter})
					if got.Total != oracle.PageInfo.Total || got.TotalIsLowerBound != oracle.PageInfo.TotalIsLowerBound {
						t.Fatalf("%d/%d got %+v oracle %+v", pi, qi, got, oracle.PageInfo)
					}
					for _, statement := range trace() {
						upper := strings.ToUpper(strings.TrimSpace(statement))
						if strings.Contains(upper, "COUNT(*)") || strings.HasPrefix(upper, "INSERT") || strings.HasPrefix(upper, "UPDATE") || strings.HasPrefix(upper, "DELETE") {
							t.Fatal("reducer issued count or write", statement)
						}
					}
				}
			}
		})
	}
}

func TestReadingCountReducerChargesRejectedRawAndNestedAttributes(t *testing.T) {
	c, s, v, items := readingBrowseFixture(t, "book")
	var other int64
	if err := c.DB.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id='other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		c.Entity(compactcatalog.Entity{Library: other, Kind: compactcatalog.Ebook, Key: fmt.Sprint("other-", i), Title: "other"}, nil)
	}
	c.Drain()
	c.Exec(`INSERT INTO catalog_browse_memberships VALUES(?,?,8) ON CONFLICT DO NOTHING`, items[0].ID, items[0].ID)
	for i := 0; i < 40; i++ {
		key := fmt.Sprintf("count-attribute-%03d", i)
		c.Exec(`INSERT INTO catalog_attribute_terms(field_id,value_key) VALUES(3,?)`, key)
		c.Exec(`INSERT INTO catalog_item_attribute_edges(item_id,term_id,source_value,source_rowid) SELECT ?,id,?,? FROM catalog_attribute_terms WHERE field_id=3 AND value_key=?`, items[0].ID, key, i, key)
	}
	age := 12
	v.Restrictions = identity.ContentRestrictions{MaximumAge: &age}
	r, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	q, raw, _ := finishReadingCount(t, s, v, r, readingCountBudget{12, ReadingCountMaximumBytes, ReadingCountYieldAfter})
	if q.Total != 4 || q.TotalIsLowerBound || raw < 40+40*3 {
		t.Fatal("raw history was skipped", q, raw)
	}
	// An entirely rejected query must still charge the raw catalogue, rather
	// than hiding the rejection under a filtered LIMIT.
	r, err = s.NewReadingCountReduction(v, BrowseRequest{Library: "reading", Query: &BrowseNode{Field: "title", Operator: "equals", Value: "no-match"}})
	if err != nil {
		t.Fatal(err)
	}
	q, raw, _ = finishReadingCount(t, s, v, r, readingCountBudget{12, ReadingCountMaximumBytes, ReadingCountYieldAfter})
	if q.Total != 0 || raw < 44 {
		t.Fatal("rejected raw rows were uncharged", q, raw)
	}
}

func TestReadingCountReducerChargesPublicationHistory(t *testing.T) {
	c, s, v, items := readingBrowseFixture(t, "book")
	c.Exec(`UPDATE reading_publications SET format='mobi' WHERE item_id=?`, items[0].ID)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < 40; i++ {
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: fmt.Sprintf("/count/history-%02d", i), Size: 100, ModifiedNS: 1, Container: "mobi"})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, items[0].ID, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO reading_publications VALUES(?,?,'mobi')`, items[0].ID, asset); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	r, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	q, raw, _ := finishReadingCount(t, s, v, r, readingCountBudget{12, ReadingCountMaximumBytes, ReadingCountYieldAfter})
	if q.Total != 3 || raw < 41 {
		t.Fatal("unsupported versions not charged", q, raw)
	}
}

func TestReadingCountReducerDriftCancellationAndAtomicProgress(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	r, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.stepReadingCountReduction(v, r, readingCountBudget{8, ReadingCountMaximumBytes, ReadingCountYieldAfter})
	if err != nil {
		t.Fatal(err)
	}
	before := *r
	c.Exec(`INSERT INTO content_rating_ages VALUES('count-drift',0)`)
	q, err := s.StepReadingCountReduction(v, r)
	if !errors.Is(err, ErrStaleContinuation) || q.Done || r.phase != before.phase || r.afterEntity != before.afterEntity || r.count != before.count {
		t.Fatal("drift saved partial work", q, err)
	}
	fresh, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.WithContext(ctx).StepReadingCountReduction(v, fresh); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	if fresh.afterEntity != 0 || fresh.count != 0 {
		t.Fatal("cancel saved progress")
	}
	changed := v
	changed.Restrictions = identity.ContentRestrictions{BlockRecordings: true}
	if _, err = s.StepReadingCountReduction(changed, fresh); !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("policy drift ignored", err)
	}
	denied := v
	denied.Libraries = nil
	if _, err = s.StepReadingCountReduction(denied, fresh); !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("authority ignored", err)
	}
	// Cooperative time yielding may return no work, but never performs a scan
	// after the time budget expired and never exposes a partial result.
	q, err = s.stepReadingCountReduction(v, fresh, readingCountBudget{4096, ReadingCountMaximumBytes, time.Nanosecond})
	if err != nil || q.Raw != 0 || q.Done {
		t.Fatal(q, err)
	}
}

func TestReadingCountReducerByteBudgetAndLosslessOversize(t *testing.T) {
	c, s, v, items := readingBrowseFixture(t, "book")
	c.Drain()
	text := strings.Repeat("x", 50000)
	c.Exec(`UPDATE catalog_browse_rows SET title=?`, text)
	request := BrowseRequest{Library: "reading", Query: &BrowseNode{Field: "title", Operator: "contains", Value: "x"}}
	r, err := s.NewReadingCountReduction(v, request)
	if err != nil {
		t.Fatal(err)
	}
	q, raw, bytes := finishReadingCount(t, s, v, r, defaultReadingCountBudget)
	if q.Total != 4 || raw == 0 || bytes < 4*50000 {
		t.Fatal(q, raw, bytes)
	}
	// More than the ordinary 4 MiB work budget is still one legal exact
	// scalar. Probe its size, evaluate once, yield, and retain no payload.
	huge := strings.Repeat("x", ReadingCountMaximumBytes+17)
	c.Exec(`UPDATE catalog_browse_rows SET title=? WHERE entity_id=?`, huge, items[0].ID)
	r, err = s.NewReadingCountReduction(v, request)
	if err != nil {
		t.Fatal(err)
	}
	reserved := r.MemoryBytes()
	oversized := 0
	for i := 0; i < 100; i++ {
		q, err = s.StepReadingCountReduction(v, r)
		if err != nil {
			t.Fatal(err)
		}
		if q.Oversized {
			oversized++
			if q.Bytes < int64(len(huge)) || q.Raw > r.queryUnits+3 {
				t.Fatal("oversize was not one unit", q)
			}
		}
		if r.MemoryBytes() != reserved {
			t.Fatal("oversized payload retained")
		}
		if q.Done {
			if q.Total != 4 {
				t.Fatal(q)
			}
			break
		}
		if i == 99 {
			t.Fatal("oversized count did not complete")
		}
	}
	if oversized != 1 {
		t.Fatal("oversized scalar was truncated, retried or skipped", oversized)
	}
}

func TestReadingCountReducerRetainedRowsAndMembershipsMatchOracle(t *testing.T) {
	c, s, v, items := readingBrowseFixture(t, "book")
	c.Attributes(items[0].ID, "label", "blocked")
	c.Drain()
	c.Exec(`INSERT INTO catalog_browse_memberships VALUES(?,?,8) ON CONFLICT DO NOTHING`, items[0].ID, items[0].ID)
	c.Exec(`UPDATE catalog_generations SET retaining=1`)
	// Deleting the blocked item's current membership retains the old membership;
	// inserting a blocked member into a formerly empty title records absence.
	c.Exec(`DELETE FROM catalog_browse_memberships WHERE entity_id=?`, items[0].ID)
	c.Exec(`INSERT INTO catalog_browse_memberships VALUES(?,?,8)`, items[1].ID, items[0].ID)
	c.Exec(`UPDATE catalog_browse_rows SET title='replacement' WHERE entity_id=?`, items[2].ID)
	v.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"blocked"}}
	request := BrowseRequest{Library: "reading", Query: &BrowseNode{Field: "title", Operator: "contains", Value: "Title"}}
	oracle, err := s.BrowseEntities(v, request)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.NewReadingCountReduction(v, request)
	if err != nil {
		t.Fatal(err)
	}
	q, _, _ := finishReadingCount(t, s, v, r, readingCountBudget{12, ReadingCountMaximumBytes, ReadingCountYieldAfter})
	if q.Total != 3 || q.Total != oracle.PageInfo.Total || q.TotalIsLowerBound != oracle.PageInfo.TotalIsLowerBound {
		t.Fatal(q, oracle.PageInfo)
	}
}

func TestReadingCountReducerPreservesPublishedClassPrefilter(t *testing.T) {
	c, s, v, items := readingBrowseFixture(t, "book")
	c.Drain()
	age := 12
	v.Restrictions = identity.ContentRestrictions{MaximumAge: &age}
	key, policy := visibilityClassKey("reading", v.Restrictions)
	c.Exec(`INSERT INTO compact_visibility_classes(class_key,library_id,policy_json,active_generation,catalog_revision,requested_ms,built_ms) SELECT ?,id,?,1,1,0,0 FROM catalog_libraries WHERE library_id='reading'`, key, policy)
	c.Exec(`INSERT INTO compact_visibility_rows(class_id,generation,entity_id,library_id,kind,sort_key,head,decade) SELECT cls.id,1,r.entity_id,r.library_id,r.kind,r.sort_key,r.head,0 FROM compact_visibility_classes cls CROSS JOIN catalog_browse_rows r WHERE cls.class_key=? AND r.entity_id=?`, key, items[1].ID)
	oracle, err := s.BrowseEntities(v, BrowseRequest{Library: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	q, _, _ := finishReadingCount(t, s, v, r, defaultReadingCountBudget)
	if q.Total != 1 || q.Total != oracle.PageInfo.Total {
		t.Fatal(q, oracle.PageInfo)
	}
}

func TestReadingCountReducerPointAndRawPlans(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	r, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading", Query: &BrowseNode{Field: "author", Operator: "equals", Value: "café author"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range []struct {
		query string
		args  []any
	}{
		{`SELECT id,library_id,kind,retired FROM catalog_entities WHERE id>? ORDER BY id LIMIT 1`, []any{0}},
		{`SELECT item_id,source FROM catalog_browse_memberships WHERE entity_id=? AND (item_id,source)>(?,?) ORDER BY item_id,source LIMIT 1`, []any{1, 0, -1}},
		{`SELECT item_id,source,present FROM catalog_browse_memberships_previous WHERE generation=? AND entity_id=? AND (item_id,source)>(?,?) ORDER BY item_id,source LIMIT 1`, []any{1, 1, 0, -1}},
		{`SELECT term_id FROM catalog_item_attribute_edges WHERE item_id=? AND term_id>? ORDER BY term_id LIMIT 1`, []any{1, 0}},
		{`SELECT rowid FROM inventory_objects WHERE rowid>? ORDER BY rowid LIMIT 1`, []any{0}},
		{`SELECT COALESCE((` + r.predicate + `),0) FROM catalog_browse_rows e WHERE e.entity_id=?`, append(append([]any{}, r.predicateArgs...), 1)},
	} {
		rows, err := c.DB.Query(`EXPLAIN QUERY PLAN `+plan.query, plan.args...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id, parent, aux int
			var detail string
			if err = rows.Scan(&id, &parent, &aux, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN ") || strings.Contains(detail, "TEMP B-TREE") {
				t.Fatal("unbounded raw or nested plan", detail, plan.query)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	if !strings.Contains(r.predicate, "m.item_id=e.entity_id") || strings.Contains(r.predicate, " IN(SELECT m.item_id") {
		t.Fatal("attribution set was materialized", r.predicate)
	}
}

func TestReadingCountReducerLocalArchiveHistory(t *testing.T) {
	if !readingcap.CB7() {
		t.Skip("CB7 is dark in this build")
	}
	c, s, v, items := readingBrowseFixture(t, "comic")

	c.Exec(`UPDATE reading_publications SET format='cb7' WHERE item_id=?`, items[0].ID)
	for i := 0; i < 40; i++ {
		delta := 1
		if i == 39 {
			delta = 0
		}
		c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT ?,source.id,a.token,source.incarnation,?,'r','{}',a.size+?,a.modified_ns FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id JOIN library_sources source ON source.library_id='reading' AND source.kind='local' WHERE link.entity_id=? LIMIT 1`, fmt.Sprint("count-object-", i), fmt.Sprint("count-path-", i), delta, items[0].ID)
	}
	c.Drain()
	oracle, err := s.BrowseEntities(v, BrowseRequest{Library: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	q, raw, _ := finishReadingCount(t, s, v, r, readingCountBudget{12, ReadingCountMaximumBytes, ReadingCountYieldAfter})
	if q.Total != 4 || q.Total != oracle.PageInfo.Total || raw < 40*3 {
		t.Fatal("local archive history skipped", q, raw, oracle.PageInfo)
	}
}

func TestReadingCountPreparedPageDoesNotRecountAndRejectsDrift(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	for _, query := range []*BrowseNode{nil, {Field: "author", Operator: "equals", Value: "Café Author"}} {
		request := BrowseRequest{Library: "reading", Limit: 1, Query: query}
		r, err := s.NewReadingCountReduction(v, request)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := r.Result(); ok {
			t.Fatal("unfinished result exposed")
		}
		count, _, _ := finishReadingCount(t, s, v, r, defaultReadingCountBudget)
		result, ok := r.Result()
		if !ok {
			t.Fatal("complete result missing")
		}
		ctx, trace := dbwork.TraceStatements(context.Background())
		page, err := s.WithContext(ctx).BrowseReadingWithPreparedCount(v, request, result)
		if err != nil {
			t.Fatal(err)
		}
		if a, b := browseRowCountStatements(trace()); a != 0 || b != 0 {
			t.Fatal("prepared page recounted", a, b)
		}
		if page.PageInfo.Total != count.Total || page.PageInfo.TotalIsLowerBound != count.TotalIsLowerBound || len(page.Entries) != 1 {
			t.Fatal(page.PageInfo, count)
		}
		bad := request
		bad.Query = &BrowseNode{Field: "title", Operator: "equals", Value: "other"}
		if _, err = s.BrowseReadingWithPreparedCount(v, bad, result); !errors.Is(err, ErrStaleContinuation) {
			t.Fatal("different query accepted", err)
		}
		if page.PageInfo.NextCursor != "" {
			request.Cursor = page.PageInfo.NextCursor
			_, err = s.WithContext(ctx).BrowseEntities(v, request)
			if err != nil {
				t.Fatal("prepared cursor", err)
			}
		}
	}
	r, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	finishReadingCount(t, s, v, r, defaultReadingCountBudget)
	result, _ := r.Result()
	c.Exec(`INSERT INTO content_rating_ages VALUES('prepared-drift',0)`)
	ctx, trace := dbwork.TraceStatements(context.Background())
	_, err = s.WithContext(ctx).BrowseReadingWithPreparedCount(v, BrowseRequest{Library: "reading"}, result)
	if !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("prepared drift accepted", err)
	}
	if a, b := browseRowCountStatements(trace()); a != 0 || b != 0 {
		t.Fatal("stale prepared page counted", a, b)
	}
}

func TestReadingCountReducerAdmitsScalarMemoryBeforeWork(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	r, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading", Query: &BrowseNode{Field: "title", Operator: "contains", Value: "Title"}})
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("reservation denied")
	calls := 0
	reserve := func(peak int64, oversized bool) error {
		calls++
		if peak <= r.MemoryBytes() {
			t.Fatal("missing transient reservation")
		}
		return denied
	}
	q, err := s.StepReadingCountReduction(v, r, reserve)
	if !errors.Is(err, denied) || q.Done || r.afterEntity != 0 || calls != 1 {
		t.Fatal("work committed despite failed reservation", q, err, calls)
	}
}

func TestReadingCountReducerVacuumInvalidatesPhysicalCursor(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	r, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.stepReadingCountReduction(v, r, readingCountBudget{8, ReadingCountMaximumBytes, ReadingCountYieldAfter})
	if err != nil {
		t.Fatal(err)
	}
	before := r.afterEntity
	c.Exec(`VACUUM`)
	if _, err = s.StepReadingCountReduction(v, r); !errors.Is(err, ErrStaleContinuation) || r.afterEntity != before {
		t.Fatal("physical cursor survived vacuum", err)
	}
}

func TestReadingCountContinuationAdmissionReusesOnlyValidatedTotals(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	first, err := s.BrowseEntities(v, BrowseRequest{Library: "reading", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.readSignedBrowseCursor(first.PageInfo.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	original.Expires = time.Now().Add(time.Minute).Unix()
	for _, tt := range []struct {
		name    string
		mutate  func(*cursorValue)
		present bool
		refuse  bool
	}{
		{"carried", func(*cursorValue) {}, true, false},
		{"zero present", func(c *cursorValue) { c.Total = 0; c.TotalPresent = true }, true, false},
		{"legacy absent", func(c *cursorValue) { c.Total = 0; c.TotalPresent = false }, false, false},
		{"wrong envelope", func(c *cursorValue) { c.CatalogRevision-- }, false, false},
		{"missing anchor", func(c *cursorValue) { c.ID = "999999999" }, false, true},
		{"wrong fingerprint", func(c *cursorValue) { c.Fingerprint = "other" }, false, true},
		{"expired", func(c *cursorValue) { c.Expires = time.Now().Add(-time.Second).Unix() }, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cursor := original
			tt.mutate(&cursor)
			// Preserve the deliberately stale envelope when signing this test case.
			raw, err := s.encodeRevisionCursor(cursor, ContentRevision{Catalog: cursor.CatalogRevision, Viewer: cursor.ViewerRevision})
			if err != nil {
				t.Fatal(err)
			}
			request := BrowseRequest{Library: "reading", Limit: 1, Cursor: raw}
			ctx, trace := dbwork.TraceStatements(context.Background())
			_, present, err := s.WithContext(ctx).BrowseReadingCarriedTotal(v, request)
			if tt.refuse {
				if !errors.Is(err, ErrStaleContinuation) {
					t.Fatal(err)
				}
			} else if err != nil || present != tt.present {
				t.Fatal(present, err)
			}
			if a, b := browseRowCountStatements(trace()); a != 0 || b != 0 {
				t.Fatal("admission counted", a, b)
			}
			if tt.refuse {
				return
			}
			r, err := s.NewReadingCountReduction(v, request)
			if err != nil {
				t.Fatal(err)
			}
			if r.done != tt.present || r.OriginalExpiry().Unix() != original.Expires {
				t.Fatal("count presence or original expiry lost", r.done, r.OriginalExpiry())
			}
			if !tt.present {
				finishReadingCount(t, s, v, r, defaultReadingCountBudget)
				result, _ := r.Result()
				_, err = s.WithContext(ctx).BrowseReadingWithPreparedCount(v, request, result)
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
