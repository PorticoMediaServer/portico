package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// browseRowCountStatements classifies the two whole-set count shapes an
// unfiltered browse can issue: the exact count of the eligible set, and the
// capped count a posted filter uses. An unrestricted Reading continuation
// must carry the exact total instead of running either.
func browseRowCountStatements(queries []string) (whole, capped int) {
	for _, query := range queries {
		query = strings.Join(strings.Fields(query), " ")
		switch {
		case strings.Contains(query, "SELECT count(*) FROM (SELECT 1 FROM catalog_browse_rows"):
			capped++
		case strings.Contains(query, "SELECT count(*) FROM catalog_browse_rows"):
			whole++
		}
	}
	return whole, capped
}

func readingBrowsePageIDs(entries []ContentEntry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func TestReadingBrowseContinuationReusesTheExactTotal(t *testing.T) {
	c, s, viewer, items := readingBrowseFixture(t, "book")
	c.Drain()
	want := make([]string, len(items))
	for i, item := range items {
		want[i] = item.Public
	}

	// Walk the whole unfiltered list twice: the first page of each walk counts
	// the eligible set exactly once, every continuation in between counts
	// nothing while still returning the same exact total and order.
	for _, limit := range []int{2, 1} {
		var ids []string
		cursor := ""
		pages := 0
		for {
			pages++
			if pages > len(items)+1 {
				t.Fatalf("limit %d: walk did not terminate", limit)
			}
			ctx, trace := dbwork.TraceStatements(context.Background())
			page, err := s.WithContext(ctx).BrowseEntities(viewer, BrowseRequest{Library: "reading", Limit: limit, Cursor: cursor})
			if err != nil {
				t.Fatalf("limit %d page %d: %v", limit, pages, err)
			}
			whole, capped := browseRowCountStatements(trace())
			if pages == 1 {
				if whole != 1 || capped != 0 {
					t.Fatalf("limit %d first page counts: whole=%d capped=%d", limit, whole, capped)
				}
			} else if whole != 0 || capped != 0 {
				t.Fatalf("limit %d page %d recounted: whole=%d capped=%d", limit, pages, whole, capped)
			}
			if page.PageInfo.Total != len(items) || page.PageInfo.TotalIsLowerBound {
				t.Fatalf("limit %d page %d total: %+v", limit, pages, page.PageInfo)
			}
			if page.Pivot != "books" {
				t.Fatalf("limit %d page %d pivot: %q", limit, pages, page.Pivot)
			}
			ids = append(ids, readingBrowsePageIDs(page.Entries)...)
			if !page.PageInfo.HasMore {
				if page.PageInfo.NextCursor != "" {
					t.Fatalf("limit %d last page published a cursor", limit)
				}
				break
			}
			if page.PageInfo.NextCursor == "" {
				t.Fatalf("limit %d page %d has more but no cursor", limit, pages)
			}
			cursor = page.PageInfo.NextCursor
		}
		if strings.Join(ids, ",") != strings.Join(want, ",") {
			t.Fatalf("limit %d order: %v want %v", limit, ids, want)
		}
	}

	// A legacy continuation with no carried total still owes the exact
	// unfiltered count: it must not fall back to the capped filter count.
	first, err := s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.readSignedBrowseCursor(first.PageInfo.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Total = 0
	legacy.TotalPresent = false
	legacy.Expires = time.Now().Add(time.Minute).Unix()
	raw, err := s.encodeCursor(legacy)
	if err != nil {
		t.Fatal(err)
	}
	ctx, trace := dbwork.TraceStatements(context.Background())
	page, err := s.WithContext(ctx).BrowseEntities(viewer, BrowseRequest{Library: "reading", Limit: 2, Cursor: raw})
	if err != nil {
		t.Fatal(err)
	}
	whole, capped := browseRowCountStatements(trace())
	if whole != 1 || capped != 0 {
		t.Fatalf("legacy total fallback counts: whole=%d capped=%d", whole, capped)
	}
	if page.PageInfo.Total != len(items) || page.PageInfo.TotalIsLowerBound {
		t.Fatalf("legacy total fallback: %+v", page.PageInfo)
	}
	if got := readingBrowsePageIDs(page.Entries); strings.Join(got, ",") != strings.Join(want[2:], ",") {
		t.Fatalf("legacy total fallback order: %v want %v", got, want[2:])
	}
}

func TestReadingBrowseContinuationRefusesBeforeCounting(t *testing.T) {
	c, s, viewer, items := readingBrowseFixture(t, "book")
	c.Drain()
	first, err := s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	valid, err := s.readSignedBrowseCursor(first.PageInfo.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	expired := valid
	expired.Expires = time.Now().Add(-time.Second).Unix()
	expiredRaw, err := s.encodeCursor(expired)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name    string
		cursor  string
		request BrowseRequest
		viewer  Viewer
		mutate  func()
	}{
		{name: "malformed", cursor: "not-a-cursor"},
		{name: "expired", cursor: expiredRaw},
		{name: "wrong viewer", cursor: first.PageInfo.NextCursor, viewer: Viewer{Profile: viewer.Profile, Fence: "other-viewer", Libraries: viewer.Libraries}},
		{name: "wrong sort", cursor: first.PageInfo.NextCursor, request: BrowseRequest{Library: "reading", Limit: 2, Sort: []BrowseSortSelection{{Field: "added"}}}},
		{name: "wrong limit", cursor: first.PageInfo.NextCursor, request: BrowseRequest{Library: "reading", Limit: 1}},
		{
			name:   "stale generation",
			cursor: first.PageInfo.NextCursor,
			mutate: func() {
				c.Exec(`UPDATE metadata_owner_fields SET value='Changed Author' WHERE entity_id=? AND field='author'`, items[0].ID)
				c.Drain()
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.mutate != nil {
				test.mutate()
			}
			request := test.request
			if request.Library == "" {
				request = BrowseRequest{Library: "reading", Limit: 2}
			}
			request.Cursor = test.cursor
			requestViewer := viewer
			if test.viewer.Fence != "" {
				requestViewer = test.viewer
			}
			ctx, trace := dbwork.TraceStatements(context.Background())
			_, err := s.WithContext(ctx).BrowseEntities(requestViewer, request)
			if !errors.Is(err, ErrStaleContinuation) && !errors.Is(err, ErrCursor) {
				t.Fatalf("refused cursor error: %v", err)
			}
			whole, capped := browseRowCountStatements(trace())
			if whole != 0 || capped != 0 {
				t.Fatalf("cursor refused after counting: whole=%d capped=%d", whole, capped)
			}
		})
	}
}

func TestReadingBrowseEmptyTotalIsExact(t *testing.T) {
	c, s, viewer, _ := readingBrowseFixture(t, "book")
	// No readable publication remains, so the ready projection is genuinely
	// empty rather than merely unbuilt.
	c.Exec(`DELETE FROM reading_publications`)
	c.Drain()
	ctx, trace := dbwork.TraceStatements(context.Background())
	page, err := s.WithContext(ctx).BrowseEntities(viewer, BrowseRequest{Library: "reading", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	whole, capped := browseRowCountStatements(trace())
	if whole != 1 || capped != 0 {
		t.Fatalf("empty total counts: whole=%d capped=%d", whole, capped)
	}
	if page.PageInfo.Total != 0 || page.PageInfo.TotalIsLowerBound || page.PageInfo.HasMore || len(page.Entries) != 0 || page.PageInfo.NextCursor != "" {
		t.Fatalf("empty page: %+v", page)
	}
}

func TestReadingBrowseRestrictedContinuationReusesWitness(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	age := 12
	v.Restrictions = identity.ContentRestrictions{MaximumAge: &age, MemberDeniedLabels: []string{"denied"}, Revision: 1}
	c.Drain()
	previous := browseTotalThreshold
	browseTotalThreshold = 1
	defer func() { browseTotalThreshold = previous }()
	for _, query := range []*BrowseNode{nil, {All: []BrowseNode{{Field: "author", Operator: "contains", Value: "café author"}}}} {
		first, err := s.BrowseEntities(v, BrowseRequest{Library: "reading", Limit: 1, Query: query})
		if err != nil {
			t.Fatal(err)
		}
		signed, err := s.readSignedBrowseCursor(first.PageInfo.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
		signed.Expires = time.Now().Add(time.Minute).Unix()
		raw, err := s.encodeCursor(signed)
		if err != nil {
			t.Fatal(err)
		}
		ctx, trace := dbwork.TraceStatements(context.Background())
		next, err := s.WithContext(ctx).BrowseEntities(v, BrowseRequest{Library: "reading", Limit: 1, Query: query, Cursor: raw})
		if err != nil {
			t.Fatal(err)
		}
		if a, b := browseRowCountStatements(trace()); a != 0 || b != 0 {
			t.Fatal("recount", a, b)
		}
		if first.PageInfo.Total != next.PageInfo.Total || first.PageInfo.TotalIsLowerBound != next.PageInfo.TotalIsLowerBound {
			t.Fatal(first.PageInfo, next.PageInfo)
		}
		if next.PageInfo.NextCursor != "" {
			carried, err := s.readSignedBrowseCursor(next.PageInfo.NextCursor)
			if err != nil || carried.Expires != signed.Expires || !carried.TotalPresent {
				t.Fatal(carried, err)
			}
		}
	}
}

func TestReadingBrowseWitnessMutationRefusesBeforeCounting(t *testing.T) {
	c, s, v, items := readingBrowseFixture(t, "book")
	age := 12
	v.Restrictions = identity.ContentRestrictions{MaximumAge: &age, Revision: 1}
	c.Exec(`INSERT INTO content_rating_ages VALUES('witness',0)`)
	c.Attributes(items[3].ID, "contentRating", "Witness")
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('epoch-account','epoch-owner',X'01','epoch-profile',1)`)
	c.Exec(`INSERT INTO profile_restrictions(profile_id) VALUES('epoch-profile')`)
	c.Exec(`INSERT INTO access_limits(account_id,updated_ms,body) VALUES('epoch-account',0,'{}')`)
	c.Drain()
	first, err := s.BrowseEntities(v, BrowseRequest{Library: "reading", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, query string }{
		{"profile policy", `UPDATE profile_restrictions SET revision=revision+1 WHERE profile_id='epoch-profile'`},
		{"member policy", `UPDATE direct_memberships SET revision=revision+1 WHERE account_id='epoch-account'`},
		{"member denial", `UPDATE access_limits SET body='{"deniedLabels":["x"]}' WHERE account_id='epoch-account'`},
		{"recording provenance", `INSERT INTO dvr_catalog_provenance SELECT l.entity_id,'recording',a.token,'source','channel','programme','guide','digest',0,1,'completed' FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? LIMIT 1`},
		{"classification age", `UPDATE content_rating_ages SET minimum_age=17 WHERE value_key='witness'`},
		{"classification key", `UPDATE content_rating_ages SET value_key='other-key' WHERE value_key='witness'`},
		{"term key", `UPDATE catalog_attribute_terms SET value_key='renamed' WHERE value_key='witness'`},
		{"term field", `UPDATE catalog_attribute_terms SET field_id=2 WHERE value_key='witness'`},
		{"edge spelling", `UPDATE catalog_item_attribute_edges SET source_value='denied' WHERE item_id=?`},
		{"edge delete", `DELETE FROM catalog_item_attribute_edges WHERE item_id=?`},
		{"membership", `INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,8)`},
		{"browse existence", `DELETE FROM catalog_browse_rows WHERE entity_id=?`},
		{"browse move", `UPDATE catalog_browse_rows SET library_id=(SELECT id FROM catalog_libraries WHERE library_id='other') WHERE entity_id=?`},
		{"entity lifetime", `UPDATE catalog_entities SET retired=1 WHERE id=?`},
		{"library lifetime", `UPDATE catalog_libraries SET retired=1 WHERE library_id='reading'`},
		{"serving", `UPDATE catalog_generations SET generation=generation+1`},
		{"retaining", `UPDATE catalog_generations SET retaining=1`},
		{"domain", `UPDATE catalog_derivations SET rebuilding=1 WHERE domain=18`},
		{"format", `UPDATE reading_publications SET format='mobi' WHERE item_id=?`},
		{"publication", `DELETE FROM reading_publications WHERE item_id=?`},
		{"ready", `UPDATE reading_attribution_state SET input_generation=input_generation+1,phase='scan' WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id='reading')`},
		{"rule", `UPDATE reading_attribution_state SET publication_rule_version=0 WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id='reading')`},
		{"retained row", `INSERT INTO catalog_browse_rows_previous(generation,present,entity_id) SELECT generation,0,? FROM catalog_generations`},
		{"retained membership", `INSERT INTO catalog_browse_memberships_previous(generation,present,entity_id,item_id,source) SELECT generation,0,?,?,8 FROM catalog_generations`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := c.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			args := make([]any, strings.Count(tt.query, "?"))
			for i := range args {
				args[i] = items[3].ID
			}
			if _, err := tx.Exec(tt.query, args...); err != nil {
				t.Fatal(err)
			}
			ctx, trace := dbwork.TraceStatements(context.Background())
			clone := *s.WithContext(ctx)
			clone.readTx = tx
			_, _, err = clone.browseSelect(BrowseRequest{Library: "reading", Profile: v.Profile, ViewerFence: v.Fence, Restrictions: v.Restrictions, Limit: 1, Cursor: first.PageInfo.NextCursor})
			if !errors.Is(err, ErrStaleContinuation) {
				t.Fatal("changed witness not refused as stale", err)
			}
			if a, b := browseRowCountStatements(trace()); a != 0 || b != 0 {
				t.Fatal("refused after count", a, b, err)
			}
		})
	}
}

func TestReadingBrowseWitnessPolicyAndAnchorRefusals(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	first, err := s.BrowseEntities(v, BrowseRequest{Library: "reading", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []identity.ContentRestrictions{{Revision: 2}, {MemberRevision: 2}, {MemberDeniedLabels: []string{"x"}}, {BlockedLabels: []string{"a,b"}}, {BlockedLabels: []string{"a", "b"}}, {BlockRecordings: true}} {
		changed := v
		changed.Restrictions = r
		ctx, trace := dbwork.TraceStatements(context.Background())
		_, err := s.WithContext(ctx).BrowseEntities(changed, BrowseRequest{Library: "reading", Limit: 1, Cursor: first.PageInfo.NextCursor})
		if err == nil {
			t.Fatal("policy accepted", r)
		}
		if a, b := browseRowCountStatements(trace()); a != 0 || b != 0 {
			t.Fatal(a, b)
		}
	}
	signed, err := s.readSignedBrowseCursor(first.PageInfo.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*cursorValue){func(c *cursorValue) { c.ID = "999999999" }, func(c *cursorValue) { c.Keys = []string{"bad"} }, func(c *cursorValue) { c.Value = "-1" }} {
		cursor := signed
		mutate(&cursor)
		raw, err := s.encodeCursor(cursor)
		if err != nil {
			t.Fatal(err)
		}
		ctx, trace := dbwork.TraceStatements(context.Background())
		_, err = s.WithContext(ctx).BrowseEntities(v, BrowseRequest{Library: "reading", Limit: 1, Cursor: raw})
		if err == nil {
			t.Fatal("bad anchor accepted")
		}
		if a, b := browseRowCountStatements(trace()); a != 0 || b != 0 {
			t.Fatal(a, b)
		}
	}
}

func TestReadingBrowseCarriedZeroDistinctFromLegacyAbsent(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	first, err := s.BrowseEntities(v, BrowseRequest{Library: "reading", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := s.readSignedBrowseCursor(first.PageInfo.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	// A trusted synthetic zero with a valid anchor isolates presence semantics;
	// a naturally empty page has no continuation.
	signed.Total = 0
	signed.TotalPresent = true
	raw, err := s.encodeCursor(signed)
	if err != nil {
		t.Fatal(err)
	}
	ctx, trace := dbwork.TraceStatements(context.Background())
	page, err := s.WithContext(ctx).BrowseEntities(v, BrowseRequest{Library: "reading", Limit: 1, Cursor: raw})
	if err != nil {
		t.Fatal(err)
	}
	if a, b := browseRowCountStatements(trace()); a != 0 || b != 0 || page.PageInfo.Total != 0 {
		t.Fatal(a, b, page.PageInfo)
	}
}
