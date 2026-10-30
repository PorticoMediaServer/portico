package remotesources

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestScanSnapshotKeepsTransportAndCatalogueRevisionsDistinct(t *testing.T) {
	f := newObservationFixture(t)
	ref, err := f.record(t, scanFacts("snapshot"), 0)
	if err != nil {
		t.Fatal(err)
	}
	recorded, snapshot, err := f.s.ScanSnapshot(context.Background(), f.file)
	if err != nil || recorded.Ref != ref || snapshot.ChangeToken != recorded.Observation.ScanRevision || snapshot.Size != 16 || snapshot.Path != f.file {
		t.Fatal(recorded, snapshot, err)
	}
	wantRevision := f.catalogObservationRevision(t)
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	inTx, txSnapshot, err := f.s.ScanSnapshotTx(context.Background(), view.Tx(), f.file)
	if err != nil || inTx.Ref != ref || txSnapshot != snapshot {
		t.Fatal(inTx, txSnapshot, err)
	}
	source, err := catalog.InventoryFenceTx(context.Background(), view.Tx(), f.job)
	if err != nil {
		t.Fatal(err)
	}
	revision, _, err := catalog.ObservationRevision(source, snapshot)
	if err != nil || revision == snapshot.ChangeToken || revision != wantRevision {
		t.Fatal("transport token became catalogue identity", revision, snapshot.ChangeToken, err)
	}
	if len(f.s.serveLeases) != 0 || len(f.s.readLeases) != 0 {
		t.Fatal("recorded projection contacted the provider")
	}
}

func TestScanSnapshotRejectsCurrentTransactionChanges(t *testing.T) {
	for _, change := range []struct{ name, sql string }{
		{"root", `UPDATE remote_sources SET root=root||'/replacement'`},
		{"kind", `UPDATE remote_sources SET kind='smb'`},
		{"generation", `UPDATE remote_sources SET generation=generation+1`},
		{"removed", `UPDATE remote_sources SET removed=1`},
		{"transport-token", `UPDATE remote_scan_observations SET scan_revision='invented'`},
		{"missing", `DELETE FROM remote_scan_observations`},
	} {
		t.Run(change.name, func(t *testing.T) {
			f := newObservationFixture(t)
			if _, err := f.record(t, scanFacts("snapshot"), 0); err != nil {
				t.Fatal(err)
			}
			writer, err := dbwork.Begin(context.Background(), f.s.db, dbwork.ClassInteractive)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback()
			if _, err := writer.Tx().Exec(change.sql); err != nil {
				t.Fatal(err)
			}
			_, snapshot, err := f.s.ScanSnapshotTx(context.Background(), writer.Tx(), f.file)
			if err == nil || snapshot != (storage.Snapshot{}) {
				t.Fatal("transaction-local source change returned bytes evidence", snapshot, err)
			}
		})
	}
}
