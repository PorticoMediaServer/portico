package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"testing"
	"time"
)

func TestBulkMetadataPreviewUsesActualSnapshotsAndCASDigests(t *testing.T) {
	service, targets, _ := bulkFixture(t)
	ctx := context.Background()
	request := catalog.BulkMetadataPreviewRequest{}
	for _, target := range targets {
		request.Targets = append(request.Targets, catalog.BulkMetadataTarget{Kind: target.Kind, ContentKind: "movie", ID: target.ID, LibraryID: "movies"})
	}
	preview, err := service.BulkPreview(ctx, previewRequestSource(service, request), previewAuthorize)

	if err != nil {
		t.Fatal(err)
	}
	if preview.Total != 3 || len(preview.Targets) != 3 {
		t.Fatal(preview)
	}
	for index, target := range targets {
		if preview.Targets[index].ID != target.ID || preview.Targets[index].ExpectedRevision != target.ExpectedRevision {
			t.Fatalf("CAS digest/order changed: %+v %+v", preview.Targets[index], target)
		}
	}
	year := preview.Fields["year"]
	if year.State != "mixed" || year.Value != nil || year.DistinctCount != 3 {
		t.Fatalf("Mixed fabricated a blank: %+v", year)
	}
	if _, ok := preview.Fields["title"]; ok {
		t.Fatal("non-bulk field offered")
	}
	foundTitle := false
	for _, field := range preview.ExcludedFields {
		if field.Field == "title" && field.Label == "Title" && !field.Bulk {
			foundTitle = true
		}
	}
	if !foundTitle || len(preview.SingleItemFields) != len(repairSchema("item", "movie")) {
		t.Fatal("single-item exclusions were guessed or lost", preview.ExcludedFields)
	}
	if preview.Fields["tags"].Entries[0].Value != "Keep" || preview.Fields["tags"].Entries[0].Count != 1 {
		t.Fatal(preview.Fields["tags"])
	}
}
func TestBulkMetadataPreviewQueryReportsMoreThan64DistinctValuesWithoutEnumeratingTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	service, db := editorFixture(t)
	fixture := catalogtest.New(t, db)

	for index := 0; index < 70; index++ {
		fixture.Movie(fixture.Handle("movies"), fmt.Sprintf("/movies/preview-%d.mkv", index), fmt.Sprintf("Preview %d", index), 1900+index)
	}
	fixture.Drain()
	node := &catalog.BrowseNode{Field: "title", Operator: "starts-with", Value: "Preview"}
	sorts := []catalog.BrowseSortSelection{{Field: "title", Direction: "asc"}}
	viewer := catalog.Viewer{Profile: "profile", Libraries: []string{"movies"}}
	browse, err := catalog.New(db).BrowseEntities(viewer, catalog.BrowseRequest{Library: "movies", Pivot: "movies", Query: node, Sort: sorts, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	request := catalog.BulkMetadataPreviewRequest{Selector: &catalog.JobSelector{Query: &catalog.JobQuery{LibraryID: "movies", Pivot: "movies", Filter: raw, Sort: sorts}}, Expected: &catalog.JobExpected{CatalogRevision: browse.PageInfo.Revision}}
	preview, err := service.BulkPreview(context.Background(), previewRequestSource(service, request), previewAuthorize)

	if err != nil {
		t.Fatal(err)
	}
	year := preview.Fields["year"]
	if preview.Total != 70 || len(preview.Targets) != 0 || year.DistinctCount != 0 || year.AtLeast != 65 || len(year.Values) != 0 || !year.Truncated || year.State != "many" {
		t.Fatalf("untruthful bounded aggregate: total=%d targets=%d field=%+v", preview.Total, len(preview.Targets), year)
	}
}
func TestBulkMetadataPreviewRechecksAuthorityInsideSnapshot(t *testing.T) {
	service, targets, _ := bulkFixture(t)
	raw, _ := json.Marshal([]string{targets[0].ID})
	selection := catalog.BulkMetadataSelection{SQL: `SELECT id FROM catalog_entities WHERE public_id IN(SELECT pid_blob(value) FROM json_each(?))`, Args: []any{string(raw)}}
	denied := errors.New("revoked")
	calls := 0
	_, err := service.BulkPreview(context.Background(), previewSQLSource(service, selection), func(context.Context, *sql.Tx) error {
		calls++
		if calls > 1 {
			return denied
		}
		return nil
	})
	if !errors.Is(err, denied) || calls != 2 {
		t.Fatalf("authority not rechecked: %d %v", calls, err)
	}
}

func TestBulkMetadataPreviewWholeView48000(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	service, db := editorFixture(t)
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, err := tx.Exec(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<48000) INSERT INTO catalog_entities(public_id,library_id,kind,title,sort_key,sort_head,year) SELECT randomblob(16),(SELECT id FROM catalog_libraries WHERE library_id='movies'),1,'Whole view '||x,'whole view '||x,'W',2020+(x%2) FROM n`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO catalog_item_details(entity_id) SELECT id FROM catalog_entities WHERE title LIKE 'Whole view %'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	preview, err := service.BulkPreview(ctx, previewSQLSource(service, catalog.BulkMetadataSelection{SQL: `SELECT id FROM catalog_entities WHERE title LIKE 'Whole view %'`, Revision: "whole-view-48000"}), previewAuthorize)

	if err != nil {
		t.Fatal(err)
	}
	if preview.Total != 48000 || len(preview.Targets) != 0 || preview.SelectionRevision != "whole-view-48000" {
		t.Fatalf("whole-view selection was enumerated/capped %+v", preview)
	}
	year := preview.Fields["year"]
	if year.State != "mixed" || year.DistinctCount != 2 || len(year.Values) != 2 || year.Values[0].Count != 24000 || year.Values[1].Count != 24000 {
		t.Fatalf("inexact whole-view aggregate %+v", year)
	}
	if len(preview.Tabs) == 0 {
		t.Fatal("bulk schema tabs absent")
	}
}

func previewAuthorize(context.Context, *sql.Tx) error { return nil }
func previewRequestSource(s *Service, in catalog.BulkMetadataPreviewRequest) BulkPreviewSource {
	c := catalog.New(s.db)
	return func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
		return c.BulkMetadataSelectionPageTx(ctx, tx, catalog.Viewer{Profile: "profile", Libraries: []string{"movies"}}, in, after)
	}
}

// Synthetic selections isolate aggregation from browse publication for tests
// inserting unpublished rows directly. Real source paging is tested in catalog.
func previewSQLSource(s *Service, selection catalog.BulkMetadataSelection) BulkPreviewSource {
	return func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
		out := catalog.BulkMetadataSelectionPage{After: after, Selection: selection}
		var revision string
		if err := tx.QueryRowContext(ctx, `SELECT printf('%d:%d',revision,metadata_revision) FROM library_revisions WHERE library_id='movies'`).Scan(&revision); err != nil {
			return out, err
		}
		out.Fence = fmt.Sprint(revision)
		rows, err := tx.QueryContext(ctx, `SELECT id FROM catalog_entities WHERE id>? ORDER BY id LIMIT ?`, after, catalog.BulkMetadataPageSize)
		if err != nil {
			return out, err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
			out.After = id
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return out, err
		}
		raw, err := json.Marshal(ids)
		if err != nil {
			return out, err
		}
		out.Selection.SQL = `SELECT id FROM (` + selection.SQL + `) WHERE id IN(SELECT value FROM json_each(?))`
		out.Selection.Args = append(append([]any{}, selection.Args...), string(raw))
		out.Done = len(ids) < catalog.BulkMetadataPageSize
		return out, nil
	}
}
func assertPreviewScratchClean(t *testing.T, s *Service) {
	t.Helper()
	entries, err := os.ReadDir(s.cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "bulk-preview-") {
			t.Fatalf("preview spill survived: %s", entry.Name())
		}
	}
}

func TestBulkMetadataPreviewPagesPreserveExactCountsAndListEntries(t *testing.T) {
	s, db := editorFixture(t)
	// Two full pages and a tail. Equal values recur after the 64-value display
	// boundary, and duplicate tags must still count once per entity.
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, e := tx.Exec(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<131) INSERT INTO catalog_entities(public_id,library_id,kind,title,sort_key,sort_head,year) SELECT randomblob(16),(SELECT id FROM catalog_libraries WHERE library_id='movies'),1,'Paged '||x,'paged '||x,'P',1900+(x%60) FROM n`)
		if e != nil {
			return e
		}
		_, e = tx.Exec(`INSERT INTO catalog_item_details(entity_id,tags) SELECT id,json_array('Shared','Tag '||year,'Shared') FROM catalog_entities WHERE title LIKE 'Paged %'`)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	selection := catalog.BulkMetadataSelection{SQL: `SELECT id FROM catalog_entities WHERE title LIKE 'Paged %'`, Revision: "paged"}
	p, err := s.BulkPreview(context.Background(), previewSQLSource(s, selection), previewAuthorize)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 131 || p.Fields["year"].DistinctCount != 60 || p.Fields["year"].Truncated || p.Fields["year"].State != "mixed" {
		t.Fatal(p.Total, p.Fields["year"])
	}
	for _, v := range p.Fields["year"].Values {
		var want int64
		if err = db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE title LIKE 'Paged %' AND CAST(year AS TEXT)=?`, v.Value).Scan(&want); err != nil || want != v.Count {
			t.Fatalf("inexact retained count %+v want %d: %v", v, want, err)
		}
	}
	tags := p.Fields["tags"]
	if tags.DistinctCount != 60 || tags.Truncated {
		t.Fatal(tags)
	}
	found := false
	for _, entry := range tags.Entries {
		if entry.Value == "Shared" {
			found = true
			if entry.Count != 131 {
				t.Fatal(entry)
			}
		}
	}
	if !found {
		t.Fatal(tags)
	}
	if shared := p.Fields["studio"]; shared.State != "shared" || shared.Value == nil || *shared.Value != "" || shared.Values[0].Count != 131 {
		t.Fatal(shared)
	}
	assertPreviewScratchClean(t, s)
}

func TestBulkMetadataPreviewFencesInterPageAndFinalMutation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct{ name, sql string }{
		{"field", `UPDATE catalog_entities SET year=year+1 WHERE title='Before'`},
		{"delete", `DELETE FROM catalog_entities WHERE title='Before'`},
		{"lock", `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) SELECT 'item',id,'year','2020','2020',1,'owner','now' FROM catalog_entities WHERE title='Before'`},
		{"relationship-lock", `INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at) SELECT 'item',id,'genre','[]','owner',1,'owner','now' FROM catalog_entities WHERE title='Before'`},
		{"side-delete", `DELETE FROM catalog_item_details WHERE entity_id IN(SELECT id FROM catalog_entities WHERE title='Before')`},
	} {
		for _, final := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/final=%v", tc.name, final), func(t *testing.T) {
				s, db := editorFixture(t)
				base := previewSQLSource(s, catalog.BulkMetadataSelection{SQL: `SELECT id FROM catalog_entities WHERE title='Before'`})
				calls := 0
				source := func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
					calls++
					if calls == 3 {
						if _, e := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, tc.sql); e != nil {
							t.Fatal(e)
						}
					}
					page, e := base(ctx, tx, after)
					// Force a non-final empty source page to test advancement independently
					// of match count. In the final case, call three is final revalidation.
					if !final && calls <= 2 {
						page.Selection.SQL = ""
						page.Done = false
						page.After = after + 1
					}
					return page, e
				}
				p, err := s.BulkPreview(context.Background(), source, previewAuthorize)
				if !errors.Is(err, catalog.ErrStaleContinuation) || p.Total != 0 {
					t.Fatalf("mutation leaked success total=%d %v", p.Total, err)
				}
				assertPreviewScratchClean(t, s)
			})
		}
	}
}

func TestBulkMetadataPreviewFinalAuthorityAndCancellationDiscardAggregate(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			s, targets, _ := bulkFixture(t)
			in := catalog.BulkMetadataPreviewRequest{Targets: []catalog.BulkMetadataTarget{{Kind: "item", ContentKind: "movie", ID: targets[0].ID, LibraryID: "movies"}}}
			base := previewRequestSource(s, in)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			denied := errors.New("revoked")
			source := func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
				calls++
				if calls == 3 {
					if cancelled {
						cancel()
					} else {
						dbwork.BumpAuthority()
					}
				}
				return base(ctx, tx, after)
			}
			p, err := s.BulkPreview(ctx, source, func(context.Context, *sql.Tx) error {
				if !cancelled && calls >= 3 {
					return denied
				}
				return nil
			})
			if p.Total != 0 || cancelled && !errors.Is(err, context.Canceled) || !cancelled && !errors.Is(err, denied) {
				t.Fatalf("late revocation/cancel leaked aggregate total=%d %v", p.Total, err)
			}
			assertPreviewScratchClean(t, s)
		})
	}
}

func TestBulkMetadataPreviewExplicitPagingPreservesCASOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db := editorFixture(t)
	fixture := catalogtest.New(t, db)
	request := catalog.BulkMetadataPreviewRequest{}
	for i := 0; i < catalog.BulkMetadataPageSize+1; i++ {
		item := fixture.Movie(fixture.Handle("movies"), fmt.Sprintf("/movies/cas-%d.mkv", i), fmt.Sprint(i), 2020)
		request.Targets = append(request.Targets, catalog.BulkMetadataTarget{Kind: "item", ContentKind: "movie", ID: item.Public, LibraryID: "movies"})
	}
	fixture.Drain()
	p, err := s.BulkPreview(context.Background(), previewRequestSource(s, request), previewAuthorize)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != len(request.Targets) {
		t.Fatal(len(p.Targets))
	}
	for i, target := range request.Targets {
		state, e := s.RepairState(context.Background(), RepairTarget{Kind: target.Kind, ID: target.ID}, allowAll)
		if e != nil {
			t.Fatal(e)
		}
		if p.Targets[i].ID != target.ID || p.Targets[i].ExpectedRevision != state.Revision {
			t.Fatalf("target/CAS %d differs %+v %+v", i, p.Targets[i], state)
		}
	}
	if year := p.Fields["year"]; len(year.Values) != 1 || year.Values[0].Value != "2020" || year.Values[0].Count != int64(len(request.Targets)) {
		t.Fatal(year)
	}
	assertPreviewScratchClean(t, s)
}

func TestBulkMetadataPreviewReadingQueryUsesCanonicalOwnerAttribution(t *testing.T) {
	s, c, target := readingOwnerFixture(t, compactcatalog.Ebook, "book")
	s.cacheRoot = t.TempDir()
	state, err := s.RepairState(context.Background(), target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	author := "Owner Author"
	_, err = s.Repair(context.Background(), target, RepairCommand{ExpectedRevision: state.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"author": {Value: &author}}}, editorActor(), allowAll)
	if err != nil {
		t.Fatal(err)
	}
	c.Drain()
	source := func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
		var revision string
		if e := tx.QueryRowContext(ctx, `SELECT printf('%d:%d',revision,metadata_revision) FROM library_revisions WHERE library_id='read'`).Scan(&revision); e != nil {
			return catalog.BulkMetadataSelectionPage{}, e
		}
		return catalog.BulkMetadataSelectionPage{Done: true, Fence: fmt.Sprint(revision), Selection: catalog.BulkMetadataSelection{SQL: `SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, Args: []any{target.ID}, Revision: "reading-query"}}, nil
	}
	p, err := s.BulkPreview(context.Background(), source, previewAuthorize)
	if err != nil {
		t.Fatal(err)
	}
	field := p.Fields["author"]
	if p.Total != 1 || len(p.Targets) != 0 || field.State != "shared" || field.Value == nil || *field.Value != author || field.Locked != 1 {
		t.Fatal(p.Total, p.Targets, field)
	}
	assertPreviewScratchClean(t, s)
}

func TestBulkMetadataPreviewLeavesOwnerLongPermitAvailable(t *testing.T) {
	s, targets, _ := bulkFixture(t)
	_, release, err := dbwork.AcquireLongRead(context.Background(), dbwork.LongReadIntegrity)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	request := catalog.BulkMetadataPreviewRequest{Targets: []catalog.BulkMetadataTarget{{Kind: "item", ContentKind: "movie", ID: targets[0].ID, LibraryID: "movies"}}}
	base := previewRequestSource(s, request)
	source := func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > dbwork.ForegroundSnapshotLifetime {
			t.Fatal("preview snapshot has no ordinary lifetime bound")
		}
		return base(ctx, tx, after)
	}
	p, err := s.BulkPreview(context.Background(), source, previewAuthorize)
	if err != nil || p.Total != 1 {
		t.Fatal(p.Total, err)
	}
	assertPreviewScratchClean(t, s)
}

func TestBulkMetadataPreviewAllowsUnrelatedProgressBetweenPages(t *testing.T) {
	s, db := editorFixture(t)
	base := previewSQLSource(s, catalog.BulkMetadataSelection{SQL: `SELECT id FROM catalog_entities WHERE title='Before'`})
	calls := 0
	source := func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
		calls++
		if calls == 3 {
			if _, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, `INSERT INTO progress(profile_id,item_id,position,playback_id) SELECT 'other-viewer',id,5000,'other-playback' FROM catalog_entities WHERE title='Before'`); err != nil {
				t.Fatal(err)
			}
		}
		return base(ctx, tx, after)
	}
	p, err := s.BulkPreview(context.Background(), source, previewAuthorize)
	if err != nil || p.Total != 1 {
		t.Fatal(p.Total, err)
	}
	assertPreviewScratchClean(t, s)
}
