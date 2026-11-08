//go:build linux

package writer

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestInventoryBatchAcceptAndReplay(t *testing.T) {
	for _, width := range []int{1, 2, maxInventoryBatchFiles} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			d, p, command, q := inventoryComposedDriverPrepared(t, width, n1BatchForecastFixture)
			c := command.(*inventoryAcceptCommand)
			e := d.opening.executor
			in := n1DriverInputFrom(c)
			before := n1DriverCaptureBaseline(t, t.Context(), e.db, in)
			out, err := p.Accept(t.Context(), command)
			if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed || out.ResultCount != int64(width) || len(out.Members) != width {
				t.Fatal(out, err)
			}
			if err = checkInventoryBatchEffects(t, e, c, before, out); err != nil {
				t.Fatal(err)
			}
			if err = p.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			checkpoint, err := n1DriverCheckpoint(t.Context(), e.db)
			if err != nil {
				t.Fatal(err)
			}
			policy := d.batchPolicy
			saved, err := d.ResolveScan(t.Context(), q)
			if err != nil || !saved.Outcome.Replayed || saved.Digest != out.Digest || !reflect.DeepEqual(saved.Members, out.Members) {
				t.Fatal(saved, err)
			}
			if d.batchPolicy != policy {
				t.Fatal("replay fed batch controller")
			}
			after, err := n1DriverCheckpoint(t.Context(), e.db)
			if err != nil || !reflect.DeepEqual(checkpoint, after) {
				t.Fatal("replay effects", err)
			}
			ack, err := d.AckScan(t.Context(), q)
			if err != nil || ack.AcknowledgedThrough != q.Receipt.Identity.Sequence {
				t.Fatal(ack, err)
			}
			retired, err := d.ResolveScan(t.Context(), q)
			if err != nil || retired.Outcome.State != ResultRetired || len(retired.Members) != 0 {
				t.Fatal(retired, err)
			}
		})
	}
}

// The expected file facts and shared deltas come from original input and raw
// pre-state. No returned result supplies its own expected catalogue content.
func checkInventoryBatchEffects(t *testing.T, e *executor, c *inventoryAcceptCommand, b n1DriverBaseline, out InventoryResult) error {
	t.Helper()
	r := c.request
	var discovered, revision, sequence, processed, pages, children int64
	var cursor string
	if err := e.db.QueryRowContext(t.Context(), `SELECT discovered,job_revision,last_command_sequence FROM inventory_runs WHERE job_id=?`, r.scope.runID).Scan(&discovered, &revision, &sequence); err != nil {
		return err
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT processed FROM jobs WHERE id=?`, r.scope.runID).Scan(&processed); err != nil {
		return err
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT cursor,pages FROM inventory_directories WHERE job_id=? AND relative_path='.'`, r.scope.runID).Scan(&cursor, &pages); err != nil {
		return err
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_results WHERE run_id=? AND command_sequence=?`, r.scope.runID, c.identity.Sequence).Scan(&children); err != nil {
		return err
	}
	n := int64(len(r.files))
	if discovered != b.RunDiscovered+n || revision != b.RunRevision+1 || sequence != c.identity.Sequence || processed != b.JobProcessed+n || pages != b.DirectoryPages+1 || cursor != r.directory.nextCursor || children != n {
		return fmt.Errorf("batch progress delta %d %d %d %d %d %s %d", discovered, revision, sequence, processed, pages, cursor, children)
	}
	var data, order, metadata, membership int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT data_revision,data_order_revision,data_metadata_revision FROM libraries WHERE id=?`, r.scope.libraryKey).Scan(&data, &order, &metadata); err != nil {
		return err
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT sequence FROM catalog_membership_clock WHERE singleton=1`).Scan(&membership); err != nil {
		return err
	}
	if data != b.LibraryData+n || order != b.LibraryOrder+n || metadata != b.LibraryMetadata+n || membership != b.Membership+n {
		return fmt.Errorf("batch aggregate deltas")
	}
	for _, table := range []string{"catalog_subjects", "catalog_identities", "catalog_assets", "catalog_entities", "catalog_asset_links", "inventory_objects", "catalog_scalar_contributions", "catalog_asset_contributions", "scan_command_receipts", "scan_command_results", "search_publish_jobs"} {
		var count int64
		if err := e.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+n1DriverQuote(table)).Scan(&count); err != nil {
			return err
		}
		delta := n
		if table == "scan_command_receipts" {
			delta = 1
		}
		if count != b.Counts[table]+delta {
			return fmt.Errorf("batch %s cardinality %d", table, count)
		}
	}
	var ageInput, agePublished, unrated, searchSequence, pending int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT age_input_revision,age_published_revision FROM libraries WHERE id=?`, r.scope.libraryKey).Scan(&ageInput, &agePublished); err != nil {
		return err
	}
	wantPublished := b.LibraryAgePublished
	if b.LibraryAgeInput == b.LibraryAgePublished && b.LibraryAgePending == 0 {
		wantPublished += n
	}
	if ageInput != b.LibraryAgeInput+n || agePublished != wantPublished {
		return errors.New("batch age publication")
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT total FROM catalog_age_item_counts WHERE library_id=? AND kind=1 AND block_unrated=0 AND admission_age=-1`, r.scope.libraryID).Scan(&unrated); err != nil {
		return err
	}
	if unrated != max(0, b.AgeUnratedTotal)+n {
		return errors.New("batch unrated count")
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT input_sequence FROM search_revision WHERE singleton=1`).Scan(&searchSequence); err != nil {
		return err
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT pending_subjects FROM search_generations WHERE id=?`, b.SearchActiveGeneration).Scan(&pending); err != nil {
		return err
	}
	if searchSequence != b.SearchInputSequence+n || pending != b.BankPending+n {
		return errors.New("batch search invalidation")
	}
	for i, f := range r.files {
		var object, title, revision, path string
		var subject, asset, epoch, size, modified int64
		err := e.db.QueryRowContext(t.Context(), `SELECT o.id,e.id,a.id,a.info_epoch,e.title,a.path,o.revision,a.size,a.modified_ns
FROM catalog_identities i JOIN catalog_entities e ON e.id=i.subject_id
JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id
JOIN inventory_objects o ON o.asset_id=a.token
WHERE i.library_id=? AND i.source_key=?`, r.scope.libraryID, f.sourceKey).Scan(&object, &subject, &asset, &epoch, &title, &path, &revision, &size, &modified)
		if err != nil {
			return err
		}
		if title != f.title || path != f.assetPath || revision != f.revision || size != f.size || modified != f.modifiedNS {
			return fmt.Errorf("batch file %d differs from original", i)
		}
		want := InventoryMember{InputOrdinal: int64(i), ObjectID: object, LibraryID: r.scope.libraryID, SubjectID: subject, AssetID: asset, AcceptedAssetRevision: epoch}
		if out.Members[i] != want {
			return fmt.Errorf("batch result member %d differs from raw rows", i)
		}
		view := *c
		view.request.files = c.request.files[i : i+1]
		original := n1DriverInputFrom(&view)
		stable, evidence, observedRevision, err := n1DriverObjectFacts(original)
		if err != nil {
			return err
		}
		if observedRevision != f.revision {
			return errors.New("batch original evidence revision")
		}
		points := []struct {
			query string
			args  []any
			want  []any
		}{
			{`SELECT sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,year,metadata_language,retired FROM catalog_entities WHERE id=?`, []any{subject}, []any{f.sortTitle, f.sortKey, f.sortHead, f.sortKeyKeepArticles, f.sortHeadKeepArticles, f.year, f.metadataLanguage, int64(0)}},
			{`SELECT object_identity,evidence_json,state,analysis_state,analysis_revision,has_analysis_probe_bindings FROM inventory_objects WHERE id=?`, []any{object}, []any{stable, evidence, "available", "pending", "", int64(0)}},
			{`SELECT membership_sequence,membership_sequence= ? FROM catalog_scalar_contributions WHERE parent_id=? AND contributor_id=? AND source=0`, []any{b.Membership + int64(i) + 1, subject, subject}, []any{b.Membership + int64(i) + 1, int64(1)}},
			{`SELECT membership_refs,authored_refs,file_size FROM catalog_asset_contributions WHERE parent_id=? AND asset_id=?`, []any{subject, asset}, []any{int64(1), int64(0), f.size}},
		}
		for _, point := range points {
			raw, err := n1DriverRow(t.Context(), e.db, point.query, point.args...)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(raw, point.want) {
				return fmt.Errorf("batch file %d raw %v != %v", i, raw, point.want)
			}
		}
	}
	return nil
}

func TestInventoryBatchPlanReservesAllDimensions(t *testing.T) {
	d, _ := n1ComposedDriverFixture(t, n1BatchForecastFixture)
	p := d.writer.effective.inventoryDriverPlan
	remaining := subtract(subtract(p.recipe.limit, p.recipe.setup), p.recipe.terminal)
	if _, err := reserveInventoryBatchSteps(remaining, p.steps, maxInventoryBatchFiles); err != nil {
		t.Fatal(err)
	}
	if p.need[governor.WALBytes] != int64(p.recipe.limit.WALBytes) || p.recipe.limit.Hold != MaxCommandHold {
		t.Fatal("changed hold/WAL contract")
	}
	for _, width := range []int{0, maxInventoryBatchFiles + 1} {
		if _, err := reserveInventoryBatchSteps(remaining, p.steps, width); !errors.Is(err, ErrInvalid) {
			t.Fatal("unbounded width", width, err)
		}
	}
	if _, err := reserveInventoryBatchSteps(Work{}, p.steps, 2); !errors.Is(err, ErrQuantum) {
		t.Fatal("unfunded prefix", err)
	}
}

func TestInventoryBatchFailureRollsBackAndRetryKeepsWidth(t *testing.T) {
	d, p, command, q := inventoryComposedDriverPrepared(t, 2, n1BatchForecastFixture)
	e := d.opening.executor
	c := command.(*inventoryAcceptCommand)
	var collision int64
	if err := e.db.QueryRowContext(t.Context(), `INSERT INTO catalog_subjects(public_id,library_id,kind) VALUES(randomblob(16),7,1) RETURNING id`).Scan(&collision); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(7,?,?)`, c.request.files[1].sourceKey, collision); err != nil {
		t.Fatal(err)
	}
	before, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Accept(t.Context(), command)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, InventoryResult{}) {
		t.Fatal("bad second member accepted a prefix", out, err)
	}
	after, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed batch changed raw state", err)
	}
	pending, err := d.ResolveScan(t.Context(), q)
	if err != nil || pending.Outcome.State != Pending {
		t.Fatal("failed original unresolved", pending, err)
	}
	if _, err = e.db.ExecContext(t.Context(), `DELETE FROM catalog_identities WHERE library_id=7 AND source_key=?`, c.request.files[1].sourceKey); err != nil {
		t.Fatal(err)
	}
	if _, err = e.db.ExecContext(t.Context(), `DELETE FROM catalog_subjects WHERE id=?`, collision); err != nil {
		t.Fatal(err)
	}
	// Force future originals to N=1. Retrying THIS already frozen original
	// still submits both files; neither preparation nor Accept calls limit.
	d.batchPolicy.width = 1
	if p.preparation.request.Limit != 2 || len(c.request.files) != 2 {
		t.Fatal("retry width changed before execution")
	}
	out, err = p.Accept(t.Context(), command)
	if err != nil || out.ResultCount != 2 || out.Outcome.Replayed {
		t.Fatal("same-original retry did not apply once", out, err)
	}
	if d.batchPolicy.width != 1 || p.preparation.request.Limit != 2 {
		t.Fatal("retry resized the original")
	}
}
