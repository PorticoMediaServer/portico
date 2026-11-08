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
	if err := e.db.QueryRowContext(t.Context(), `SELECT discovered_count,revision,last_command_sequence FROM inventory_runs WHERE job_id=?`, r.scope.runID).Scan(&discovered, &revision, &sequence); err != nil {
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
